#!/usr/bin/env bash
set -euo pipefail

# ── colours ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

info()    { echo -e "${CYAN}[info]${RESET}  $*"; }
ok()      { echo -e "${GREEN}[ok]${RESET}    $*"; }
warn()    { echo -e "${YELLOW}[warn]${RESET}  $*"; }
die()     { echo -e "${RED}[error]${RESET} $*" >&2; exit 1; }
header()  { echo -e "\n${BOLD}$*${RESET}"; }

DRY_RUN=false
MANIFEST_TIMEOUT="2h"
OPERATOR_OBJ_ID="${CRONFOUNDRY_OPERATOR_OBJECT_ID:-}"
CLI_REPORTS_REPO=""
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=true ;;
    --manifest-timeout=*) MANIFEST_TIMEOUT="${arg#*=}" ;;
    --operator-object-id=*) OPERATOR_OBJ_ID="${arg#*=}" ;;
    --reports-repo=*) CLI_REPORTS_REPO="${arg#*=}" ;;
  esac
done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/state.sh
source "${SCRIPT_DIR}/lib/state.sh"
# shellcheck source=lib/steps.sh
source "${SCRIPT_DIR}/lib/steps.sh"
save() { state_save "$1" "$2"; }   # shorthand for state_save

# Retry helper for transient gh/network failures (TLS handshake timeouts,
# rate-limit blips, etc.). Runs the given command up to 3 times with
# 3s/6s exponential backoff between attempts. Returns the command's
# last exit code.
gh_retry() {
  local attempt=1 max=3 delay
  while true; do
    if "$@"; then
      return 0
    fi
    local rc=$?
    if (( attempt >= max )); then
      return "$rc"
    fi
    delay=$(( attempt * 3 ))
    warn "gh transient failure (attempt ${attempt}/${max}); retrying in ${delay}s..."
    sleep "$delay"
    attempt=$(( attempt + 1 ))
  done
}

# Resolve the operator's AAD object ID. Prefer 'az ad signed-in-user show', but
# fall back to decoding 'oid' from the access token JWT when Conditional Access
# blocks the Graph call (common on locked-down corporate tenants). Cache to
# OPERATOR_OBJ_ID so multiple steps don't re-pay the cost.
resolve_operator_object_id() {
  if [[ -n "${OPERATOR_OBJ_ID:-}" ]]; then
    return 0
  fi
  if OPERATOR_OBJ_ID=$(az ad signed-in-user show --query id -o tsv 2>/dev/null) \
    && [[ -n "$OPERATOR_OBJ_ID" ]]; then
    return 0
  fi
  warn "az ad signed-in-user show was blocked (likely Conditional Access); decoding access token instead."
  local token
  token=$(az account get-access-token --query accessToken -o tsv 2>/dev/null) || token=""
  if [[ -n "$token" ]]; then
    OPERATOR_OBJ_ID=$(python3 - "$token" <<'PYEOF' 2>/dev/null || echo ""
import base64, json, sys
parts = sys.argv[1].split(".")
if len(parts) < 2:
    sys.exit(0)
payload = parts[1] + "=" * (-len(parts[1]) % 4)
claims = json.loads(base64.urlsafe_b64decode(payload))
oid = claims.get("oid", "")
sys.stdout.write(oid)
PYEOF
)
  fi
  if [[ -z "${OPERATOR_OBJ_ID:-}" ]]; then
    die "Could not resolve operator object ID.\n  Conditional Access blocked 'az ad signed-in-user show', and access-token decode also failed.\n  Workaround: pass --operator-object-id=<your-oid>, or set CRONFOUNDRY_OPERATOR_OBJECT_ID env var.\n  Find your OID at https://portal.azure.com → Microsoft Entra ID → Users → (your account) → Object ID."
  fi
}

# Resume detection: the env suffix prompt has to happen BEFORE any other state
# is read, so re-runs pick up the right per-env state file. List existing
# per-env files; default-pick the only one if there's just one; else ask.
list_env_states() {
  # shellcheck disable=SC2012
  ls -1 "${HOME}"/.cronfoundry-quickstart-state-* 2>/dev/null \
    | sed -E "s|^${HOME}/.cronfoundry-quickstart-state-||"
}

# An existing CF_ENV value lives in this file iff a per-env state already
# exists for that suffix. Used both to detect legacy files and to refuse
# duplicate suffix entry when the user picks "n" for "new env".
env_state_exists() {
  [[ -f "${HOME}/.cronfoundry-quickstart-state-$1" ]]
}

# Migrate the legacy unsuffixed state file (Phase 1's pre-per-env layout) so
# in-flight quickstarts resume cleanly. Read the file's saved CF_ENV, if any,
# and rename to the per-env path. If no CF_ENV is saved, the legacy file is
# from before the env-suffix prompt was reached — drop it; subsequent prompts
# will recreate everything in the per-env file from scratch.
migrate_legacy_state_file() {
  local legacy="${HOME}/.cronfoundry-quickstart-state"
  [[ -f "$legacy" ]] || return 0
  # shellcheck disable=SC1090
  local saved_env
  saved_env=$(grep -E '^CF_ENV=' "$legacy" 2>/dev/null | tail -1 | sed -E 's/^CF_ENV=//; s/^"//; s/"$//')
  if [[ -n "$saved_env" ]] && [[ ! -f "${HOME}/.cronfoundry-quickstart-state-${saved_env}" ]]; then
    mv "$legacy" "${HOME}/.cronfoundry-quickstart-state-${saved_env}"
    echo -e "${CYAN}[info]${RESET}  Migrated legacy state file → ~/.cronfoundry-quickstart-state-${saved_env}"
  else
    rm -f "$legacy"
  fi
}

