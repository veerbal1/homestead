# Homestead

A deliberately small Go URL shortener, taken through a **full production
lifecycle** by one person — the point of this project is everything *around*
the app: provision, deploy, data, caching, observability, change, recovery,
and capacity, each owned end-to-end the way a solo/startup engineer would.

The app is intentionally tiny. The interesting part is the envelope.

Deployed to **https://homestead.undercoverdevs.com** — provisioned by Terraform
and spun up on demand (torn down between sessions to control cost), so it isn't
kept always-on.

---

## What it does

- `POST /shorten` — create a short code for a URL (API-key protected, **rate limited**)
- `GET /r/{code}` — redirect to the original URL (**Redis cache-aside** in front of Postgres)
- `GET /healthz` — liveness (process up, no DB dependency)
- `GET /readyz` — readiness (checks the database; gates traffic — **not** the cache)
- `GET /version` — the running git SHA (build-stamped via `-ldflags`)
- `GET /metrics` — Prometheus metrics (RED + cache hit/miss)

## Architecture

```
client ──HTTPS──> Caddy (:443, auto TLS) ──> app (Go, :8080, X-API-Key)
                                              │
                                              ├──> Redis (cache + rate limiter)
                                              │      dev: local container · prod: Upstash (TLS)
                                              │
                                              └──> Neon (managed Postgres)

traces ──OTLP──> Jaeger (local) / a managed backend is a config swap
infra: one EC2 box, provisioned by Terraform (SG + instance + Docker via cloud-init)
```

