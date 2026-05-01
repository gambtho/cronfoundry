# Pre-release Polish — Phase 1: install.sh Friction Elimination

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `bash install.sh` produces a green Copilot Enterprise run on a fresh Azure subscription in <15 minutes, asking the operator only for: subscription, region, env suffix, skill repo, reports repo, and one OAuth device-flow user-code typed into a browser.

**Architecture:** Reuse the existing `internal/githubapp` manifest-flow package (PR #45) and existing admin CLI commands (`connect-repo`, `set-secret`). Reorder so Bicep deploys *before* GitHub App registration — this lets the manifest flow register the App with the real FQDN as its callback/webhook URL, eliminating the post-deploy URL-update step. Add a new `cronfoundry admin connect-copilot` CLI to script the Copilot device-flow. Add a starter-skill push step that uses `gh api`. Make the script's state machine resumable and idempotent with per-step verifiers.

**Tech Stack:** Bash 4+, Go 1.25, cobra CLI, `gh` CLI for GitHub API calls, Azure CLI 2.60+, the existing `internal/llm/copilot` package for device-flow.

**Spec:** `docs/superpowers/specs/2026-04-30-prerelease-polish-design.md` §Phase 1.

---

## File Structure

**Modified:**
- `scripts/quickstart-copilot.sh` — restructured into a state machine; Bicep moves before App registration; adds new steps for `connect-copilot`, `connect-repo`, `set-secret`, starter-skill push, run-tail.
- `scripts/lib/state.sh` (new) — extracted state-file load/save helpers with per-env suffix support.
- `scripts/lib/steps.sh` (new) — extracted step framework: `step_run "name" "verifier_cmd" "do_cmd"` so each step has explicit verifier + body. Idempotency comes from the verifier.
- `cmd/cronfoundry/admin_connectcopilot.go` (new) — `cronfoundry admin connect-copilot --prefix <name>` CLI command running the device-flow against the deployed instance.
- `cmd/cronfoundry/admin_connectcopilot_test.go` (new) — unit tests.
- `cmd/cronfoundry/admin.go` — register the new command.

**Reused as-is:**
- `internal/githubapp/*` — manifest flow, server, state.
- `cmd/cronfoundry/setup_githubapp.go` — `cronfoundry setup github-app` already exists.
- `cmd/cronfoundry/admin_connectrepo.go`, `admin_setsecret.go` — already exist.
- `internal/llm/copilot.go` — device-flow code already there for the UI; we wrap it.

**New `install.sh` step order** (vs current 17 steps):

| # | Step | Today | After |
|---|------|-------|-------|
| 1 | Prereqs (with `gh` added) | OK | + check `gh auth status` |
| 2 | `az login` | OK | unchanged |
| 3 | Subscription | OK | unchanged |
| 4 | Repo root check | OK | unchanged |
| 5 | Env suffix | OK | per-env state file |
| 6 | Region | OK | unchanged |
| 7 | Master key gen | OK | unchanged |
| 8 | Postgres password | OK | unchanged |
| 9 | Image tag | OK | unchanged |
| 10 | Skill repo + reports repo prompt | was §6 + §7 | merged, validated via `gh api` |
| 11 | **Bicep deploy** | was §14 | moved here; captures FQDN to state |
| 12 | **`admin init`** + revision restart | was §15 | unchanged; auto-tightens firewall after |
| 13 | **GitHub App manifest flow** | was §5 (manual paste) | invokes `cronfoundry setup github-app --base-api … --state-file …`; passes real FQDN URLs |
| 14 | **Auto-discover install ID** | was §6 (paste) | `gh api /app/installations` using App JWT, pick install on the operator's repo |
| 15 | **Connect repo via admin CLI** | UI click | `cronfoundry admin connect-repo` |
| 16 | **Set webhook secret** | UI click | `cronfoundry admin set-secret github_webhook_secret` |
| 17 | **Copilot device-flow** | UI click | `cronfoundry admin connect-copilot --prefix copilot` |
| 18 | **Push starter skill** | manual yaml | `gh api` push to `cronfoundry-quickstart` branch on skill repo, then merge or PR |
| 19 | **Wait for first green run** | manual UI watch | poll `/api/runs?limit=1` until `succeeded`; print URL |

---

## Task 1: Extract state-file helpers into `scripts/lib/state.sh`

**Files:**
- Create: `scripts/lib/state.sh`
- Test: `scripts/lib/state_test.bats`
- Modify: `scripts/quickstart-copilot.sh:18-26` (swap inline state code for `source scripts/lib/state.sh`)

The current script's state-file logic is duplicated between top-of-file load and per-call `save`. Extract into a sourced library; add per-env suffix so concurrent envs don't collide.

- [ ] **Step 1: Write failing bats tests for state load/save round-trip**

```bash
# scripts/lib/state_test.bats
#!/usr/bin/env bats

setup() {
  STATE_DIR=$(mktemp -d)
  export STATE_FILE="${STATE_DIR}/state-test"
  source "${BATS_TEST_DIRNAME}/state.sh"
}

teardown() {
  rm -rf "${STATE_DIR}"
}

@test "state_save and reload round-trips a value" {
  state_init
  state_save "CF_FOO" "bar"
  unset CF_FOO
  state_load
  [ "$CF_FOO" = "bar" ]
}

@test "state_save quotes special characters" {
  state_init
  state_save "CF_PASSWORD" 'hunter2$#!'
  unset CF_PASSWORD
  state_load
  [ "$CF_PASSWORD" = 'hunter2$#!' ]
}

@test "state_init creates file with mode 600" {
  state_init
  perms=$(stat -c '%a' "$STATE_FILE" 2>/dev/null || stat -f '%Lp' "$STATE_FILE")
  [ "$perms" = "600" ]
}

@test "state_clear removes the file" {
  state_init
  state_save "CF_FOO" "bar"
  state_clear
  [ ! -f "$STATE_FILE" ]
}

@test "state_path_for env returns per-env file" {
  unset STATE_FILE
  result=$(state_path_for "copilot1")
  [[ "$result" == *".cronfoundry-quickstart-state-copilot1" ]]
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `bats scripts/lib/state_test.bats`
Expected: 5 failures, "state.sh: No such file or directory" or "state_init: command not found".

- [ ] **Step 3: Implement state.sh**

```bash
# scripts/lib/state.sh
# State-file helpers for the cronfoundry quickstart script.
#
# STATE_FILE may be set by the caller; otherwise default to
# ~/.cronfoundry-quickstart-state. Supports per-env suffix via
# state_path_for().

state_path_for() {
  local env="$1"
  echo "${HOME}/.cronfoundry-quickstart-state-${env}"
}

state_init() {
  : "${STATE_FILE:=${HOME}/.cronfoundry-quickstart-state}"
  if [[ ! -f "$STATE_FILE" ]]; then
    touch "$STATE_FILE"
  fi
  chmod 600 "$STATE_FILE"
}

state_load() {
  : "${STATE_FILE:=${HOME}/.cronfoundry-quickstart-state}"
  if [[ -f "$STATE_FILE" ]]; then
    # shellcheck disable=SC1090
    source "$STATE_FILE"
  fi
}

state_save() {
  local key="$1" val="$2"
  state_init
  # Remove any existing line with this key, then append.
  if grep -q "^${key}=" "$STATE_FILE" 2>/dev/null; then
    grep -v "^${key}=" "$STATE_FILE" > "${STATE_FILE}.tmp" && mv "${STATE_FILE}.tmp" "$STATE_FILE"
  fi
  printf '%s=%q\n' "$key" "$val" >> "$STATE_FILE"
  chmod 600 "$STATE_FILE"
  # Also export for in-process reads.
  export "${key}=${val}"
}

state_clear() {
  : "${STATE_FILE:=${HOME}/.cronfoundry-quickstart-state}"
  rm -f "$STATE_FILE"
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `bats scripts/lib/state_test.bats`
Expected: 5 passes.

- [ ] **Step 5: Replace inline state code in quickstart-copilot.sh**

Replace lines 18-26 of `scripts/quickstart-copilot.sh` with:

```bash
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/state.sh
source "${SCRIPT_DIR}/lib/state.sh"
state_init
state_load
save() { state_save "$1" "$2"; }   # legacy alias used elsewhere in script
```

- [ ] **Step 6: Smoke-run script with `--dry-run` to verify no regression**

Run: `bash scripts/quickstart-copilot.sh --dry-run` (currently exits early; verify it loads state without error).
Expected: existing behavior preserved, no new errors.

- [ ] **Step 7: Commit**

```bash
git add scripts/lib/state.sh scripts/lib/state_test.bats scripts/quickstart-copilot.sh
git commit -m "refactor(quickstart): extract state helpers into scripts/lib/state.sh"
```

---

## Task 2: Add the step-framework helper `scripts/lib/steps.sh`

**Files:**
- Create: `scripts/lib/steps.sh`
- Test: `scripts/lib/steps_test.bats`
- Modify: `scripts/quickstart-copilot.sh` — replace ad-hoc `header "[step N/17] …"` calls with `step_run`.

Goal: each step is `step_run NAME VERIFIER BODY`. The verifier is run first; if it succeeds, the body is skipped (idempotency). If verifier fails, body runs; verifier runs again; if still failing, the script aborts with a precise diagnostic.

- [ ] **Step 1: Write failing tests**

```bash
# scripts/lib/steps_test.bats
#!/usr/bin/env bats

setup() {
  source "${BATS_TEST_DIRNAME}/steps.sh"
  WORK=$(mktemp -d)
}

teardown() {
  rm -rf "$WORK"
}

@test "step_run skips body when verifier passes" {
  ran=0
  step_run "noop" "true" "ran=1"
  [ "$ran" = "0" ]
}

@test "step_run runs body when verifier fails, then re-verifies" {
  step_run "create marker" "test -f $WORK/marker" "touch $WORK/marker"
  [ -f "$WORK/marker" ]
}

@test "step_run aborts when verifier still fails after body" {
  run step_run "broken" "false" "true"
  [ "$status" -ne 0 ]
  [[ "$output" == *"verifier failed after body"* ]]
}

@test "step_run prints expected/got on failure" {
  run step_run "broken" "false" "echo doing stuff; false"
  [[ "$output" == *"step: broken"* ]]
}
```

- [ ] **Step 2: Verify tests fail**

Run: `bats scripts/lib/steps_test.bats`
Expected: 4 failures.

- [ ] **Step 3: Implement steps.sh**

```bash
# scripts/lib/steps.sh
# Step framework with idempotent verifier + body.
#
# Usage: step_run NAME VERIFIER_CMD BODY_CMD
#
# Verifier is evaluated as a shell expression. If it returns 0, the step is
# considered already done and BODY is skipped. Otherwise BODY runs, then
# VERIFIER runs again; if it still fails, abort.

: "${RED:=$'\033[0;31m'}"
: "${GREEN:=$'\033[0;32m'}"
: "${YELLOW:=$'\033[1;33m'}"
: "${CYAN:=$'\033[0;36m'}"
: "${BOLD:=$'\033[1m'}"
: "${RESET:=$'\033[0m'}"

step_run() {
  local name="$1" verifier="$2" body="$3"

  echo -e "\n${BOLD}▶ ${name}${RESET}"

  if eval "$verifier" &>/dev/null; then
    echo -e "  ${GREEN}✓ already done${RESET}"
    return 0
  fi

  echo -e "  ${CYAN}→ running…${RESET}"
  if ! eval "$body"; then
    echo -e "${RED}step: ${name}${RESET}" >&2
    echo -e "${RED}body returned non-zero${RESET}" >&2
    echo -e "${YELLOW}resume with: bash $(basename "$0")${RESET}" >&2
    return 1
  fi

  if ! eval "$verifier" &>/dev/null; then
    echo -e "${RED}step: ${name}${RESET}" >&2
    echo -e "${RED}verifier failed after body. expected: ${verifier}${RESET}" >&2
    echo -e "${YELLOW}resume with: bash $(basename "$0")${RESET}" >&2
    return 1
  fi

  echo -e "  ${GREEN}✓ done${RESET}"
}
```

- [ ] **Step 4: Tests pass**

Run: `bats scripts/lib/steps_test.bats`
Expected: 4 passes.

- [ ] **Step 5: Source steps.sh in quickstart-copilot.sh**

Add after the state.sh source line:

```bash
# shellcheck source=lib/steps.sh
source "${SCRIPT_DIR}/lib/steps.sh"
```

(Don't yet rewrite existing steps to use `step_run`; that happens incrementally in later tasks.)

- [ ] **Step 6: Commit**

```bash
git add scripts/lib/steps.sh scripts/lib/steps_test.bats scripts/quickstart-copilot.sh
git commit -m "feat(quickstart): add step framework with idempotent verifiers"
```

---

## Task 3: Add `gh` CLI to prereq check + add `gh auth status` verification

**Files:**
- Modify: `scripts/quickstart-copilot.sh` (the prereq check block, ~line 35-50).

`gh` is required for: install-ID auto-discovery, starter-skill push.

- [ ] **Step 1: Add gh check after the existing `check_cmd` calls**

After `check_cmd go "Install: …"`, add:

```bash
check_cmd gh "Install: https://cli.github.com/"
if ! gh auth status &>/dev/null; then
  die "gh is installed but not authenticated. Run: gh auth login\nThen re-run this script."
fi
ok "gh authenticated as $(gh api /user --jq .login)"
```

- [ ] **Step 2: Manual verification**

Run: `bash scripts/quickstart-copilot.sh` (let it reach prereq, then Ctrl-C).
Expected: prereq block prints `gh authenticated as <yourlogin>` if `gh auth login` was run, otherwise dies with the install hint.

- [ ] **Step 3: Commit**

```bash
git add scripts/quickstart-copilot.sh
git commit -m "feat(quickstart): require gh CLI in prereq check"
```

---

## Task 4: Reorder the script — Bicep deploy moves before GitHub App registration

This is the biggest single change in Phase 1. After this task, the linear flow is:

```
prereqs → az login → subscription → env → region → keys → repos prompt
  → Bicep deploy (FQDN captured) → admin init → manifest flow w/ real URLs
  → connect-repo → set webhook secret → copilot device-flow → push skill
  → wait for first run
```

**Files:**
- Modify: `scripts/quickstart-copilot.sh` (block reorder, plus a placeholder URL fix in the params builder).
- Modify: `deploy/main.bicep` — confirm `githubApp*` parameters are optional (the App doesn't yet exist when Bicep runs).

- [ ] **Step 1: Audit Bicep parameters that currently require App values**

Run:

```bash
grep -n -E "githubApp|appId|clientId|clientSecret|pem" deploy/main.bicep deploy/modules/*.bicep
```

Identify which parameters are needed at deploy time vs only at runtime. Anything runtime-only should be set later via `containerapp update --set-env-vars` after the manifest flow.

Expected output: list of param uses; goal is to reduce the at-deploy set to: master key, Postgres password, image tag, env suffix, region. Everything GitHub-App-related becomes a post-deploy `containerapp update`.

- [ ] **Step 2: If Bicep currently requires App params, write tests for the post-deploy update path**

This is a Bicep-layer change. If `deploy/main.bicep` requires `githubAppId` etc., add new defaults (e.g. `param githubAppId string = ''`) and let the serve container start without them — the serve binary should already tolerate missing GitHub config (operator can later set it via UI), but verify by reading `internal/bootstrap/*.go`:

```bash
grep -n "GITHUB_APP_ID\|GithubApp" internal/bootstrap/*.go
```

If serve fails when these are unset, file a bug task — but the design assumes serve already supports the "no GitHub App configured yet" state because PR #45 explicitly added the in-UI manifest flow.

- [ ] **Step 3: In quickstart-copilot.sh, move the Bicep-deploy step block to before App registration**

The Bicep deploy block (current §14, ~lines 270-310) moves up. The §13 params-build block also moves up, but with **App params written as empty strings** since they don't yet exist:

```bash
# §11 (was §13): build params file with App fields blank
header "[step 11/19] Build Bicep parameters file"
PARAMS_FILE="deploy/params.quickstart-${CF_ENV}.json"
python3 <<PYEOF
import json
params = {
  "\$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
  "contentVersion": "1.0.0.0",
  "parameters": {
    "envSuffix":         {"value": "${CF_ENV}"},
    "location":          {"value": "${CF_REGION}"},
    "imageTag":          {"value": "${CF_IMAGE_TAG}"},
    "masterKey":         {"value": "${CF_MASTER_KEY}"},
    "postgresPassword":  {"value": "${CF_PG_PASSWORD}"},
    "githubAppId":       {"value": ""},
    "githubAppClientId": {"value": ""},
    "githubAppClientSecret": {"value": ""},
    "githubAppPem":      {"value": ""},
    "githubWebhookSecret": {"value": ""},
  }
}
with open("${PARAMS_FILE}", "w") as f: json.dump(params, f, indent=2)
PYEOF
ok "Wrote ${PARAMS_FILE}"
```

- [ ] **Step 4: Bicep deploy and FQDN capture**

```bash
# §12 (was §14): Bicep deploy
header "[step 12/19] Bicep deploy (~10 min)"
az deployment sub create \
  --location "${CF_REGION}" \
  --template-file deploy/main.bicep \
  --parameters "@${PARAMS_FILE}" \
  || die "Bicep deploy failed. Inspect with: az deployment sub list -o table"

# Capture FQDN
CF_FQDN=$(az containerapp show \
  --resource-group "rg-cronfoundry-${CF_ENV}" \
  --name "cf-serve-${CF_ENV}" \
  --query properties.configuration.ingress.fqdn -o tsv)
[[ -z "$CF_FQDN" ]] && die "Could not get FQDN from deployed serve container"
save CF_FQDN "$CF_FQDN"
ok "Deployed at https://${CF_FQDN}"
```

- [ ] **Step 5: Move admin-init block to right after deploy**

The current §15 admin-init block stays where it is conceptually but renumber to §13. Existing code is correct — it migrates the DB. Add the firewall-tighten step at the end:

```bash
# §13 firewall tighten
OPERATOR_IP=$(curl -fsS https://api.ipify.org)
az postgres flexible-server firewall-rule create \
  --resource-group "rg-cronfoundry-${CF_ENV}" \
  --name "cf-pg-${CF_ENV}" \
  --rule-name "AllowOperator" \
  --start-ip-address "$OPERATOR_IP" --end-ip-address "$OPERATOR_IP" \
  --output none
# Remove the broad rule from §13a (the broad rule should have been added before admin init)
az postgres flexible-server firewall-rule delete \
  --resource-group "rg-cronfoundry-${CF_ENV}" \
  --name "cf-pg-${CF_ENV}" \
  --rule-name "AllowAll" \
  --yes --output none 2>/dev/null || true
ok "Firewall tightened to operator IP ${OPERATOR_IP}"
```

- [ ] **Step 6: Manual smoke (without running the App-registration parts yet)**

Run on a throwaway Azure RG: `bash scripts/quickstart-copilot.sh` and confirm Bicep deploys and serve container is reachable at `https://<fqdn>`. Stop the script there.

Cleanup: `az group delete --name rg-cronfoundry-<env> --yes --no-wait`.

- [ ] **Step 7: Commit**

```bash
git add scripts/quickstart-copilot.sh deploy/main.bicep
git commit -m "feat(quickstart): deploy Bicep before GitHub App registration"
```

---

## Task 5: Replace manual GitHub App registration with the existing manifest flow

**Files:**
- Modify: `scripts/quickstart-copilot.sh` — replace §5's `read -p` block with an invocation of `cronfoundry setup github-app` passing the FQDN as the callback/webhook URL.
- Read for reference: `internal/githubapp/manifest.go` (`ManifestInput`).

The `cronfoundry setup github-app` command exists. We just call it. It writes the App ID, client ID/secret, PEM path, and webhook secret to the state file. After it returns, those values are loaded.

- [ ] **Step 1: Read the ManifestInput struct so we know the field names**

Run: `grep -n "type ManifestInput\|HomepageURL\|CallbackURL\|WebhookURL" internal/githubapp/manifest.go`

Confirm the struct has fields for: Name, HomepageURL, CallbackURL, WebhookURL. If the existing CLI command doesn't yet accept these via flags, that's a sub-task (Task 5b).

- [ ] **Step 2: Confirm the CLI exposes URL flags**

Run: `cronfoundry setup github-app --help`
Expected: flags include `--homepage-url`, `--callback-url`, `--webhook-url`. If missing, add them — see Task 5b.

- [ ] **Step 2a (only if 5b needed): Add URL flags to setup_githubapp.go**

Add to `cmd/cronfoundry/setup_githubapp.go`:

```go
var homepageURL, callbackURL, webhookURL string

cmd.Flags().StringVar(&homepageURL, "homepage-url", "", "Homepage URL for the GitHub App")
cmd.Flags().StringVar(&callbackURL, "callback-url", "", "OAuth callback URL")
cmd.Flags().StringVar(&webhookURL, "webhook-url", "", "Webhook URL")
```

Pass them through:

```go
ManifestInput: githubapp.ManifestInput{
    Name:        defaultName,
    HomepageURL: homepageURL,
    CallbackURL: callbackURL,
    WebhookURL:  webhookURL,
},
```

Add corresponding fields to `ManifestInput` in `internal/githubapp/manifest.go` if missing, plus tests in `manifest_test.go`. Tests must verify the flags reach the rendered manifest JSON.

- [ ] **Step 3: Replace §5 in quickstart-copilot.sh**

Old §5 (lines ~120-145, the four `read -p` calls for App ID / Client ID / Client Secret / PEM path) → new step:

```bash
# §14 (was §5): GitHub App manifest flow
header "[step 14/19] Register GitHub App (manifest flow)"
if [[ -z "${CF_GITHUB_APP_ID:-}" ]]; then
  ./cronfoundry setup github-app \
    --state-file "$STATE_FILE" \
    --pem-dir "${HOME}/.cronfoundry" \
    --homepage-url "https://${CF_FQDN}" \
    --callback-url "https://${CF_FQDN}/oauth/callback" \
    --webhook-url "https://${CF_FQDN}/webhook/github" \
    || die "GitHub App manifest flow failed. Re-run with --manual to use the legacy paste prompts."
  state_load   # pick up CF_GITHUB_APP_ID, CF_GITHUB_CLIENT_ID, etc. that the command wrote
fi
ok "GitHub App registered: App ID ${CF_GITHUB_APP_ID}"
```

- [ ] **Step 4: Verify that `setup github-app` actually writes the expected state-file keys**

Run: `grep -n "CF_GITHUB_APP_ID\|CF_GITHUB_CLIENT_ID\|CF_GITHUB_CLIENT_SECRET\|CF_GITHUB_PEM_PATH\|CF_GITHUB_WEBHOOK_SECRET" internal/githubapp/state.go`

Each must be written by the manifest flow's state writer. If a key name differs (e.g. `CRONFOUNDRY_GITHUB_*`), align: pick one canonical naming and make both the script and Go writer agree. **The script's existing names are authoritative.**

- [ ] **Step 5: Smoke test**

Run on a throwaway RG, after Bicep deploys: confirm the manifest flow opens the browser, the operator authorizes, the script continues with App ID populated.

- [ ] **Step 6: Commit**

```bash
git add scripts/quickstart-copilot.sh cmd/cronfoundry/setup_githubapp.go internal/githubapp/manifest.go internal/githubapp/manifest_test.go
git commit -m "feat(quickstart): use manifest flow for GitHub App registration"
```

---

## Task 6: After App registration, push the App params into the deployed Container App

The serve container deployed in Task 4 has empty App env vars. After the manifest flow returns, set them via `containerapp update`.

**Files:**
- Modify: `scripts/quickstart-copilot.sh` (new step right after §14).

- [ ] **Step 1: Add the env-var-update step**

```bash
# §15: push App credentials into the serve container
header "[step 15/19] Push GitHub App credentials to deployed container"
PEM_CONTENTS=$(cat "${CF_GITHUB_PEM_PATH}" | base64 -w 0)
az containerapp update \
  --resource-group "rg-cronfoundry-${CF_ENV}" \
  --name "cf-serve-${CF_ENV}" \
  --set-env-vars \
    "CRONFOUNDRY_GITHUB_APP_ID=${CF_GITHUB_APP_ID}" \
    "CRONFOUNDRY_GITHUB_CLIENT_ID=${CF_GITHUB_CLIENT_ID}" \
    "CRONFOUNDRY_GITHUB_CLIENT_SECRET=${CF_GITHUB_CLIENT_SECRET}" \
    "CRONFOUNDRY_GITHUB_APP_PEM_B64=${PEM_CONTENTS}" \
    "CRONFOUNDRY_GITHUB_WEBHOOK_SECRET=${CF_GITHUB_WEBHOOK_SECRET}" \
  --output none \
  || die "Failed to update Container App with App credentials"
ok "Container App updated; new revision rolling out"

# Wait for revision to become ready
for i in {1..30}; do
  STATUS=$(curl -s -o /dev/null -w "%{http_code}" "https://${CF_FQDN}/healthz" || echo "000")
  if [[ "$STATUS" == "200" ]]; then ok "Serve healthy at https://${CF_FQDN}"; break; fi
  sleep 5
done
```

- [ ] **Step 2: Verify env-var names match what serve reads**

Run: `grep -n "CRONFOUNDRY_GITHUB_" internal/bootstrap/*.go cmd/cronfoundry/serve.go`

Confirm names match. If serve expects `CRONFOUNDRY_GITHUB_APP_PEM` (raw, not B64), drop the `_B64` suffix and the base64 encoding.

- [ ] **Step 3: Confirm `/healthz` endpoint exists**

Run: `grep -rn "healthz" internal/api internal/webapi cmd/cronfoundry`

If the endpoint doesn't exist, add a trivial one (separate small task) that returns 200 once DB is reachable. For this plan, assume it exists or use `/api/runs?limit=1` with auth-skip as a fallback.

- [ ] **Step 4: Commit**

```bash
git add scripts/quickstart-copilot.sh
git commit -m "feat(quickstart): push GitHub App credentials to Container App after registration"
```

---

## Task 7: Auto-discover the installation ID

**Files:**
- Modify: `scripts/quickstart-copilot.sh` — replace §6's `read -p installation ID`.

After the manifest flow completes, the operator installs the App on a repo (the manifest flow's last step opens the install page). We then list installations via the App JWT and find the one for the operator's skill repo.

- [ ] **Step 1: Add a helper that mints an App JWT from the PEM**

```bash
# In install.sh, near the top (or in scripts/lib/jwt.sh)
mint_app_jwt() {
  local app_id="$1" pem="$2"
  python3 - <<PYEOF
import jwt, time, sys
with open("${pem}") as f: key = f.read()
now = int(time.time())
print(jwt.encode({"iat": now-60, "exp": now+540, "iss": "${app_id}"}, key, algorithm="RS256"))
PYEOF
}
```

Note: requires `pyjwt` Python package. Add to prereq check OR shell out to a Go helper using the `internal/githubapp` package — preferred:

```bash
JWT=$(./cronfoundry setup mint-jwt --app-id "$CF_GITHUB_APP_ID" --pem "$CF_GITHUB_PEM_PATH")
```

If `mint-jwt` doesn't exist, add it as a sub-command (small Go task using `internal/githubapp`).

- [ ] **Step 2: Add `cronfoundry setup mint-jwt` Go command**

Create `cmd/cronfoundry/setup_mintjwt.go`:

```go
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/gambtho/cronfoundry/internal/githubapp"
)

func newSetupMintJWTCmd() *cobra.Command {
	var appID, pemPath string
	cmd := &cobra.Command{
		Use:   "mint-jwt",
		Short: "Mint a short-lived GitHub App JWT for ad-hoc API calls",
		RunE: func(cmd *cobra.Command, _ []string) error {
			pem, err := os.ReadFile(pemPath)
			if err != nil {
				return fmt.Errorf("read pem: %w", err)
			}
			jwt, err := githubapp.MintAppJWT(appID, pem)
			if err != nil {
				return err
			}
			fmt.Println(jwt)
			return nil
		},
	}
	cmd.Flags().StringVar(&appID, "app-id", "", "GitHub App ID")
	cmd.Flags().StringVar(&pemPath, "pem", "", "Path to App private-key PEM")
	_ = cmd.MarkFlagRequired("app-id")
	_ = cmd.MarkFlagRequired("pem")
	return cmd
}
```

If `githubapp.MintAppJWT` doesn't exist, extract it from the manifest-flow code (it must already exist somewhere — search `grep -rn "RS256" internal/githubapp internal/github`).

Test:

```go
// cmd/cronfoundry/setup_mintjwt_test.go
package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMintJWTCmd_PrintsToken(t *testing.T) {
	tmp := t.TempDir()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil { t.Fatal(err) }
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	pemPath := filepath.Join(tmp, "app.pem")
	if err := os.WriteFile(pemPath, pemBytes, 0600); err != nil { t.Fatal(err) }

	cmd := newSetupMintJWTCmd()
	cmd.SetArgs([]string{"--app-id", "12345", "--pem", pemPath})
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := cmd.Execute(); err != nil { t.Fatal(err) }

	tok := strings.TrimSpace(out.String())
	if strings.Count(tok, ".") != 2 {
		t.Errorf("expected JWT with 3 segments, got %q", tok)
	}
}
```

Register the command in `cmd/cronfoundry/setup.go` (or wherever `setup` parent is).

Run: `go test ./cmd/cronfoundry/ -run TestMintJWTCmd -count=1`
Expected: PASS.

- [ ] **Step 3: Add the install-ID auto-discover step in install.sh**

```bash
# §16: discover installation ID
header "[step 16/19] Discover GitHub App installation"
JWT=$(./cronfoundry setup mint-jwt --app-id "$CF_GITHUB_APP_ID" --pem "$CF_GITHUB_PEM_PATH")
INSTALL_JSON=$(curl -fsS \
  -H "Accept: application/vnd.github+json" \
  -H "Authorization: Bearer ${JWT}" \
  https://api.github.com/app/installations)

INSTALL_COUNT=$(echo "$INSTALL_JSON" | jq 'length')
if [[ "$INSTALL_COUNT" -eq 0 ]]; then
  warn "No installations found yet."
  echo "Open this URL to install the App on your skill repo:"
  echo "  https://github.com/apps/$(echo "$JWT" | jwt-decode-or-skip)/installations/new"
  read -rp "Press Enter once installed..."
  INSTALL_JSON=$(curl -fsS \
    -H "Accept: application/vnd.github+json" \
    -H "Authorization: Bearer ${JWT}" \
    https://api.github.com/app/installations)
  INSTALL_COUNT=$(echo "$INSTALL_JSON" | jq 'length')
fi
if [[ "$INSTALL_COUNT" -eq 1 ]]; then
  CF_INSTALLATION_ID=$(echo "$INSTALL_JSON" | jq '.[0].id')
elif [[ "$INSTALL_COUNT" -gt 1 ]]; then
  echo "Multiple installations found:"
  echo "$INSTALL_JSON" | jq '.[] | {id, account: .account.login}'
  read -rp "Enter installation ID for ${CF_SKILL_REPO}: " CF_INSTALLATION_ID
fi
save CF_INSTALLATION_ID "$CF_INSTALLATION_ID"
ok "Installation ID: ${CF_INSTALLATION_ID}"
```

(Replace `jwt-decode-or-skip` with a literal: the App slug is in the manifest-flow state. If `CF_GITHUB_APP_SLUG` is saved, use it; else just print the install URL from the manifest-flow output that the user should already have seen.)

- [ ] **Step 4: Commit**

```bash
git add scripts/quickstart-copilot.sh cmd/cronfoundry/setup_mintjwt.go cmd/cronfoundry/setup_mintjwt_test.go
git commit -m "feat(quickstart): auto-discover GitHub App installation ID"
```

---

## Task 8: Add `cronfoundry admin connect-copilot` CLI

**Files:**
- Create: `cmd/cronfoundry/admin_connectcopilot.go`
- Create: `cmd/cronfoundry/admin_connectcopilot_test.go`
- Modify: `cmd/cronfoundry/admin.go` — register command.
- Modify: `internal/llm/copilot.go` — extract device-flow into a callable function if not already.

This wraps the existing Copilot Enterprise device-flow that the UI uses, exposes it as a CLI, and stores the resulting token via the admin DB connection.

- [ ] **Step 1: Read the existing UI device-flow code**

Run: `grep -rn "device.code\|verification_uri\|user_code" internal/llm/ internal/webapi/`

Identify the function that initiates the device-flow and the function that stores the token. The CLI command will call them in sequence.

- [ ] **Step 2: Write failing test**

```go
// cmd/cronfoundry/admin_connectcopilot_test.go
package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Stub GitHub device-flow endpoints.
func newDeviceFlowStub(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/login/device/code", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"device_code":      "DEV123",
			"user_code":        "WDJB-MJHT",
			"verification_uri": "https://github.com/login/device",
			"expires_in":       900,
			"interval":         5
		}`))
	})
	mux.HandleFunc("/login/oauth/access_token", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"access_token": "ghu_test",
			"token_type":   "bearer",
			"scope":        "copilot"
		}`))
	})
	return httptest.NewServer(mux)
}

func TestConnectCopilotCmd_PrintsUserCodeAndStoresToken(t *testing.T) {
	stub := newDeviceFlowStub(t)
	defer stub.Close()

	stored := map[string]string{}
	cmd := newAdminConnectCopilotCmd(connectCopilotDeps{
		DeviceCodeURL: stub.URL + "/login/device/code",
		TokenURL:      stub.URL + "/login/oauth/access_token",
		PollInterval:  0,
		StoreToken: func(ctx context.Context, prefix, token string) error {
			stored[prefix] = token
			return nil
		},
	})
	cmd.SetArgs([]string{"--prefix", "copilot"})
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	output := out.String()
	if !strings.Contains(output, "WDJB-MJHT") {
		t.Errorf("expected output to show user code, got: %s", output)
	}
	if stored["copilot"] != "ghu_test" {
		t.Errorf("expected token stored, got: %v", stored)
	}
}
```

- [ ] **Step 3: Run, verify failure**

Run: `go test ./cmd/cronfoundry/ -run TestConnectCopilotCmd -count=1`
Expected: FAIL — `newAdminConnectCopilotCmd` undefined.

- [ ] **Step 4: Implement**

```go
// cmd/cronfoundry/admin_connectcopilot.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

type connectCopilotDeps struct {
	DeviceCodeURL string
	TokenURL      string
	PollInterval  time.Duration
	HTTPClient    *http.Client
	StoreToken    func(ctx context.Context, prefix, token string) error
}

func newAdminConnectCopilotCmd(deps connectCopilotDeps) *cobra.Command {
	var prefix string
	if deps.HTTPClient == nil {
		deps.HTTPClient = http.DefaultClient
	}
	if deps.PollInterval == 0 {
		deps.PollInterval = 5 * time.Second
	}
	if deps.DeviceCodeURL == "" {
		deps.DeviceCodeURL = "https://github.com/login/device/code"
	}
	if deps.TokenURL == "" {
		deps.TokenURL = "https://github.com/login/oauth/access_token"
	}

	cmd := &cobra.Command{
		Use:   "connect-copilot",
		Short: "Run Copilot Enterprise device-flow and store the access token",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			dc, err := requestDeviceCode(ctx, deps)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"Open %s in your browser and enter code: %s\n",
				dc.VerificationURI, dc.UserCode)

			tok, err := pollForToken(ctx, deps, dc)
			if err != nil {
				return err
			}

			if deps.StoreToken != nil {
				if err := deps.StoreToken(ctx, prefix, tok); err != nil {
					return fmt.Errorf("store token: %w", err)
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "✓ Copilot Enterprise connected (prefix: %s)\n", prefix)
			return nil
		},
	}

	cmd.Flags().StringVar(&prefix, "prefix", "copilot", "Provider prefix")
	return cmd
}

type deviceCodeResp struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type tokenResp struct {
	AccessToken string `json:"access_token"`
	Error       string `json:"error"`
}

func requestDeviceCode(ctx context.Context, d connectCopilotDeps) (*deviceCodeResp, error) {
	form := url.Values{
		"client_id": {"Iv1.b507a08c87ecfe98"}, // GitHub Copilot's public client ID
		"scope":     {"read:user"},
	}
	req, err := http.NewRequestWithContext(ctx, "POST", d.DeviceCodeURL,
		strings.NewReader(form.Encode()))
	if err != nil { return nil, err }
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := d.HTTPClient.Do(req)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	var out deviceCodeResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil { return nil, err }
	return &out, nil
}

func pollForToken(ctx context.Context, d connectCopilotDeps, dc *deviceCodeResp) (string, error) {
	deadline := time.Now().Add(time.Duration(dc.ExpiresIn) * time.Second)
	if dc.ExpiresIn == 0 {
		deadline = time.Now().Add(15 * time.Minute)
	}
	for time.Now().Before(deadline) {
		form := url.Values{
			"client_id":   {"Iv1.b507a08c87ecfe98"},
			"device_code": {dc.DeviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}
		req, err := http.NewRequestWithContext(ctx, "POST", d.TokenURL,
			strings.NewReader(form.Encode()))
		if err != nil { return "", err }
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		resp, err := d.HTTPClient.Do(req)
		if err != nil { return "", err }
		var tr tokenResp
		_ = json.NewDecoder(resp.Body).Decode(&tr)
		resp.Body.Close()
		if tr.AccessToken != "" {
			return tr.AccessToken, nil
		}
		if tr.Error != "" && tr.Error != "authorization_pending" && tr.Error != "slow_down" {
			return "", fmt.Errorf("device flow error: %s", tr.Error)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(d.PollInterval):
		}
	}
	return "", fmt.Errorf("device flow timed out")
}
```

The `StoreToken` dependency is what production wiring fills in: a real call into `internal/llm/copilot` or a direct DB insert via the admin connection. The default registration in `admin.go` provides that wiring; this test injects a stub.

- [ ] **Step 5: Wire the real StoreToken in admin.go**

In `cmd/cronfoundry/admin.go`, locate where other `admin_*` commands are registered, and add:

```go
import (
	// existing imports
	"github.com/gambtho/cronfoundry/internal/llm/copilot"
)

// ...

connectCopilotDeps := connectCopilotDeps{
	StoreToken: func(ctx context.Context, prefix, token string) error {
		// Open admin DB connection like other admin commands do; reuse helper.
		db, err := openAdminDB(ctx)
		if err != nil { return err }
		defer db.Close()
		return copilot.StoreToken(ctx, db, prefix, token)
	},
}
adminCmd.AddCommand(newAdminConnectCopilotCmd(connectCopilotDeps))
```

If `copilot.StoreToken` doesn't exist, add it as a thin wrapper around whatever the UI device-flow handler uses (find that with `grep -rn "copilot" internal/webapi/`).

- [ ] **Step 6: Tests pass**

Run: `go test ./cmd/cronfoundry/ -run TestConnectCopilotCmd -count=1`
Expected: PASS.
Run: `go vet ./...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add cmd/cronfoundry/admin_connectcopilot.go cmd/cronfoundry/admin_connectcopilot_test.go cmd/cronfoundry/admin.go internal/llm/copilot.go
git commit -m "feat(admin): connect-copilot CLI for Copilot Enterprise device-flow"
```

---

## Task 9: Wire connect-repo, set-secret, connect-copilot into install.sh

**Files:**
- Modify: `scripts/quickstart-copilot.sh`.

After the manifest flow + install-ID discovery, the script now has all the inputs the existing admin CLIs need. Replace the §17 manual UI clicks with CLI calls.

- [ ] **Step 1: Add the three CLI calls**

```bash
# §17: connect repo
header "[step 17/19] Connect skill repo via admin CLI"
./cronfoundry admin connect-repo "${CF_SKILL_REPO}" \
  --installation-id "${CF_INSTALLATION_ID}" \
  --base-url "https://${CF_FQDN}" \
  || die "connect-repo failed"
ok "Repo connected: ${CF_SKILL_REPO}"

# §17b: set webhook secret
header "[step 17b/19] Store webhook secret"
echo -n "${CF_GITHUB_WEBHOOK_SECRET}" | ./cronfoundry admin set-secret github_webhook_secret \
  --base-url "https://${CF_FQDN}" \
  || die "set-secret failed"
ok "Webhook secret stored"

# §17c: copilot device-flow
header "[step 17c/19] Connect Copilot Enterprise"
./cronfoundry admin connect-copilot --prefix copilot \
  --base-url "https://${CF_FQDN}" \
  || die "connect-copilot failed"
```

- [ ] **Step 2: Verify admin commands accept --base-url**

Run: `./cronfoundry admin connect-repo --help`

If `--base-url` doesn't exist, add it to each admin command (one small commit per command). The flag points the command at the deployed instance instead of expecting a local DB connection.

This may already work via `CRONFOUNDRY_BASE_URL` env var — check `grep -n "BASE_URL\|base-url" cmd/cronfoundry/admin_*.go` and adapt accordingly.

- [ ] **Step 3: Smoke test on a real Azure deploy**

End-to-end: deploy, manifest, then verify the three steps run without `read -p` interruptions.

- [ ] **Step 4: Commit**

```bash
git add scripts/quickstart-copilot.sh cmd/cronfoundry/admin_connectrepo.go cmd/cronfoundry/admin_setsecret.go
git commit -m "feat(quickstart): script connect-repo, set-secret, connect-copilot via CLI"
```

---

## Task 10: Auto-push starter `cronfoundry.yaml` + `SKILL.md` to the skill repo

**Files:**
- Create: `scripts/templates/cronfoundry.yaml.tmpl` — skill manifest template.
- Create: `scripts/templates/smoke-skill.md.tmpl` — SKILL.md template.
- Modify: `scripts/quickstart-copilot.sh`.

- [ ] **Step 1: Create the templates**

`scripts/templates/cronfoundry.yaml.tmpl`:

```yaml
version: 1
skills:
  - path: skills/smoke
    schedules:
      - name: every-5
        cron: "*/5 * * * *"
        timezone: UTC
        provider: copilot-enterprise
        copilot_prefix: copilot
        model: gpt-4o
        destinations:
          - github-issue:
              repo: __REPORTS_REPO__
              title: "smoke — {{ run.date }}"
              labels: [smoke]
        writeback:
          enabled: true
          path: memory.md
          mode: append
```

`scripts/templates/smoke-skill.md.tmpl`:

```markdown
---
name: smoke
description: Proves the CronFoundry pipeline end to end
max_tokens: 200
---
Write one short sentence confirming this pipeline works.
End with:
<memory>
run at {{ run.started_at }}
</memory>
```

- [ ] **Step 2: Add the auto-push step**

```bash
# §18: push starter skill to skill repo
header "[step 18/19] Push starter skill to ${CF_SKILL_REPO}"

# Check if cronfoundry.yaml already exists on default branch.
DEFAULT_BRANCH=$(gh api "repos/${CF_SKILL_REPO}" --jq .default_branch)
if gh api "repos/${CF_SKILL_REPO}/contents/cronfoundry.yaml?ref=${DEFAULT_BRANCH}" &>/dev/null; then
  ok "cronfoundry.yaml already exists in ${CF_SKILL_REPO}; skipping"
else
  # Render templates
  TMPDIR=$(mktemp -d)
  sed "s|__REPORTS_REPO__|${CF_REPORTS_REPO}|g" \
    "scripts/templates/cronfoundry.yaml.tmpl" > "${TMPDIR}/cronfoundry.yaml"
  cp "scripts/templates/smoke-skill.md.tmpl" "${TMPDIR}/SKILL.md"

  # Push via gh api (creates files on a new branch, opens a PR)
  YAML_B64=$(base64 -w 0 "${TMPDIR}/cronfoundry.yaml")
  SKILL_B64=$(base64 -w 0 "${TMPDIR}/SKILL.md")

  # Get default-branch SHA
  PARENT_SHA=$(gh api "repos/${CF_SKILL_REPO}/branches/${DEFAULT_BRANCH}" --jq .commit.sha)

  # Create branch
  gh api -X POST "repos/${CF_SKILL_REPO}/git/refs" \
    -f ref="refs/heads/cronfoundry-quickstart" \
    -f sha="${PARENT_SHA}" --silent

  # Create files on the branch
  gh api -X PUT "repos/${CF_SKILL_REPO}/contents/cronfoundry.yaml" \
    -f branch="cronfoundry-quickstart" \
    -f message="chore: cronfoundry quickstart manifest" \
    -f content="${YAML_B64}" --silent

  gh api -X PUT "repos/${CF_SKILL_REPO}/contents/skills/smoke/SKILL.md" \
    -f branch="cronfoundry-quickstart" \
    -f message="chore: cronfoundry smoke skill" \
    -f content="${SKILL_B64}" --silent

  # Open a PR
  PR_URL=$(gh pr create \
    --repo "${CF_SKILL_REPO}" \
    --base "${DEFAULT_BRANCH}" \
    --head "cronfoundry-quickstart" \
    --title "CronFoundry quickstart: add smoke skill" \
    --body "Adds a 5-minute smoke schedule to verify the install. Merge to start firing." \
    --json url --jq .url)

  ok "PR opened: ${PR_URL}"
  echo "Merge this PR to fire the first run."
  read -rp "Press Enter once merged..."

  rm -rf "${TMPDIR}"
fi
```

- [ ] **Step 3: Commit**

```bash
git add scripts/templates/ scripts/quickstart-copilot.sh
git commit -m "feat(quickstart): auto-push starter skill via gh api"
```

---

## Task 11: Wait for the first green run

**Files:**
- Modify: `scripts/quickstart-copilot.sh`.

- [ ] **Step 1: Add the polling step**

```bash
# §19: wait for first green run
header "[step 19/19] Waiting for first run"
START=$(date +%s)
DEADLINE=$((START + 900))  # 15 minutes max wait (covers webhook delay + 5-min cron)

while [[ $(date +%s) -lt $DEADLINE ]]; do
  RUNS=$(curl -fsS "https://${CF_FQDN}/api/runs?limit=1" 2>/dev/null || echo '[]')
  STATUS=$(echo "$RUNS" | jq -r '.[0].status // empty')
  case "$STATUS" in
    succeeded)
      ELAPSED=$(($(date +%s) - START))
      ok "✅ First run green in ${ELAPSED}s — open https://${CF_FQDN}/"
      exit 0
      ;;
    failed|partial_failure)
      RUN_ID=$(echo "$RUNS" | jq -r '.[0].id')
      die "First run status: ${STATUS}. Inspect at https://${CF_FQDN}/runs/${RUN_ID}"
      ;;
  esac
  sleep 10
done
warn "First run did not complete in 15 minutes. Check the dashboard at https://${CF_FQDN}/"
exit 1
```

Note: `/api/runs` may require auth. If so, the script needs an admin token. Check `grep -n "/api/runs" internal/webapi/`. If auth is required, either:
- Use a public `/healthz/last-run` endpoint that returns the latest run status without auth (small new endpoint, separate task), or
- Skip the auto-tail and just print "open https://${CF_FQDN}/runs to watch your first run".

Pick the simpler path: print-and-exit if auth blocks polling, leave auto-tail as a follow-up.

- [ ] **Step 2: Commit**

```bash
git add scripts/quickstart-copilot.sh
git commit -m "feat(quickstart): poll for first green run after install"
```

---

## Task 12: Add `cronfoundry-quickstart down` teardown command

**Files:**
- Create: `scripts/quickstart-down.sh`.
- Modify: `Makefile` to add a `quickstart-down ENV=copilot1` target (optional).

- [ ] **Step 1: Implement teardown script**

```bash
#!/usr/bin/env bash
# scripts/quickstart-down.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/lib/state.sh"

ENV_SUFFIX="${1:-}"
[[ -z "$ENV_SUFFIX" ]] && { echo "usage: $0 <env-suffix>"; exit 1; }

STATE_FILE=$(state_path_for "$ENV_SUFFIX")
state_load

# 1. Delete resource group (no-wait)
echo "Deleting resource group rg-cronfoundry-${ENV_SUFFIX}..."
az group delete --name "rg-cronfoundry-${ENV_SUFFIX}" --yes --no-wait || true

# 2. Revoke GitHub App installation
if [[ -n "${CF_GITHUB_APP_ID:-}" && -n "${CF_INSTALLATION_ID:-}" && -n "${CF_GITHUB_PEM_PATH:-}" ]]; then
  echo "Revoking GitHub App installation ${CF_INSTALLATION_ID}..."
  JWT=$("${SCRIPT_DIR}/../cronfoundry" setup mint-jwt \
    --app-id "$CF_GITHUB_APP_ID" --pem "$CF_GITHUB_PEM_PATH")
  curl -fsS -X DELETE \
    -H "Accept: application/vnd.github+json" \
    -H "Authorization: Bearer ${JWT}" \
    "https://api.github.com/app/installations/${CF_INSTALLATION_ID}" || true
fi

# 3. Remove state file
state_clear

echo "Done. The GitHub App registration itself remains; delete it manually if desired:"
echo "  https://github.com/settings/apps/${CF_GITHUB_APP_SLUG:-<your-app-name>}"
```

- [ ] **Step 2: Commit**

```bash
git add scripts/quickstart-down.sh
git commit -m "feat(quickstart): add teardown script with state cleanup and install revoke"
```

---

## Self-review checklist (run after all tasks above land)

- [ ] **Re-read the spec §Phase 1 and confirm every step in the table maps to a task above.** If a step is missing, add a task.
- [ ] **Run the full script on a clean Azure subscription.** Document any friction in `docs/superpowers/specs/<date>-quickstart-dogfood-round1.md`. (This is Phase 2, not Phase 1 — flag as the handoff point.)
- [ ] **All Go tests pass:** `go test ./...`
- [ ] **All bats tests pass:** `bats scripts/lib/`
- [ ] **`go vet ./...` clean.**

---

## Handoff to Phase 2

Once Tasks 1–12 land and pass tests, the script is ready for Phase 2 (live Azure dogfood). Phase 2's plan
(`docs/superpowers/plans/2026-04-30-prerelease-phase2-dogfood.md`) is a runbook, not a TDD task list.