select_env_suffix() {
  migrate_legacy_state_file
  local existing
  mapfile -t existing < <(list_env_states)
  if [[ ${#existing[@]} -eq 0 ]]; then
    while true; do
      read -rp "Env suffix (<=10 chars, lowercase/numbers/hyphens, default: copilot1): " CF_ENV
      CF_ENV="${CF_ENV:-copilot1}"
      if [[ ! "$CF_ENV" =~ ^[a-z0-9-]{1,10}$ ]]; then
        echo -e "${YELLOW}[warn]${RESET}  Invalid suffix '$CF_ENV'. Use only lowercase letters, numbers, and hyphens, max 10 chars."
        continue
      fi
      if env_state_exists "$CF_ENV"; then
        echo -e "${YELLOW}[warn]${RESET}  Env '$CF_ENV' already has a state file — re-run without args to resume it, or pick a different suffix."
        continue
      fi
      return 0
    done
  elif [[ ${#existing[@]} -eq 1 ]]; then
    CF_ENV="${existing[0]}"
    echo -e "${CYAN}[info]${RESET}  Resuming env '$CF_ENV' from previous run (state file: ${HOME}/.cronfoundry-quickstart-state-${CF_ENV})"
    return 0
  fi
  echo "Existing env states found:"
  local i=1
  for s in "${existing[@]}"; do echo "  $i) $s"; i=$((i+1)); done
  echo "  n) start a new env"
  while true; do
    read -rp "Resume which env? [1-${#existing[@]} or n]: " choice
    if [[ "$choice" == "n" ]]; then
      while true; do
        read -rp "New env suffix (<=10 chars, lowercase/numbers/hyphens): " CF_ENV
        if [[ ! "$CF_ENV" =~ ^[a-z0-9-]{1,10}$ ]]; then
          echo -e "${YELLOW}[warn]${RESET}  Invalid suffix."
          continue
        fi
        if env_state_exists "$CF_ENV"; then
          echo -e "${YELLOW}[warn]${RESET}  Env '$CF_ENV' already exists — pick a different suffix, or restart and resume from the menu."
          continue
        fi
        return 0
      done
    elif [[ "$choice" =~ ^[0-9]+$ ]] && (( choice >= 1 && choice <= ${#existing[@]} )); then
      CF_ENV="${existing[$((choice-1))]}"
      return 0
    fi
    echo -e "${YELLOW}[warn]${RESET}  Invalid choice."
  done
}

# Pick env BEFORE state load so we use the right file from the start.
select_env_suffix
STATE_FILE="$(state_path_for "$CF_ENV")"
export STATE_FILE
state_init
state_load
# Persist CF_ENV (idempotent) and compute resource names once.
save CF_ENV "$CF_ENV"
KV_NAME="cf-kv-${CF_ENV}"
CA_NAME="cf-serve-${CF_ENV}"
RG="rg-cronfoundry-${CF_ENV}"

GUIDE_URL="https://gambtho.github.io/cronfoundry/guides/quickstart-copilot.html"

# ── Step 1: prereq check ─────────────────────────────────────────────────────
header "[step 1/25] Checking prerequisites"

check_cmd() {
  local cmd=$1 hint=$2
  if ! command -v "$cmd" &>/dev/null; then
    die "'$cmd' not found. $hint\nSee §1 of $GUIDE_URL"
  fi
  ok "$cmd found"
}

check_cmd az      "Install: https://learn.microsoft.com/cli/azure/install-azure-cli"
check_cmd git     "Install: https://git-scm.com/downloads"
check_cmd python3 "Install: https://www.python.org/downloads/"
check_cmd openssl "Usually pre-installed. Install via your OS package manager."
check_cmd curl    "Install via your OS package manager."
check_cmd go      "Install: https://golang.org/dl/ (need >= 1.21)"
check_cmd gh "Install: https://cli.github.com/"
check_cmd jq "Install: https://stedolan.github.io/jq/download/"
if ! gh auth status &>/dev/null; then
  die "gh is installed but not authenticated. Run: gh auth login\nThen re-run this script."
fi
ok "gh authenticated as $(gh api /user --jq .login)"

# az version check (need >= 2.60)
AZ_VER=$(az version --query '"azure-cli"' -o tsv 2>/dev/null || echo "0.0.0")
AZ_MAJOR=$(echo "$AZ_VER" | cut -d. -f1)
AZ_MINOR=$(echo "$AZ_VER" | cut -d. -f2)
if [[ "$AZ_MAJOR" -lt 2 ]] || { [[ "$AZ_MAJOR" -eq 2 ]] && [[ "$AZ_MINOR" -lt 60 ]]; }; then
  die "az CLI $AZ_VER found; need >= 2.60. Run: az upgrade"
fi
ok "az $AZ_VER"

# bicep check (need >= 0.26). Single call: capture output and exit code; only
# run 'az bicep install' on real failure. The first 'az bicep version' call
# is slow (~30-60s cold-start) on machines that haven't run any az command
# yet — running it twice doubled the wait without any benefit.
BICEP_OUT=$(az bicep version 2>/dev/null)
BICEP_RC=$?
if (( BICEP_RC != 0 )); then
  info "Bicep not found -- installing via az bicep install..."
  az bicep install || die "Failed to install Bicep. Check internet connectivity.\nSee §1 of $GUIDE_URL"
  BICEP_OUT=$(az bicep version 2>/dev/null)
fi
BICEP_VER=$(echo "$BICEP_OUT" | grep -Eo '[0-9]+\.[0-9]+' | head -1 || echo "0.0")
BICEP_MINOR=$(echo "$BICEP_VER" | cut -d. -f2)
if [[ "${BICEP_MINOR:-0}" -lt 26 ]]; then
  warn "Bicep $BICEP_VER found; recommend >= 0.26. Run: az bicep upgrade"
fi
ok "bicep $BICEP_VER"

ok "All prerequisites satisfied"

# ── Step 2: az login ──────────────────────────────────────────────────────────
header "[step 2/25] Azure login"
if az account show &>/dev/null; then
  CURRENT_ACCOUNT=$(az account show --query '[name, id]' -o tsv | tr '\t' ' / ')
  ok "Already logged in: $CURRENT_ACCOUNT"
else
  info "Running az login..."
  az login
fi

# ── Step 3: subscription ──────────────────────────────────────────────────────
header "[step 3/25] Select subscription"
if [[ -z "${CF_SUBSCRIPTION_ID:-}" ]]; then
  az account list --query '[].{Name:name, ID:id}' -o table
  read -rp "Enter subscription ID or name: " CF_SUBSCRIPTION_ID
  az account set --subscription "$CF_SUBSCRIPTION_ID" \
    || die "Could not set subscription '$CF_SUBSCRIPTION_ID'. Verify it exists and you have access.\nSee §3 of $GUIDE_URL"
  save CF_SUBSCRIPTION_ID "$CF_SUBSCRIPTION_ID"
else
  az account set --subscription "$CF_SUBSCRIPTION_ID" \
    || die "Could not re-apply saved subscription '$CF_SUBSCRIPTION_ID'.\nSee §3 of $GUIDE_URL"
fi
ok "Subscription: $CF_SUBSCRIPTION_ID"

# ── Step 4: clone check ───────────────────────────────────────────────────────
header "[step 4/25] Verify cronfoundry clone"
if [[ ! -f "deploy/main.bicep" ]]; then
  die "Run this script from inside a cronfoundry clone.\n  git clone https://github.com/gambtho/cronfoundry && cd cronfoundry\nSee §4 of $GUIDE_URL"
fi
REPO_ROOT=$(git rev-parse --show-toplevel)
ok "Repo root: $REPO_ROOT"

# ── Step 5: skill repo ────────────────────────────────────────────────────────
header "[step 5/25] Skill repo"
if [[ -z "${CF_SKILL_REPO:-}" ]]; then
  echo "  CronFoundry pulls scheduled skill definitions (cronfoundry.yaml + skills/) from"
  echo "  a GitHub repo. The installer will open a PR there with a starter smoke skill."
  while true; do
    read -rp "Skill repo (owner/repo): " CF_SKILL_REPO_INPUT
    if [[ ! "$CF_SKILL_REPO_INPUT" =~ ^[^/[:space:]]+/[^/[:space:]]+$ ]]; then
      warn "Expected 'owner/repo'; got '$CF_SKILL_REPO_INPUT'."
      continue
    fi
    # gh api exit codes don't distinguish 404 from auth/network errors, so
    # capture HTTP status and only treat 404 as "not exist → offer to create".
    HTTP_STATUS=$(gh api "repos/${CF_SKILL_REPO_INPUT}" --silent \
      -i 2>/dev/null | grep -E "^HTTP/" | head -1 | awk '{print $2}')
    case "${HTTP_STATUS:-}" in
      200)
        CF_SKILL_REPO="$CF_SKILL_REPO_INPUT"
        break
        ;;
      404)
        read -rp "Repo $CF_SKILL_REPO_INPUT not found. Create it as a private repo? [Y/n]: " mkrepo
        mkrepo="${mkrepo:-y}"
        if [[ "$mkrepo" =~ ^[Yy]$ ]]; then
          info "Creating private repo $CF_SKILL_REPO_INPUT..."
          gh repo create "$CF_SKILL_REPO_INPUT" --private --add-readme \
            || die "gh repo create failed; verify your auth has 'repo' scope and the name is available."
          CF_SKILL_REPO="$CF_SKILL_REPO_INPUT"
          break
        fi
        # else: loop and let the operator paste a different name
        ;;
      "")
        die "gh api 'repos/${CF_SKILL_REPO_INPUT}' returned no HTTP status — gh may not be authenticated or network is unreachable. Run 'gh auth status'."
        ;;
      *)
        die "gh api 'repos/${CF_SKILL_REPO_INPUT}' returned HTTP ${HTTP_STATUS} (auth scope or rate-limit issue). Run 'gh auth status' or check https://api.github.com rate limits."
        ;;
    esac
  done
  save CF_SKILL_REPO "$CF_SKILL_REPO"
fi
ok "Skill repo: $CF_SKILL_REPO"

# ── Step 6: reports repo ──────────────────────────────────────────────────────
# By default reports go to the same repo as skills. Override via --reports-repo
# or by exporting CF_REPORTS_REPO before running.
header "[step 6/25] Reports repo"
if [[ -z "${CF_REPORTS_REPO:-}" ]]; then
  if [[ -n "$CLI_REPORTS_REPO" ]]; then
    if [[ ! "$CLI_REPORTS_REPO" =~ ^[^/[:space:]]+/[^/[:space:]]+$ ]]; then
      die "--reports-repo expects 'owner/repo'; got '$CLI_REPORTS_REPO'."
    fi
    CF_REPORTS_REPO="$CLI_REPORTS_REPO"
  else
    CF_REPORTS_REPO="$CF_SKILL_REPO"
  fi
  save CF_REPORTS_REPO "$CF_REPORTS_REPO"
fi
ok "Reports repo: $CF_REPORTS_REPO"

# ── Step 7: env suffix (already chosen above; restate + warn) ─────────────────
header "[step 7/25] Environment suffix"
warn "Key Vault soft-delete retains the name 'cf-kv-${CF_ENV}' for 7 days after teardown."
warn "Re-runs after teardown need a new suffix (e.g. copilot2)."
ok "Env: $CF_ENV"

# ── Step 8: master key ────────────────────────────────────────────────────────
header "[step 8/25] Generate master key"
if [[ -z "${CF_MASTER_KEY:-}" ]]; then
  CF_MASTER_KEY=$(openssl rand -base64 32)
  save CF_MASTER_KEY "$CF_MASTER_KEY"
  warn "SAVE THIS KEY -- if lost, encrypted secrets are unrecoverable."
  echo "  Master key: $CF_MASTER_KEY"
fi
ok "Master key ready"

# ── Step 9: region ───────────────────────────────────────────────────────────
header "[step 9/25] Region"
if [[ -z "${CF_REGION:-}" ]]; then
  read -rp "Azure region (default: swedencentral): " CF_REGION
  CF_REGION="${CF_REGION:-swedencentral}"
  save CF_REGION "$CF_REGION"
fi
info "Note: Postgres Flexible Server SKU/quota varies by region; swedencentral and eastus2 are widely available."
info "See §10 of $GUIDE_URL"
ok "Region: $CF_REGION"

# ── Step 10: image tag ────────────────────────────────────────────────────────
header "[step 10/25] Image tag"
if [[ -z "${CF_IMAGE_TAG:-}" ]]; then
  CF_IMAGE_TAG=$(curl -fsSL "https://api.github.com/repos/gambtho/cronfoundry/releases/latest" \
    2>/dev/null \
    | python3 -c "import sys,json; d=json.load(sys.stdin); print(d['tag_name'].lstrip('v'))" \
    2>/dev/null) || CF_IMAGE_TAG=""
  if [[ -z "$CF_IMAGE_TAG" ]]; then
    warn "Could not fetch latest release tag from GitHub API (network issue or rate limit)."
    read -rp "Enter image tag manually (e.g. 0.7.6) [default: latest]: " CF_IMAGE_TAG
    CF_IMAGE_TAG="${CF_IMAGE_TAG:-latest}"
  fi
  save CF_IMAGE_TAG "$CF_IMAGE_TAG"
fi
ok "Image tag: $CF_IMAGE_TAG"

# ── Step 11: postgres password ────────────────────────────────────────────────
header "[step 11/25] Generate Postgres password"
if [[ -z "${CF_PG_PASSWORD:-}" ]]; then
  CF_PG_PASSWORD=$(openssl rand -base64 48 | tr -dc 'A-Za-z0-9' | head -c24)
  save CF_PG_PASSWORD "$CF_PG_PASSWORD"
fi
ok "Postgres password generated (saved to state file)"

# ── Step 12: build params file ────────────────────────────────────────────────
header "[step 12/25] Build params file"
PARAMS_FILE="deploy/params.quickstart-${CF_ENV}.json"
ADMIN_LOGIN=$(gh api /user --jq .login 2>/dev/null) || ADMIN_LOGIN=""
if [[ -z "$ADMIN_LOGIN" ]]; then
  ADMIN_LOGIN=$(git config user.name 2>/dev/null) || ADMIN_LOGIN=""
fi
if [[ -z "$ADMIN_LOGIN" ]]; then
  warn "Could not infer GitHub login from gh or git config; using 'admin' as adminLogins value."
  warn "You may want to set it: git config --global user.name 'Your GitHub login'"
  ADMIN_LOGIN="admin"
fi
PARAMS_TMP="${PARAMS_FILE}.tmp.$$"
# GitHub App fields are intentionally empty here: Bicep deploys before the
# GitHub App exists. Task 6 of the manifest flow pushes real values via
# `az containerapp update --set-env-vars` after registration.
python3 - "$PARAMS_TMP" \
    "$CF_ENV" "$CF_REGION" "$CF_IMAGE_TAG" \
    "$CF_PG_PASSWORD" "$CF_MASTER_KEY" "$ADMIN_LOGIN" << 'PYEOF'
import json, sys
out_path, env, region, tag, pg_pw, master_key, admin = sys.argv[1:]
params = {
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
  "contentVersion": "1.0.0.0",
  "parameters": {
    "env":                        {"value": env},
    "location":                   {"value": region},
    "imageTag":                   {"value": tag},
    "githubAppId":                {"value": ""},
    "githubAppOAuthClientId":     {"value": ""},
    "githubAppOAuthClientSecret": {"value": ""},
    "githubAppPem":               {"value": ""},
    "postgresAdminPassword":      {"value": pg_pw},
    "masterKey":                  {"value": master_key},
    "adminLogins":                {"value": admin},
    "viewerLogins":               {"value": ""},
    "ingressExternal":            {"value": True},
  }
}
with open(out_path, "w") as f:
    json.dump(params, f, indent=2)
print(f"Wrote {out_path}")
PYEOF
mv "$PARAMS_TMP" "$PARAMS_FILE"
chmod 600 "$PARAMS_FILE"
ok "Params file: $PARAMS_FILE"

# ── Step 13: deploy ───────────────────────────────────────────────────────────
header "[step 13/25] Deploy to Azure (~10 min)"
if [[ "$DRY_RUN" == "true" ]]; then
  warn "--dry-run: skipping deploy; later steps will use a placeholder FQDN"
  CF_FQDN="<not-yet-deployed>"
else
  # Per-env deployment name avoids 'DeploymentActive' conflicts with concurrent envs.
  CF_DEPLOY_NAME="cronfoundry-${CF_ENV}"

  # If the previous run kicked off a deploy and the script aborted, the deployment
  # may still be Running. Detect, prompt, and either wait or cancel before retrying.
  EXISTING_STATE=$(az deployment sub show --name "$CF_DEPLOY_NAME" \
    --query "properties.provisioningState" -o tsv 2>/dev/null || echo "NotFound")
  if [[ "$EXISTING_STATE" == "Running" || "$EXISTING_STATE" == "Accepted" ]]; then
    warn "Found in-flight deployment '$CF_DEPLOY_NAME' from a previous run."
    info "Waiting for it to complete (poll every 30s)..."
    while true; do
      sleep 30
      EXISTING_STATE=$(az deployment sub show --name "$CF_DEPLOY_NAME" \
        --query "properties.provisioningState" -o tsv 2>/dev/null || echo "NotFound")
      info "  state: $EXISTING_STATE"
      [[ "$EXISTING_STATE" =~ ^(Running|Accepted)$ ]] || break
    done
    if [[ "$EXISTING_STATE" == "Succeeded" ]]; then
      ok "Existing deployment succeeded; reusing it."
    fi
  fi

  if [[ "$EXISTING_STATE" == "Succeeded" ]]; then
    info "Deployment already Succeeded; skipping az deployment sub create."
  else
    az deployment sub create \
      --name "$CF_DEPLOY_NAME" \
      --location "$CF_REGION" \
      --template-file deploy/main.bicep \
      --parameters "@$PARAMS_FILE"
  fi

  CF_FQDN=$(az containerapp show \
    --resource-group "rg-cronfoundry-${CF_ENV}" \
    --name "cf-serve-${CF_ENV}" \
    --query properties.configuration.ingress.fqdn -o tsv 2>/dev/null || echo "")
  [[ -n "$CF_FQDN" ]] || die "Could not retrieve FQDN after deploy. Check rg-cronfoundry-${CF_ENV}.\nSee §13 of $GUIDE_URL"
fi
save CF_FQDN "$CF_FQDN"
ok "Deployed. FQDN: $CF_FQDN"

# ── Step 14: admin init ───────────────────────────────────────────────────────
header "[step 14/25] Initialize database"
CF_DB_URL="postgres://cfadmin:${CF_PG_PASSWORD}@cf-pg-${CF_ENV}.postgres.database.azure.com:5432/cronfoundry?sslmode=require"
if [[ "$DRY_RUN" == "true" ]]; then
  :
elif [[ "${CF_DB_INITIALIZED:-0}" == "1" && -x ./cronfoundry ]]; then
  ok "Database already initialized (CF_DB_INITIALIZED=1) and ./cronfoundry binary present; skipping firewall reopen, build, init, and restart"
else
  # Build the binary FIRST: a failed build dies before we open the firewall,
  # so a broken local toolchain doesn't leave Postgres exposed to 0.0.0.0/0.
  if [[ ! -x ./cronfoundry ]]; then
    if ! make build; then
      warn "make build failed; falling back to go build"
      go build -o cronfoundry ./cmd/cronfoundry \
        || die "Binary build failed. Ensure go >= 1.21 is installed.\nSee §14 of $GUIDE_URL"
    fi
  fi

  # WSL2-safe: use broad rule — WSL2 NAT may present a different source IP to Azure
  warn "Opening Postgres firewall to 0.0.0.0/0 for WSL2 NAT compatibility (will be tightened in step 15)."
  az postgres flexible-server firewall-rule create \
    --resource-group "rg-cronfoundry-${CF_ENV}" \
    --name "cf-pg-${CF_ENV}" \
    --rule-name AllowOperator \
    --start-ip-address "0.0.0.0" \
    --end-ip-address "255.255.255.255" \
    || warn "Firewall rule creation failed (may already exist) — database init may fail if connectivity is blocked."

  if [[ "${CF_DB_INITIALIZED:-0}" == "1" ]]; then
    ok "Database already initialized (CF_DB_INITIALIZED=1); skipping admin init + restart"
  else
    CRONFOUNDRY_DATABASE_URL="$CF_DB_URL" \
    CRONFOUNDRY_MASTER_KEY="$CF_MASTER_KEY" \
    ./cronfoundry admin init

    RESTART_TS=$(date +%s)
    az containerapp update \
      --resource-group "rg-cronfoundry-${CF_ENV}" \
      --name "cf-serve-${CF_ENV}" \
      --set-env-vars "RESTART_TRIGGER=${RESTART_TS}" \
      || die "Failed to trigger Container App restart.\nSee §14 of $GUIDE_URL"

    info "Waiting for Container App to become healthy..."
    HEALTH="unknown"
    # shellcheck disable=SC2034
    for i in $(seq 1 12); do
      HEALTH=$(az containerapp revision list \
        --resource-group "rg-cronfoundry-${CF_ENV}" \
        --name "cf-serve-${CF_ENV}" \
        --query '[?properties.trafficWeight>`0`].properties.healthState' \
        -o tsv 2>/dev/null | head -1 || echo "unknown")
      [[ "$HEALTH" == "Healthy" ]] && break
      sleep 10
    done
    if [[ "$HEALTH" != "Healthy" ]]; then
      warn "Container App did not become Healthy after 120 s (last state: $HEALTH)."
      warn "Note: until GitHub App creds are pushed (next steps) the serve binary will fail startup — this is expected."
      warn "Check: az containerapp logs show --resource-group rg-cronfoundry-${CF_ENV} --name cf-serve-${CF_ENV} --follow"
      warn "Continuing. See §14 of $GUIDE_URL"
    else
      ok "Container App health: $HEALTH"
    fi
    save CF_DB_INITIALIZED 1
  fi
fi

# ── Step 15: tighten Postgres firewall ───────────────────────────────────────
header "[step 15/25] Tighten Postgres firewall"
if [[ "$DRY_RUN" == "true" ]]; then
  :
elif [[ "${CF_FIREWALL_TIGHTENED:-0}" == "1" ]]; then
  ok "Postgres firewall already tightened (CF_FIREWALL_TIGHTENED=1); skipping"
else
  OPERATOR_IP=$(curl -fsSL https://api.ipify.org 2>/dev/null || echo "")
  if [[ -n "$OPERATOR_IP" ]]; then
    info "Narrowing Postgres firewall to operator IP: $OPERATOR_IP"
    az postgres flexible-server firewall-rule create \
      --resource-group "rg-cronfoundry-${CF_ENV}" \
      --name "cf-pg-${CF_ENV}" \
      --rule-name AllowOperatorIP \
      --start-ip-address "$OPERATOR_IP" \
      --end-ip-address "$OPERATOR_IP" \
      || warn "Could not create AllowOperatorIP rule."
    az postgres flexible-server firewall-rule delete \
      --resource-group "rg-cronfoundry-${CF_ENV}" \
      --name "cf-pg-${CF_ENV}" \
      --rule-name AllowOperator \
      --yes 2>/dev/null || true
    ok "Postgres firewall tightened to $OPERATOR_IP"
    save CF_FIREWALL_TIGHTENED 1
  else
    warn "Could not resolve operator IP via api.ipify.org; leaving broad firewall rule in place."
    warn "Tighten manually: az postgres flexible-server firewall-rule update ..."
  fi
fi

# ── Step 16: GitHub App ───────────────────────────────────────────────────────
header "[step 16/25] Register GitHub App (manifest flow)"
if [[ "$DRY_RUN" == "true" ]]; then
  warn "--dry-run: skipping GitHub App registration"
elif [[ -z "${CF_GITHUB_APP_ID:-}" ]]; then
  info "Manifest flow timeout: $MANIFEST_TIMEOUT (override with --manifest-timeout=DUR)."
  ./cronfoundry setup github-app \
    --state-file "$STATE_FILE" \
    --pem-dir "${HOME}/.cronfoundry" \
    --homepage-url "https://${CF_FQDN}" \
    --callback-url "https://${CF_FQDN}/oauth/callback" \
    --webhook-url "https://${CF_FQDN}/webhook/github" \
    --timeout "$MANIFEST_TIMEOUT" \
    || die "GitHub App manifest flow failed.\n  Your Azure resources are preserved. Just re-run this script to retry only this step:\n    bash scripts/quickstart-copilot.sh\n  Or use --manual for legacy paste prompts."
  state_load   # pick up CF_GITHUB_APP_ID, CF_GITHUB_CLIENT_ID, etc. that the command wrote
  ok "GitHub App registered: App ID ${CF_GITHUB_APP_ID}"
fi

# ── Step 17: Push GitHub App credentials to deployed container ───────────────
header "[step 17/25] Push GitHub App credentials to deployed container"
# KV_NAME, CA_NAME, RG were computed once just after CF_ENV was set.
if [[ "$DRY_RUN" == "true" ]]; then
  warn "--dry-run: skipping Container App credential push"
elif [[ "${CF_CREDS_PUSHED:-0}" == "1" ]]; then
  ok "Container App credentials already pushed (CF_CREDS_PUSHED=1); skipping"
else
  [[ -f "${CF_GITHUB_PEM_PATH:-}" ]] \
    || die "CF_GITHUB_PEM_PATH not set or file missing; was the manifest flow successful?"
  : "${CF_GITHUB_APP_ID:?CF_GITHUB_APP_ID not set; manifest flow may have failed}"
  : "${CF_GITHUB_CLIENT_ID:?CF_GITHUB_CLIENT_ID not set; manifest flow may have failed}"
  : "${CF_GITHUB_CLIENT_SECRET:?CF_GITHUB_CLIENT_SECRET not set; manifest flow may have failed}"
  : "${CF_GITHUB_WEBHOOK_SECRET:?CF_GITHUB_WEBHOOK_SECRET not set; manifest flow may have failed}"

  # Bicep grants the cf-serve managed identity Secrets Officer on the vault, but
  # the operator running this script does NOT inherit that. We need to grant
  # ourselves write access before any 'az keyvault secret set' can succeed.
  # (Step 19 below re-grants for the admin CLI block — that call is idempotent.)
  KV_ID=$(az keyvault show --name "$KV_NAME" --query id -o tsv) \
    || die "Could not resolve Key Vault $KV_NAME (does the deploy step run?)"
  resolve_operator_object_id
  info "Granting operator (${OPERATOR_OBJ_ID}) Secrets Officer on $KV_NAME..."
  az role assignment create \
    --role "Key Vault Secrets Officer" \
    --assignee-object-id "$OPERATOR_OBJ_ID" \
    --assignee-principal-type User \
    --scope "$KV_ID" \
    --output none 2>/dev/null \
    || info "Role assignment may already exist (continuing)"
  # RBAC propagation can lag a few seconds; poll for the actual permission
  # before attempting the write so we get a clear error message instead of a
  # 403 burn-through.
  RBAC_OK=0
  for i in {1..12}; do
    if az keyvault secret list --vault-name "$KV_NAME" --maxresults 1 --output none 2>/dev/null; then
      RBAC_OK=1
      break
    fi
    info "Waiting for RBAC propagation… (${i}/12)"
    sleep 5
  done
  if [[ "$RBAC_OK" != "1" ]]; then
    die "RBAC propagation timed out after ~60s for vault $KV_NAME.\n  Operator: $OPERATOR_OBJ_ID\n  Verify: az role assignment list --assignee \"$OPERATOR_OBJ_ID\" --scope \"$KV_ID\"\n  If propagation eventually catches up, re-run this script to resume from step 17."
  fi

  # 1. PEM into Key Vault (already wired as secretRef in Bicep)
  az keyvault secret set --vault-name "$KV_NAME" --name github-app-pem \
    --file "${CF_GITHUB_PEM_PATH}" --output none \
    || die "Failed to upload PEM to Key Vault $KV_NAME"

  # 2. OAuth client secret is a Container App secret (Bicep set it to empty at deploy)
  az containerapp secret set --resource-group "$RG" --name "$CA_NAME" \
    --secrets "oauth-client-secret=${CF_GITHUB_CLIENT_SECRET}" --output none \
    || die "Failed to update Container App OAuth secret"

  # 3. Plain env vars (App ID, OAuth client ID, webhook secret)
  az containerapp update --resource-group "$RG" --name "$CA_NAME" \
    --set-env-vars \
      "CRONFOUNDRY_GITHUB_APP_ID=${CF_GITHUB_APP_ID}" \
      "CRONFOUNDRY_GITHUB_OAUTH_CLIENT_ID=${CF_GITHUB_CLIENT_ID}" \
      "CRONFOUNDRY_GITHUB_WEBHOOK_SECRET=${CF_GITHUB_WEBHOOK_SECRET}" \
    --output none \
    || die "Failed to update Container App env vars"
  ok "Container App credentials applied; new revision rolling out"

  # 4. Wait for /healthz
  HEALTHY=0
  for i in {1..60}; do
    STATUS=$(curl -fsS -o /dev/null -w "%{http_code}" "https://${CF_FQDN}/healthz" 2>/dev/null || echo "000")
    if [[ "$STATUS" == "200" ]]; then ok "Serve healthy at https://${CF_FQDN}"; HEALTHY=1; break; fi
    sleep 5
  done
  [[ "$HEALTHY" == "1" ]] || warn "Serve did not become healthy in 5 minutes; check 'az containerapp logs show --name $CA_NAME --resource-group $RG'"
  save CF_CREDS_PUSHED 1
fi

# ── Step 18: Discover GitHub App installation ───────────────────────────────
header "[step 18/25] Discover GitHub App installation"
if [[ "$DRY_RUN" == "true" ]]; then
  warn "--dry-run: skipping GitHub App installation discovery"
elif [[ -z "${CF_INSTALLATION_ID:-}" ]]; then
  JWT=$(./cronfoundry setup mint-jwt --app-id "$CF_GITHUB_APP_ID" --pem "$CF_GITHUB_PEM_PATH") \
    || die "Failed to mint App JWT"

  fetch_installs() {
    curl -fsS \
      -H "Accept: application/vnd.github+json" \
      -H "Authorization: Bearer ${JWT}" \
      https://api.github.com/app/installations
  }
  INSTALL_JSON=$(fetch_installs) || die "Failed to list App installations"
  INSTALL_COUNT=$(echo "$INSTALL_JSON" | jq 'length')

  if [[ "$INSTALL_COUNT" -eq 0 ]]; then
    warn "No installations found yet."
    SLUG="${CF_GITHUB_APP_SLUG:-}"
    if [[ -n "$SLUG" ]]; then
      echo "Open this URL to install the App on your skill repo:"
      echo "  https://github.com/apps/${SLUG}/installations/new"
    else
      echo "Open the GitHub App's install page from the manifest-flow output."
    fi
    read -rp "Press Enter once installed..."
    INSTALL_JSON=$(fetch_installs) || die "Failed to list App installations after install"
    INSTALL_COUNT=$(echo "$INSTALL_JSON" | jq 'length')
  fi

  if [[ "$INSTALL_COUNT" -eq 1 ]]; then
    CF_INSTALLATION_ID=$(echo "$INSTALL_JSON" | jq '.[0].id')
  elif [[ "$INSTALL_COUNT" -gt 1 ]]; then
    echo "Multiple installations found:"
    echo "$INSTALL_JSON" | jq '.[] | {id, account: .account.login}'
    read -rp "Enter installation ID for ${CF_SKILL_REPO}: " CF_INSTALLATION_ID
  else
    die "Still no installations after retry"
  fi
  save CF_INSTALLATION_ID "$CF_INSTALLATION_ID"
fi
ok "Installation ID: ${CF_INSTALLATION_ID:-<dry-run>}"

# ── Step 19: Grant operator KV access ─────────────────────────────────────────
header "[step 19/25] Grant operator Key Vault access"
if [[ "$DRY_RUN" != "true" ]]; then
  KV_ID=$(az keyvault show --name "$KV_NAME" --query id -o tsv) \
    || die "Could not resolve Key Vault $KV_NAME"
  resolve_operator_object_id
  az role assignment create \
    --role "Key Vault Secrets Officer" \
    --assignee-object-id "$OPERATOR_OBJ_ID" \
    --assignee-principal-type User \
    --scope "$KV_ID" \
    --output none 2>/dev/null \
    || warn "Role assignment may already exist (continuing)"
  ok "Operator granted Secrets Officer on $KV_NAME"
fi

# Common env for admin CLI calls. SECRET_STORE=keyvault routes Put() to KV
# so the deployed Container App (which also reads via KV) sees these secrets.
ADMIN_ENV=(
  "CRONFOUNDRY_DATABASE_URL=$CF_DB_URL"
  "CRONFOUNDRY_MASTER_KEY=$CF_MASTER_KEY"
  "SECRET_STORE=keyvault"
  "AZURE_KEYVAULT_URL=https://${KV_NAME}.vault.azure.net/"
)

# ── Step 20: Connect skill repo ───────────────────────────────────────────────
header "[step 20/25] Connect skill repo via admin CLI"
if [[ "$DRY_RUN" != "true" ]]; then
  env "${ADMIN_ENV[@]}" \
    ./cronfoundry admin connect-repo "${CF_SKILL_REPO}" \
      --installation-id "${CF_INSTALLATION_ID}" \
      || die "connect-repo failed"
  ok "Repo connected: ${CF_SKILL_REPO}"
fi

# ── Step 21: Store webhook secret (no-op) ────────────────────────────────────
# The webhook secret is already in place: step 17 set it as the
# CRONFOUNDRY_GITHUB_WEBHOOK_SECRET env var on the Container App, which is
# what internal/webapi/server.go reads (Deps.WebhookSecret). There is no
# secrets-store consumer of this value. Earlier versions of this script
# called `admin set-secret github_webhook_secret` here, but that wrote
# to a Postgres key nothing reads, AND fails on Key Vault (KV doesn't
# allow underscores in secret names). Keeping the header for step-numbering
# stability but skipping the body.
header "[step 21/25] Webhook secret"
ok "Webhook secret already wired into Container App env (step 17). Skipping store."

# ── Step 22: Connect Copilot Enterprise ──────────────────────────────────────
header "[step 22/25] Connect Copilot Enterprise (device flow)"
if [[ "$DRY_RUN" != "true" ]]; then
  env "${ADMIN_ENV[@]}" \
    ./cronfoundry admin connect-copilot --prefix copilot \
      || die "connect-copilot failed"
fi

# ── Step 23: Push starter skill to skill repo ───────────────────────────────
header "[step 23/25] Push starter cronfoundry.yaml + smoke skill to ${CF_SKILL_REPO}"
if [[ "$DRY_RUN" == "true" ]]; then
  :
elif [[ "${CF_STARTER_PR_PUSHED:-0}" == "1" ]]; then
  ok "Starter PR already pushed (CF_STARTER_PR_PUSHED=1); skipping"
else
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  TPL_DIR="$SCRIPT_DIR/templates"

  DEFAULT_BRANCH=$(gh_retry gh api "repos/${CF_SKILL_REPO}" --jq .default_branch) \
    || die "Could not look up default branch of ${CF_SKILL_REPO}"

  if gh_retry gh api "repos/${CF_SKILL_REPO}/contents/cronfoundry.yaml?ref=${DEFAULT_BRANCH}" &>/dev/null; then
    ok "cronfoundry.yaml already exists on ${DEFAULT_BRANCH}; skipping starter push"
    save CF_STARTER_PR_PUSHED 1
  else
    TMP_YAML=$(mktemp)
    TMP_SKILL=$(mktemp)
    trap 'rm -f "$TMP_YAML" "$TMP_SKILL"' EXIT
    sed "s|__REPORTS_REPO__|${CF_REPORTS_REPO}|g" "$TPL_DIR/cronfoundry.yaml.tmpl" > "$TMP_YAML"
    cp "$TPL_DIR/smoke-skill.md.tmpl" "$TMP_SKILL"

    BASE_SHA=$(gh_retry gh api "repos/${CF_SKILL_REPO}/git/ref/heads/${DEFAULT_BRANCH}" --jq .object.sha) \
      || die "Could not resolve SHA for ${DEFAULT_BRANCH}"

    # Create branch (ignore failure if it already exists)
    gh_retry gh api -X POST "repos/${CF_SKILL_REPO}/git/refs" \
      -f ref="refs/heads/cronfoundry-quickstart" \
      -f sha="${BASE_SHA}" &>/dev/null || true

    # base64 -w0 is GNU-only; pipe through tr for macOS/BSD portability.
    YAML_B64=$(base64 < "$TMP_YAML" | tr -d '\n')
    SKILL_B64=$(base64 < "$TMP_SKILL" | tr -d '\n')

    # GitHub's PUT /contents/{path} requires the existing blob SHA when the
    # file is already present at the target ref — otherwise returns 422
    # 'sha wasn't supplied'. Look up SHAs first; pass them only when set so
    # initial creates still work.
    YAML_SHA=$(gh_retry gh api "repos/${CF_SKILL_REPO}/contents/cronfoundry.yaml?ref=cronfoundry-quickstart" \
      --jq .sha 2>/dev/null || echo "")
    SKILL_SHA=$(gh_retry gh api "repos/${CF_SKILL_REPO}/contents/skills/smoke/SKILL.md?ref=cronfoundry-quickstart" \
      --jq .sha 2>/dev/null || echo "")

    yaml_args=( -X PUT "repos/${CF_SKILL_REPO}/contents/cronfoundry.yaml"
                -f message="Add starter cronfoundry.yaml"
                -f branch="cronfoundry-quickstart"
                -f content="${YAML_B64}" )
    [[ -n "$YAML_SHA" ]] && yaml_args+=( -f sha="$YAML_SHA" )
    gh_retry gh api "${yaml_args[@]}" >/dev/null \
      || die "Failed to PUT cronfoundry.yaml"

    skill_args=( -X PUT "repos/${CF_SKILL_REPO}/contents/skills/smoke/SKILL.md"
                 -f message="Add starter smoke skill"
                 -f branch="cronfoundry-quickstart"
                 -f content="${SKILL_B64}" )
    [[ -n "$SKILL_SHA" ]] && skill_args+=( -f sha="$SKILL_SHA" )
    gh_retry gh api "${skill_args[@]}" >/dev/null \
      || die "Failed to PUT skills/smoke/SKILL.md"

    PR_URL=""
    EXISTING_PR=$(gh_retry gh pr list \
      --repo "${CF_SKILL_REPO}" \
      --head "cronfoundry-quickstart" \
      --state open \
      --json url --jq '.[0].url // ""' 2>/dev/null || echo "")
    if [[ -n "$EXISTING_PR" ]]; then
      PR_URL="$EXISTING_PR"
      ok "Using existing PR: $PR_URL"
    else
      PR_URL=$(gh_retry gh pr create \
        --repo "${CF_SKILL_REPO}" \
        --base "${DEFAULT_BRANCH}" \
        --head "cronfoundry-quickstart" \
        --title "CronFoundry quickstart: starter cronfoundry.yaml + smoke skill" \
        --body "Auto-generated by the CronFoundry quickstart installer. Merge to enable the daily smoke run.") \
        || die "gh pr create failed"
      ok "Opened PR: ${PR_URL}"
    fi

    rm -f "$TMP_YAML" "$TMP_SKILL"

    echo ""
    echo -e "  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo -e "  ${BOLD}Merge the starter PR to enable the smoke schedule:${RESET}"
    echo ""
    echo -e "    ${PR_URL}"
    echo ""
    echo "  After merging, the daily 09:00 UTC cron will fire on the"
    echo "  next tick. Press Enter here once you've merged (this just"
    echo "  unblocks step 24's first-run probe — it's safe to skip)."
    echo -e "  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
    echo ""
    read -rp "Press Enter once merged (or to continue regardless)... " _
    save CF_STARTER_PR_PUSHED 1
  fi
fi

# ── Step 24: Wait for first green run ─────────────────────────────────────────
header "[step 24/25] Waiting for first run"
if [[ "$DRY_RUN" != "true" ]]; then
  PROBE=$(curl -fsS -o /dev/null -w "%{http_code}" "https://${CF_FQDN}/api/runs?limit=1" 2>/dev/null || echo "000")
  if [[ "$PROBE" != "200" ]]; then
    warn "Auto-tail unavailable (HTTP $PROBE; /api/runs requires session auth)."
    echo "  Open: https://${CF_FQDN}/runs to watch the first run."
  else
    START=$(date +%s)
    DEADLINE=$((START + 900))
    SETTLED=0
    while [[ $(date +%s) -lt $DEADLINE ]]; do
      RUNS=$(curl -fsS "https://${CF_FQDN}/api/runs?limit=1" 2>/dev/null || echo '[]')
      STATUS=$(echo "$RUNS" | jq -r '.[0].status // empty')
      case "$STATUS" in
        succeeded)
          ELAPSED=$(($(date +%s) - START))
          ok "First run green in ${ELAPSED}s — open https://${CF_FQDN}/"
          SETTLED=1
          break
          ;;
        failed|partial_failure)
          RUN_ID=$(echo "$RUNS" | jq -r '.[0].id')
          die "First run status: ${STATUS}. Inspect at https://${CF_FQDN}/runs/${RUN_ID}"
          ;;
      esac
      sleep 10
    done
    if [[ "$SETTLED" != "1" ]]; then
      warn "First run did not complete in 15 minutes. Check https://${CF_FQDN}/"
    fi
  fi
fi

# ── Step 25: Final dashboard ──────────────────────────────────────────────────
header "[step 25/25] Final dashboard"
echo ""
echo "  Open: https://${CF_FQDN}/"
echo ""
echo "  Push a cronfoundry.yaml to your skill repo with:"
echo "    provider: copilot-enterprise"
echo "    copilot_prefix: copilot"
echo ""
echo "  Full guide: $GUIDE_URL"
echo ""

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  CronFoundry deployed successfully!"
echo "  URL:         https://${CF_FQDN}/"
echo "  State file:  $STATE_FILE"
echo "  Guide:       $GUIDE_URL"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
