# Homestead

A deliberately small Go URL shortener, taken through a **full production
lifecycle** by one person — the point of this project is everything *around*
the app: deploy, data, observability, change, and recovery, each owned
end-to-end the way a solo/startup engineer would.

The app is intentionally tiny. The interesting part is the envelope.

---

## What it does

- `POST /shorten` — create a short code for a URL (API-key protected)
- `GET /r/{code}` — redirect to the original URL
- `GET /healthz` — liveness (process up, no DB dependency)
- `GET /readyz` — readiness (checks the database; gates traffic)
- `GET /version` — the running git SHA (build-stamped via `-ldflags`)
- `GET /metrics` — Prometheus metrics (RED)

## Architecture

```
client ──HTTP──> EC2 box ──> app (Go, :8080, X-API-Key)
                              │
                              └──> Neon (managed Postgres)
```

Single node, HTTP only (see [What I'd do next](#what-id-do-next)).

## Tech

Go · [pgx](https://github.com/jackc/pgx) · Neon (managed Postgres) ·
[goose](https://github.com/pressly/goose) migrations · Docker (multi-stage,
distroless, non-root) · GitHub Actions · `slog` structured logging ·
Prometheus client.

## Run it locally

```bash
cp .env.example .env   # set POSTGRES_*, DATABASE_URL, API_KEY
docker compose up
```

Brings up Postgres, runs migrations, then the app on `:8080`.

```bash
curl -X POST localhost:8080/shorten \
  -H "X-API-Key: $API_KEY" -H "Content-Type: application/json" \
  -d '{"url":"https://example.com"}'
```

---

## Lifecycle

This is the actual point of the project.

### Deploy — merge = deploy
Push to `main` runs [`deploy.yml`](.github/workflows/deploy.yml): **test →
build → deploy**. Nobody SSHes to release.
- Image is multi-stage, distroless, non-root, tagged by **git SHA** (never
  `latest`) and pushed to GHCR.
- The deploy job runs migrations, then SSHes to the box and does
  `docker compose pull && up -d` for that SHA.

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
  `lock_timeout` (documented from a real lock-queue incident, below).

### Observe
- `/readyz` gates traffic; the app does **not** crash when the DB is
  unreachable (liveness stays up, readiness reports 503 honestly).
- Structured JSON logs (`slog`) with a request id per request.
- `/metrics` (RED); `/version` stamped with the SHA.
- External uptime monitoring (UptimeRobot) on `/healthz` and `/readyz`.

### Recover
- **Smoke gate:** after deploy, CI retries `/readyz` until 200 and asserts
  `/version == <deployed SHA>` — a green deploy means the app is *actually*
  serving the new code, not just "container started".
- **One-click rollback:** [`rollback.yml`](.github/workflows/rollback.yml)
  (`workflow_dispatch` + SHA input) redeploys any previously built image.
  Deterministic because images are SHA-tagged.
- **Incident practice:** a game-day drill + blameless postmortem —
  [`docs/postmortems`](docs/postmortems).
- **Runbook:** [`docs/runbook.md`](docs/runbook.md) — deploy, rollback, key
  rotation, logs, box start/stop.

---

## Decision records

Format: *options · choice · why · what would change it.*

- **Managed vs self-hosted DB** — self-host container · **managed (Neon)**.
  Managed removes toil (backup/failover/patching); the edges I still own
  (connections, migrations, restores) are the same either way. *Change if:*
  compliance/scale forces a self-managed cluster.
- **Boot behaviour on DB down** — crash on boot · **stay up, readiness
  gates**. A DB blip shouldn't kill the process; liveness stays green,
  `/readyz` reports 503, traffic is gated. *Change if:* the app genuinely
  can't function without a warm dependency at start.
- **Image tag** — `latest` · **git SHA**. SHA-tagging makes every deploy and
  rollback deterministic and individually addressable. *Change if:* never.
- **Build platforms** — multi-arch · **amd64-only**. The prod box is x86 and
  nothing consumes arm64 today; arm64 via emulation cost ~5 min/push for zero
  benefit (build 6.5 min → 56 s). *Change if:* local arm64 (kind on Apple
  Silicon) or Graviton in prod — re-add arm64 with a build cache.
- **CI → box access** — SSM · **SSH, port 22 open, key-only auth**. Simplest
  path that works for a single learning box. *Change if:* production —
  replace with SSM Session Manager (no inbound SSH port).

---

## What I'd do next

Honest gaps, roughly in priority:

- **TLS + a domain** (Ingress/reverse proxy + cert-manager or Caddy) — it's
  HTTP on a bare IP today.
- **Stable address** — Elastic IP (the auto IP churns on stop/start).
- **Secrets manager** — secrets currently live in the box `.env` + GitHub
  Actions secrets; move to SSM Parameter Store / a secrets manager.
- **Kubernetes (k3s) + Terraform** — the same loop on a real cluster, with the
  box and networking provisioned as code.
- **GitOps** (Argo/Flux) — deploy becomes a git commit, rollback a git revert.
- **HA** — this is single-node by design; no redundancy yet.
