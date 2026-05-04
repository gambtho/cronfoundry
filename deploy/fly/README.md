# CronFoundry — Fly.io Deployment

## Quick start

The fastest path is `scripts/fly-quickstart.sh` from the repo root.
It prompts for any missing values, generates encryption keys,
provisions apps + Postgres, sets all secrets, deploys both apps,
and opens a starter PR against your skills repo.

```bash
bash scripts/fly-quickstart.sh
```

Use `--non-interactive` to fail (rather than prompt) on missing
config, `--image=REF` to deploy a specific image tag, and
`--fresh` to destroy and re-provision everything (irreversible —
including Postgres data).

The manual steps below remain accurate if you'd rather provision by
hand or need to understand what the script does.

---

## Prerequisites

- `flyctl` installed and authenticated (`flyctl auth login`)
- A Fly.io organization
- An external Postgres database OR `fly postgres create` (see below)

## Steps

### 1. Create apps

```bash
flyctl apps create cronfoundry-api
flyctl apps create cronfoundry-runner
```

### 2. Create Postgres

```bash
fly postgres create --name cronfoundry-db --region iad
fly postgres attach --app cronfoundry-api cronfoundry-db
# This sets DATABASE_URL automatically on cronfoundry-api
```

Or set `DATABASE_URL` manually if using an external DB.

### 3. Generate secrets

```bash
# 32-byte master key for the Postgres secret store
openssl rand -hex 32

# High-entropy runner API key
openssl rand -hex 32
```

### 4. Set secrets

```bash
flyctl secrets set --app cronfoundry-api \
  CRONFOUNDRY_MASTER_KEY=<hex-key-from-step-3> \
  CRONFOUNDRY_RUNNER_API_KEY=<runner-key-from-step-3> \
  CRONFOUNDRY_GITHUB_APP_ID=<app-id> \
  CRONFOUNDRY_GITHUB_APP_PEM="$(cat your-app.private-key.pem)" \
  CRONFOUNDRY_GITHUB_OAUTH_CLIENT_ID=<client-id> \
  CRONFOUNDRY_GITHUB_OAUTH_CLIENT_SECRET=<client-secret> \
  CRONFOUNDRY_GITHUB_WEBHOOK_SECRET=<webhook-secret> \
  CRONFOUNDRY_ADMIN_LOGINS=<your-github-login> \
  FLY_RUNNER_APP=cronfoundry-runner \
  FLY_RUNNER_IMAGE=registry.fly.io/cronfoundry-runner:latest \
  FLY_API_TOKEN=$(flyctl auth token)
```

The same `CRONFOUNDRY_RUNNER_API_KEY` must be set on the runner app:

```bash
flyctl secrets set --app cronfoundry-runner \
  CRONFOUNDRY_RUNNER_API_KEY=<same-runner-key>
```

### 5. Deploy API

```bash
flyctl deploy --config deploy/fly/fly.api.toml --app cronfoundry-api \
  --image ghcr.io/cronfoundry/cronfoundry:latest
```

### 6. Register runner image

```bash
flyctl deploy --config deploy/fly/fly.runner.toml --app cronfoundry-runner \
  --image ghcr.io/cronfoundry/cronfoundry:latest --no-ha
```

### 7. Verify

```bash
flyctl logs --app cronfoundry-api
flyctl open --app cronfoundry-api
```

## Secrets Management

Secrets entered via the CronFoundry UI are stored **encrypted in Postgres** using AES-256-GCM with `CRONFOUNDRY_MASTER_KEY` as the encryption key. Back up this key — loss means loss of all stored secrets.

To rotate the master key:
1. Re-enter all secrets in the UI after setting the new key.
2. Set the new `CRONFOUNDRY_MASTER_KEY` via `flyctl secrets set`.
