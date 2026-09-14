# Homestead Runbook

Operational runbook for the homestead URL shortener. Single-node, no HA.

## Facts

- **Box:** EC2 `i-02fe19776742c475f` (t3.micro, `ap-south-1`). Public IP
  `13.233.159.159` — **changes on stop/start** (no Elastic IP).
- **DB:** Neon (managed Postgres). URL lives in the box `~/.env` (runtime) and
  in the GitHub Actions secret `DATABASE_URL` (used only to run migrations in CI).
- **Image:** `ghcr.io/veerbal1/homestead:<git-sha>` (public, SHA-tagged, never `latest`).
- **CI/CD:** `.github/workflows/deploy.yml` — jobs `test → build → deploy`.
- **GitHub secrets:** `SSH_PRIVATE_KEY`, `BOX_HOST`, `BOX_USER`, `DATABASE_URL`.
- **Uptime:** UptimeRobot monitors `/healthz` and `/readyz` (5-min).

## Deploy (automatic — merge = deploy)

Push/merge to `main` triggers CI: **test → build → deploy**.
The deploy job:
1. Runs migrations on Neon (`goose up`) — in CI, before rollout.
2. SSHes to the box and runs `TAG=<sha> docker compose -f docker-compose.prod.yml pull && up -d`.
3. Smoke gate: retries `/readyz` until 200, then asserts `/version == <sha>`.
   The job fails (red) if the app isn't actually serving the new SHA.

Nobody SSHes to release. There is no manual deploy step.

## Roll back

Rollback = redeploy a previous SHA (images are SHA-tagged, so it is deterministic).

1. Find the previous good run and its deploy job id:
   ```bash
   gh run list
   gh run view <run-id> --json jobs -q '.jobs[] | select(.name=="deploy") | .databaseId'
   ```
2. Re-run only that deploy job:
   ```bash
   gh run rerun --job <deploy-job-id>
   ```
3. Verify: `curl -s http://13.233.159.159/version` returns the target SHA.

**Important:** rollback rolls back **code only, not the database.** Migrations
are additive / expand-contract, so old code keeps working against the current
schema. Do **not** rely on down-migrations to undo a schema change.

## Rotate the API key

1. SSH to the box: `ssh -i ~/.ssh/homestead.pem ec2-user@<box-ip>`
2. Edit `~/.env`, set the new `API_KEY=...`, keep `chmod 600 ~/.env`.
3. Recreate the container so it picks up the new value:
   ```bash
   TAG=<current-sha> docker compose -f docker-compose.prod.yml up -d
   ```
4. Give clients the new key. `API_KEY` lives only in the box `.env` (not in GitHub).

## Where are the logs?

On the box:
```bash
docker compose -f docker-compose.prod.yml logs -f app
```
Structured JSON logs (slog), each line carries a request id (`X-Request-Id`).

## Box start / stop (cost control)

Stop when idle (compute charge stops; ~$3.6/mo public IPv4 also stops):
```bash
aws ec2 stop-instances --profile homestead --region ap-south-1 \
  --instance-ids i-02fe19776742c475f
```
Start again, then fetch the **new** IP and update dependents:
```bash
aws ec2 start-instances --profile homestead --region ap-south-1 \
  --instance-ids i-02fe19776742c475f
aws ec2 wait instance-running --profile homestead --region ap-south-1 \
  --instance-ids i-02fe19776742c475f
NEWIP=$(aws ec2 describe-instances --profile homestead --region ap-south-1 \
  --instance-ids i-02fe19776742c475f \
  --query 'Reservations[0].Instances[0].PublicIpAddress' --output text)
echo "$NEWIP"
gh secret set BOX_HOST --body "$NEWIP"   # so CI deploys to the new IP
# also update the UptimeRobot monitors to the new IP
```

## Common incidents

- **`/readyz` 503, `/healthz` 200:** app up, DB unreachable. Check Neon status /
  connection string in box `~/.env`. App does not crash on DB loss (readiness gates).
- **Deploy job fails at "Deploy to box" with SSH timeout:** box stopped, IP
  changed (update `BOX_HOST`), or security group closed port 22.
- **Smoke gate fails on `version mismatch`:** the pull/up didn't bring up the new
  image (check GHCR has the tag; check box `docker compose logs`).
- **Lock-queue jam under a migration:** see `docs/postmortems/2026-09-14-lock-queue-jam.md`.
