# Postmortem: Shorten requests failing during a schema migration

**Date:** 14 Sep, 2026 18:45 GMT
**Author:** Veerbal
**Status:** Resolved
**Severity:** SEV2 (user-facing writes failing; reads unaffected)

## Summary

During a schema change on the `links` table, all `POST /shorten` requests began timing out and failing for ~2 minutes. Root cause was a long-running transaction holding a lock on `links`; the migration's `ALTER TABLE` queued behind it, and its
pending lock request in turn blocked all new writes. Resolved by ending the
offending transaction, which released the lock and let the queue drain.

## Impact

- `POST /shorten` requests hung, then failed with an empty reply after the app's
10s `WriteTimeout` fired. Users could not create short links.
- `GET /{code}` (reads) and `/readyz` were unaffected — the DB and app were both
"healthy" the whole time. This was a lock-contention incident, not an outage of
any component.



## Timeline (UTC)

- **T0** — A transaction opened on `links` (`BEGIN; SELECT ...`) and was left open,
holding a light `ACCESS SHARE` lock.
- **T0+** — A migration ran `ALTER TABLE links ADD COLUMN ...`, which needs an
`ACCESS EXCLUSIVE` lock. It could not acquire it and began waiting.
- **T0++** — New `POST /shorten` INSERTs arrived. Although they don't conflict with
the open transaction's light lock, they queued *behind the migration's pending
ACCESS EXCLUSIVE request*, so they blocked.
- **T0+10s** — Blocked requests exceeded the server's `WriteTimeout` (10s); the app
closed the connections, surfacing as failed requests to clients.
- **Tn** — On-call ended the offending transaction (`ROLLBACK`). The lock released,
the migration completed instantly, and queued INSERTs drained. Fresh requests
returned 200.

> Note: This was a controlled game-day drill, not a production incident.
> Timings are from the exercise.

## Root Cause

Postgres queues lock requests. A migration's *pending* heavy lock request blocks
new writers even before it is granted. Because an unrelated transaction held the
table lock and never committed, the migration — and every write behind it — was
stuck. The trigger was a transaction left open in-flight; the amplifier was running
a lock-taking migration while that transaction was live.

## Detection

Observed as a spike in failed/slow `POST /shorten` requests (empty replies after
10s). Components' own health checks stayed green, which is why symptom-based
alerting (user-facing error/latency) matters more than component-health alerts here.

## Resolution

`ROLLBACK` of the stuck transaction released the root lock; the system self-drained.
No data was lost (the migration is additive; failed requests were never committed).

## Prevention (action items)

1. **Set** `lock_timeout` **on all migrations** (e.g. `SET lock_timeout = '2s'`) so an
  `ALTER` that can't get its lock quickly *fails fast* instead of holding writers
   hostage. A failed migration we retry is far cheaper than a user-facing outage.
2. **Enforce** `idle_in_transaction_session_timeout` on the database so any
  transaction left open (bug, forgotten commit) is killed automatically before it
   can block others. (Neon already did this to us during testing — it's a real defense.)
3. **Alert on lock waits**, not just on component health: page when queries are
  blocked waiting on locks beyond N seconds (visible in `pg_stat_activity` /
   `pg_locks`). This turns a silent jam into an early signal.
4. **Keep migrations small and additive**, and avoid running lock-taking DDL while
  long transactions may be in flight; prefer expand/contract for column changes.



## What went well / what to improve

- **Well:** The app's `WriteTimeout` converted an unbounded hang into a bounded 10s
failure — the blast radius was contained.
- **Improve:** We had no alert on lock waits; detection was manual. Action item #3
closes that gap.

