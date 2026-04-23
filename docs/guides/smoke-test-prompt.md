# Smoke Test Prompt — Azure MVP

Paste everything below the line into a fresh Claude Code session after merging PR #16 into main.

---

## Task

Run the Azure MVP smoke runbook end-to-end against a fresh Azure deployment. Follow `docs/guides/smoke-test-mvp-azure.md` step by step. Log every finding in `docs/guides/smoke-test-mvp-azure-findings.md` (append — do not overwrite prior findings). Fix blockers in code or docs as you go; do not add deferred features.

## Rules

- **Never push to main.** Create a branch, open a PR, work there.
- **All edits happen in a git worktree** (per memory — use `git worktree add`).
- Tear down all Azure resources at the end (`az group delete --yes --no-wait`).
- Delete the smoke skill repo when done.

## Azure subscription

- Subscription ID: `d0ecd0d2-779b-4fd0-8f04-d46d07f05703`
- Use region `swedencentral`
- Pick a fresh env name (e.g. `p8smoke`)

## GitHub App

- App ID: `3466423`
- OAuth Client ID: `Iv23liM78E4FbFqH3xBa`
- The operator will provide: OAuth Client Secret, PEM, installation ID, and Anthropic API key when prompted.

## Smoke skill repo

Create `gambtho/cronfoundry-smoke-skill` (or ask the operator to create it) with:

- `cronfoundry.yaml` — model `anthropic/claude-haiku-4-5-20251001`, destination `github-issue`, writeback enabled
- `skills/smoke/SKILL.md` — a trivial skill prompt (e.g. "Say hello")

## What v0.7.5 already fixed (don't re-discover these)

These were found in the prior smoke run (PR #16, findings F16–F23) and are now in main:

| # | Issue | What was fixed |
|---|-------|---------------|
| F16 | Postgres no firewall rules | `AllowAllAzureServicesAndResourcesWithinAzureIps` rule in `postgres.bicep` |
| F17 | Extensions not allow-listed | `azure.extensions = UUID-OSSP,CITEXT` in `postgres.bicep` |
| F18 | KV role too restrictive | Upgraded to Secrets Officer in `keyVault.bicep` |
| F19 | Serve can't start runner | Contributor role on RG via `roleAssignment.bicep` |
| F20 | Null container name | `Name: "runner"` in `armclient_real.go` |
| F21 | Missing Image in template | `ContainerImage` field + `AZURE_CAE_JOB_IMAGE` env var |
| F22 | Wrong runner API URL | `CRONFOUNDRY_API_BASE_URL` env var + Bicep wiring |
| F23 | APIBaseURL dual-purpose | Separated `RunnerAPIURL` from `APIBaseURL` in scheduler |

## Known open issue (F24)

The runner container has no GitHub installation token, so `github-issue` destinations fail with `partial_failure`. The LLM call works — tokens are populated. This is deferred pending a design decision on how the runner obtains a short-lived installation token.

## Pass criteria

- Run reaches `succeeded` or `partial_failure` (with only F24 as the failure reason)
- `input_tokens` and `output_tokens` are populated in the finalize event
- Dashboard shows the run
- Audit log rows exist
- All Azure resources deleted at end

## Key operational notes from prior run

1. `serve` does not auto-migrate — run `admin init` separately after deploy.
2. `llm_secret_ref` is not set by YAML sync — update DB manually: `UPDATE schedule SET llm_secret_ref = 'llm-key' WHERE name = 'every-5'`
3. Azure KV secret names: no underscores, use hyphens (e.g. `llm-key`).
4. Cookie name is `cf_session` (not `session`). Use HMAC-SHA256 with master key.
5. After Bicep deploy, force a new Container App revision: `az containerapp update --set-env-vars "RESTART_TRIGGER=$(date +%s)"`
6. DNS from WSL2 can be flaky — use `--resolve` with `dig +short` IP if curl times out.
7. The release workflow multi-arch Docker build takes ~15-20 minutes.
8. The run-now endpoint is `POST /api/schedules/{id}/run-now` (hyphenated).
9. `params.<env>.json` is gitignored and contains real secrets — don't commit it.
