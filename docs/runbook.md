# Homestead Runbook

Operational runbook for the homestead URL shortener. Single-node, no HA.

## Facts

- **Box:** EC2 `t3.micro` (`ap-south-1`), **provisioned by Terraform** (`infra/main.tf`).
  Instance id + public IP live in Terraform state (local, gitignored):
  ```bash
  cd infra
  terraform output -raw public_ip
  terraform state show aws_instance.homestead | grep '^ *id'
  ```
  No Elastic IP → **public IP changes on stop/start** (and on rebuild).
- **Domain + TLS:** `https://homestead.undercoverdevs.com` — Caddy terminates TLS
  (Let's Encrypt, auto-issued + renewed) and reverse-proxies to `app:8080`. Caddy is part
  of `docker-compose.prod.yml`; its config is the 3-line `Caddyfile`. Certs live in the
  `caddy_data` Docker volume (survives `up -d`; losing the volume = re-issue, rate-limit risk).
- **DNS:** `homestead.undercoverdevs.com` A record → box public IP (managed wherever
  `undercoverdevs.com` DNS lives). **Must be updated whenever the IP changes.**
- **DB:** Neon (managed Postgres). Connection string lives in the GitHub Actions secret
  `DATABASE_URL`. The box `~/.env` is **shipped by CI on every deploy** (chmod 600) —
  do not hand-edit it, the next deploy overwrites it.
- **Image:** `ghcr.io/veerbal1/homestead:<git-sha>` (public, SHA-tagged, never `latest`).
- **CI/CD:** `.github/workflows/deploy.yml` (test → build → deploy) and
  `.github/workflows/rollback.yml` (one-click rollback). Both share concurrency group `prod`.
- **GitHub secrets:** `SSH_PRIVATE_KEY`, `BOX_HOST`, `BOX_USER`, `DATABASE_URL`, `API_KEY`.
  (`BOX_HOST` = box public IP → must be updated when the IP changes.)
- **Uptime:** UptimeRobot on the **domain** (not the IP): `/healthz` and `/readyz` (5-min).
- **SSH:** `ssh -i ~/.ssh/homestead.pem ec2-user@<box-ip>`. Port 22 is open to the world
  (accepted debt for a learning box; key-only auth). Planned hardening: SSM Session Manager.

## Deploy (automatic — merge = deploy)

Push/merge to `main` triggers CI: **test → build → deploy**. The deploy job:
1. Runs migrations on Neon (`goose up`) — in CI, before rollout (idempotent).
2. Ships `docker-compose.prod.yml`, `Caddyfile`, and a fresh `~/.env` (built from GitHub
   secrets) to the box via scp.
3. Pulls the SHA image and restarts:
   `TAG=<sha> docker compose -f docker-compose.prod.yml pull && up -d`.
4. Smoke gate over HTTPS: retries `/readyz` until 200, then asserts `/version == <sha>`.
   The job goes red if the app isn't actually serving the new SHA.

Nobody SSHes to release. There is no manual deploy step.

## Roll back

Rollback = redeploy a previous SHA (images are SHA-tagged, so it is deterministic).
Preferred: the one-click rollback workflow (code only, no migrations).

1. Pick the target SHA (a previous good `/version` value).
2. Run:
   ```bash
   gh workflow run rollback.yml -f sha=<sha>
   ```
   (or: Actions → Rollback → Run workflow → enter SHA)
3. It redeploys that image and smoke-asserts `/version == <sha>` over HTTPS.

Fallback — re-run an old deploy job (also re-runs migrations; safe, idempotent):
```bash
gh run list
gh run rerun --job <deploy-job-id>
```

**Important:** rollback rolls back **code only, not the database.** Migrations are
additive / expand-contract, so old code keeps working against the current schema.
Do **not** rely on down-migrations to undo a schema change.

## Rotate secrets

Key rule: the box `~/.env` is a **build artifact of GitHub secrets** — rotate the secret,
then redeploy to ship it. Manual edits on the box are overwritten by the next deploy.

**API_KEY**
1. `gh secret set API_KEY --body '<new-key>'`
2. Redeploy: re-run the latest deploy job (`gh run rerun --job <deploy-id>`), or push any
   commit to `main`.
3. Hand clients the new key. (`API_KEY` is only needed by the app, not migrations.)

**DATABASE_URL**
1. `gh secret set DATABASE_URL --body '<new-url>'`
2. Redeploy as above (migrations + rollout both read the secret).

**SSH deploy key**
1. Generate a new keypair; add the public key to the box `~/.ssh/authorized_keys`.
2. `gh secret set SSH_PRIVATE_KEY < new_key.pem`
3. Verify one deploy run succeeds; then remove the old public key from the box.

## Where are the logs?

On the box:
```bash
docker compose -f docker-compose.prod.yml logs -f app    # structured JSON (slog) + request ids
docker compose -f docker-compose.prod.yml logs -f caddy  # TLS issuance/renewal, proxy errors
```

## TLS / certificates (Caddy)

- Caddy obtains + renews Let's Encrypt certs automatically (HTTP-01 → **port 80 must stay
  open** and DNS must point at the box).
- Cert state lives in the `caddy_data` volume. `docker compose down` (no `-v`) is safe;
  **never** run `down -v` on the box.
- Check renewal: `docker compose logs caddy | grep -i cert`.
- If HTTPS breaks: check DNS A record → box IP, SG ports 80/443, then caddy logs.

## Box lifecycle (Terraform + cost)

Terraform state is **local and gitignored** (`infra/terraform.tfstate`) — keep it. Losing
it means Terraform no longer tracks the existing box.

**Stop / start when idle** (compute charge stops; public IPv4 ~$3.6/mo also stops).
Instance id: `terraform state show aws_instance.homestead | grep '^ *id'`.
```bash
aws ec2 stop-instances  --profile homestead --region ap-south-1 --instance-ids <id>
aws ec2 start-instances --profile homestead --region ap-south-1 --instance-ids <id>
```
After every start (IP changes):
1. `cd infra && terraform output -raw public_ip` → new IP
2. Update the DNS A record (domain → new IP)
3. `gh secret set BOX_HOST --body "<new-ip>"`
4. UptimeRobot monitors use the domain → no change needed there.

**Rebuild from zero:**
```bash
cd infra
terraform apply                  # SG + EC2 + cloud-init installs Docker + compose plugin
terraform output -raw public_ip
```
Then: update the DNS A record, `gh secret set BOX_HOST`, re-run the deploy job (ships
config + app). The Neon DB is untouched by a box rebuild.

**Teardown:** `terraform destroy` (box + SG gone; Neon DB stays; rebuild per above).

## Common incidents

- **`/readyz` 503, `/healthz` 200:** app up, DB unreachable. Check Neon status /
  `DATABASE_URL`. The app does not crash on DB loss (readiness gates).
- **HTTPS fails / cert errors:** DNS A record wrong, SG port 80/443 closed, or caddy
  container down (`docker compose ps`, caddy logs). ACME cannot validate if port 80 is closed.
- **Deploy job fails at "Deploy to box" with SSH timeout:** box stopped or IP changed
  (update DNS + `BOX_HOST`), or SG port 22 issue.
- **Smoke gate fails on `version mismatch`:** pull/up didn't bring up the new image
  (check the GHCR tag; `docker compose logs app` on the box).
- **Lock-queue jam under a migration:** see `docs/postmortems/2026-09-14-lock-queue-jam.md`.