Single node, no HA (see [What I'd do next](#what-id-do-next)).

## Tech

Go · [pgx](https://github.com/jackc/pgx) · Neon (managed Postgres) ·
[go-redis](https://github.com/redis/go-redis) · Upstash (managed Redis) ·
[goose](https://github.com/pressly/goose) migrations · Docker (multi-stage,
distroless, non-root) · **Terraform** (IaC) · **Caddy** (reverse proxy + auto
HTTPS) · GitHub Actions · `slog` structured logging · Prometheus client ·
**OpenTelemetry** traces · [k6](https://k6.io) load testing.

## Run it locally

```bash
cp .env.example .env   # set POSTGRES_*, DATABASE_URL, REDIS_URL, API_KEY
docker compose up
```

Brings up Postgres + Redis, runs migrations, then the app on `:8080`.

```bash
curl -X POST localhost:8080/shorten \
  -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{"url":"https://example.com"}'
```

---

## Lifecycle

This is the actual point of the project.

### Provision — infrastructure as code
[`infra/`](infra) is Terraform: the security group, the EC2 instance, and an
AMI lookup. Docker + the compose plugin install themselves on first boot via
`user_data` (cloud-init) — no manual SSH. `terraform apply` brings the box up;
`terraform destroy` tears it down to $0. The whole environment is reproducible
and disposable, which is how cost stays near zero between sessions.

### Deploy — merge = deploy
Push to `main` runs [`deploy.yml`](.github/workflows/deploy.yml): **test →
build → deploy**. Nobody SSHes to release.
- Image is multi-stage, distroless, non-root, tagged by **git SHA** (never
  `latest`) and pushed to GHCR.
- The deploy job runs migrations, ships the compose file + Caddyfile + a `.env`
  (built from GitHub secrets, incl. `REDIS_URL`) to the box, then
  `docker compose pull && up -d` for that SHA. A fresh Terraform box is fully
  configured by CI — nothing is hand-placed.
- **Caddy** terminates TLS and reverse-proxies to the app; it obtains and
  renews Let's Encrypt certificates automatically from just the domain name.

### Test — CI actually tests
Unit tests for the core logic ([`internal/shortener`](internal/shortener)):
- `CleanLink` — **behaviour** tests (table-driven, matched on stable error
  substrings, not brittle exact strings).
- `GenerateSlug` — **property** test (length + charset invariants over 1000
  runs; randomness is a security feature, so it's tested by properties, not
  fixed output).

### Data — managed Postgres, owned edges
- Neon (managed) — provider handles hardware/patching/backup plumbing.
- **Migrations run in the pipeline** (`goose up` on Neon), before rollout,
  idempotent — never at app boot, never by hand.
- Connection pooling; safe schema changes via **expand/contract** +
  `lock_timeout` (documented from a real lock-queue incident).

### Cache — Redis cache-aside, fail-open
- The redirect read path is **cache-aside**: check Redis, on a miss read
  Postgres and write the key back (24h TTL, `link:` key prefix for future
  format versioning). Measured **~99% hit ratio** under load; Postgres reads
  drop by the same factor.
- **Fail-open by design.** If Redis errors or is down, the request silently
  falls back to Postgres — the cache is an optimisation, never a gate.
  `/readyz` deliberately does **not** depend on Redis.
- **Verified, not assumed:** in a kill-drill (kill Redis mid-load) the service
  degraded to Postgres with **0% failed requests**, not a crash. The exercise
  also exposed that fail-open must be fail-*fast* — with default client
  timeouts a dead cache added multi-second tail latency, so the client uses
  short read/write timeouts and no retries.
- Cache hits/misses/errors are exported on `/metrics` (`cache_requests_total`).
- dev = local Redis container; prod = **Upstash** (managed, TLS) — the same
  one-line `REDIS_URL` swap as the DB.

### Rate limiting
- Fixed-window limiter on `POST /shorten` (Redis `INCR` + `EXPIRE`), returns
  `429` + `Retry-After`. Shared in Redis so the limit is global, not per
  instance. **Fail-open** too — a Redis blip must not lock out real users
  (the app protects against abuse, and availability wins over a brief loss of
  that protection). See [What I'd do next](#what-id-do-next) for the atomicity
  refinement.

### Observe
- `/readyz` gates traffic; the app does **not** crash when the DB is
  unreachable (liveness stays up, readiness reports 503 honestly).
- Structured JSON logs (`slog`) with a request id per request.
- `/metrics` (RED + cache); `/version` stamped with the SHA.
- **Distributed tracing** (OpenTelemetry): HTTP, Redis, and pgx are
  instrumented, so one request's waterfall shows *where the time went*
  (handler → cache get → DB select → cache set). Exported over OTLP to a local
  Jaeger; a managed backend (Grafana Cloud/Tempo) is a config swap, deferred.
  Tracing is guarded — with no OTLP endpoint set (prod), it's a no-op, never a
  crash.
- External uptime monitoring (UptimeRobot) on `/healthz` and `/readyz`.

### Change — safe schema evolution
- `lock_timeout` so a migration safe-fails instead of freezing writers behind
  a lock queue; **expand/contract** (add → dual-write → backfill → switch →
  drop) so a rolling deploy never has code and schema out of sync.

### Recover
- **Smoke gate:** after deploy, CI retries `/readyz` until 200 and asserts
  `/version == <deployed SHA>` over **HTTPS** — a green deploy means the app is
  *actually* serving the new code, not just "container started".
- **One-click rollback:** [`rollback.yml`](.github/workflows/rollback.yml)
  (`workflow_dispatch` + SHA input) redeploys any previously built image.
  Deterministic because images are SHA-tagged; verified over HTTPS.
- **Incident practice:** a game-day drill + blameless postmortem —
  [`docs/postmortems`](docs/postmortems).
- **Runbook:** [`docs/runbook.md`](docs/runbook.md) — deploy, rollback, key
  rotation, logs, box start/stop.

### Capacity — load tested, honestly
Load tests with [k6](https://k6.io) (`k6/`), using a ramping **arrival-rate**
model (target a request rate, not a fixed VU count — otherwise the load
throttles itself as the app slows and never finds the ceiling):
- Under overload the box **sheds load and recovers** — it refused connections
  at the peak (backpressure) rather than crashing, and came back healthy once
  load dropped.
- **Deploy-under-load gap:** a single-container `--force-recreate` under steady
  load drops **~1.5s of traffic** (old container stops before the new one
  listens). That number is exactly what justifies a zero-downtime deploy.
- A *clean* absolute ceiling needs a same-region load box (running k6 from a
  laptop over the internet measures the laptop and the network, not the app) —
  treated as a separate performance exercise, not chased here.

### SLO & error budget
- **SLIs:** redirect success rate; % of redirects under 100ms.
- **SLOs:** 99.9% success · 99% under 100ms.
- **Error budget** = 0.1%. Budget burn drives the response: fast burn → page,
  slow burn → ticket, exhausted → freeze risky changes, healthy → ship. It
  turns "should I panic?" into arithmetic — and the ~1.5s deploy gap above,
  across many deploys a day, is enough to threaten the budget on its own, which
  is the concrete case for zero-downtime deploys.

---

## Decision records

Format: *options · choice · why · what would change it.*

- **Provisioning** — click-ops / shell script · **Terraform**. State-aware and
  declarative: it applies only the diff, is idempotent, and `destroy` cleanly
  removes everything — which makes a disposable, rebuild-on-demand box cheap.
  *Change if:* nothing — IaC is the baseline.
- **Box bootstrap** — SSH in and install by hand · **`user_data` (cloud-init)**.
  The box configures itself on first boot; no human, no drift, reproducible.
  *Change if:* config outgrows a boot script → a real config/image build step.
- **TLS** — app terminates TLS itself · **reverse proxy (Caddy)**. The app
  stays plain HTTP and portable; Caddy owns certs + renewal in one place.
  *Change if:* on Kubernetes → Ingress + cert-manager instead.
- **Managed vs self-hosted DB** — self-host container · **managed (Neon)**.
  Managed removes toil (backup/failover/patching); the edges I still own
  (connections, migrations, restores) are the same either way. *Change if:*
  compliance/scale forces a self-managed cluster.
- **Cache placement** — in-process map · **shared Redis**. An in-process cache
  diverges across replicas (inconsistent, low hit-ratio, no shared
  invalidation) and dies on restart; a shared Redis is one source of truth.
  *Change if:* a single instance where per-node caching is genuinely enough.
- **Cache/rate-limit failure mode** — fail-closed · **fail-open**. The cache is
  an optimisation and the limiter guards against abuse; a Redis outage should
  degrade (serve from DB, drop the limit briefly), not take the service down.
  `/readyz` never depends on Redis. *Change if:* the limiter guards a fragile,
  expensive downstream → fail-closed there.
- **Redis hosting** — container on the box · **local container (dev) + Upstash
  (prod)**. Managed removes the second stateful thing to run on a 1GB box; TLS
  and zero idle cost. *Change if:* the app moves inside a VPC → ElastiCache.
- **Tracing backend** — managed cloud first · **local Jaeger, managed deferred**.
  Verify the instrumentation locally (fast, no account), then swap the OTLP
  endpoint. Deep dashboards are a separate tool concern. *Change if:* real
  production traffic needs retained, queryable traces → Grafana Cloud/Tempo.
- **Boot behaviour on DB down** — crash on boot · **stay up, readiness gates**.
  A DB blip shouldn't kill the process; liveness stays green, `/readyz` reports
  503, traffic is gated. *Change if:* the app genuinely can't function without
  a warm dependency at start.
- **Image tag** — `latest` · **git SHA**. SHA-tagging makes every deploy and
  rollback deterministic and individually addressable. *Change if:* never.
- **Build platforms** — multi-arch · **amd64-only**. The box is x86 and nothing
  consumes arm64 today; arm64 via emulation cost ~5 min/push for zero benefit
  (build 6.5 min → 56 s). *Change if:* local arm64 or Graviton — re-add arm64
  with a build cache.
- **CI → box access** — SSM · **SSH, port 22 open, key-only auth**. Simplest
  path that works for a single learning box. *Change if:* production — replace
  with SSM Session Manager (no inbound SSH port).

---

## What I'd do next

Honest gaps, roughly in priority:

- **Zero-downtime deploy** — the measured ~1.5s recreate gap is the reason:
  start the new container, wait until healthy, drain the old (rolling /
  blue-green / canary) so a deploy drops zero requests.
- **Atomic rate limiter** — `INCR` + `EXPIRE` are two commands; a crash between
  them can leave a key with no TTL (permanent lockout). Fold them into one Lua
  script so Redis runs them atomically.
- **Stable address / DNS automation** — the box uses an auto-assigned IP, so a
  rebuild needs the DNS `A` record + `BOX_HOST` secret updated (an Elastic IP
  or a DNS-API script would remove that manual step).
- **Secrets manager** — secrets live in GitHub Actions secrets + a box `.env`;
  move to SSM Parameter Store / a secrets manager.
- **Managed traces + clean capacity number** — export traces to Grafana
  Cloud/Tempo; run k6 from a same-region box for an attributable ceiling.
- **Kubernetes (k3s)** — the same loop on a real cluster (Ingress, rollouts,
  RBAC), with a load test proving zero failed requests on a rolling deploy.
- **Correctness on the money path** — idempotency keys, a queue consumer with a
  DLQ, and a reconciliation job.
- **HA** — this is single-node by design; no redundancy yet.
