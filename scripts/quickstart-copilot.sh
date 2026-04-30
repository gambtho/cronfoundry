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
for arg in "$@"; do [[ "$arg" == "--dry-run" ]] && DRY_RUN=true; done

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/state.sh
source "${SCRIPT_DIR}/lib/state.sh"
# shellcheck source=lib/steps.sh
source "${SCRIPT_DIR}/lib/steps.sh"
state_init
state_load
save() { state_save "$1" "$2"; }   # legacy alias used elsewhere in script

GUIDE_URL="https://gambtho.github.io/cronfoundry/guides/quickstart-copilot.html"

# ── Step 1: prereq check ─────────────────────────────────────────────────────
header "[step 1/17] Checking prerequisites"

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
check_cmd go      "Install: https://golang.org/dl/ (need >= 1.21)"
check_cmd gh "Install: https://cli.github.com/"
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

# bicep check (need >= 0.26)
if ! az bicep version &>/dev/null; then
  info "Bicep not found -- installing via az bicep install..."
  az bicep install || die "Failed to install Bicep. Check internet connectivity.\nSee §1 of $GUIDE_URL"
fi
BICEP_VER=$(az bicep version 2>/dev/null | grep -Eo '[0-9]+\.[0-9]+' | head -1 || echo "0.0")
BICEP_MINOR=$(echo "$BICEP_VER" | cut -d. -f2)
if [[ "${BICEP_MINOR:-0}" -lt 26 ]]; then
  warn "Bicep $BICEP_VER found; recommend >= 0.26. Run: az bicep upgrade"
fi
ok "bicep $BICEP_VER"

ok "All prerequisites satisfied"

# ── Step 2: az login ──────────────────────────────────────────────────────────
header "[step 2/17] Azure login"
if az account show &>/dev/null; then
  CURRENT_ACCOUNT=$(az account show --query '[name, id]' -o tsv | tr '\t' ' / ')
  ok "Already logged in: $CURRENT_ACCOUNT"
else
  info "Running az login..."
  az login
fi

# ── Step 3: subscription ──────────────────────────────────────────────────────
header "[step 3/17] Select subscription"
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
header "[step 4/17] Verify cronfoundry clone"
if [[ ! -f "deploy/main.bicep" ]]; then
  die "Run this script from inside a cronfoundry clone.\n  git clone https://github.com/gambtho/cronfoundry && cd cronfoundry\nSee §4 of $GUIDE_URL"
fi
REPO_ROOT=$(git rev-parse --show-toplevel)
ok "Repo root: $REPO_ROOT"

# ── Step 5: GitHub App ────────────────────────────────────────────────────────
header "[step 5/17] GitHub App setup"
echo ""
echo "  CronFoundry uses a GitHub App (not an OAuth App) for repo access."
echo "  You need to create one if you haven't already."
echo ""
echo "  1. Open: https://github.com/settings/apps/new"
echo "     (Check the URL ends in /settings/apps/new -- not /applications/new)"
echo "  2. Name: anything globally unique, e.g. cronfoundry-$(whoami)"
echo "  3. Homepage URL: https://example.com  (placeholder -- you'll update after deploy)"
echo "  4. Callback URL: https://example.com/oauth/callback"
echo "  5. Webhook URL: https://example.com/webhook/github"
echo "     Webhook secret: generate with: openssl rand -hex 32"
echo "  6. Permissions -> Repository: Contents (R+W), Issues (W), Metadata (R)"
echo "     Account: Email (R)"
echo "  7. Subscribe to events: Push"
echo "  8. Save, then note the App ID, generate a Client Secret, download the .pem"
echo "  9. Install App on your skill repo and reports repo"
echo ""

if [[ -z "${CF_GITHUB_APP_ID:-}" ]]; then
  read -rp "GitHub App ID (numeric): " CF_GITHUB_APP_ID
  save CF_GITHUB_APP_ID "$CF_GITHUB_APP_ID"
fi
if [[ -z "${CF_GITHUB_CLIENT_ID:-}" ]]; then
  read -rp "GitHub App Client ID (starts with Iv23li): " CF_GITHUB_CLIENT_ID
  save CF_GITHUB_CLIENT_ID "$CF_GITHUB_CLIENT_ID"
fi
if [[ -z "${CF_GITHUB_CLIENT_SECRET:-}" ]]; then
  read -rsp "GitHub App Client Secret: " CF_GITHUB_CLIENT_SECRET; echo
  save CF_GITHUB_CLIENT_SECRET "$CF_GITHUB_CLIENT_SECRET"
  warn "State file $STATE_FILE contains sensitive credentials — treat it like .env and do not commit it."
fi
if [[ -z "${CF_GITHUB_PEM_PATH:-}" ]]; then
  read -rp "Path to GitHub App .pem file: " CF_GITHUB_PEM_PATH
  [[ -f "$CF_GITHUB_PEM_PATH" ]] || die "File not found: $CF_GITHUB_PEM_PATH"
  save CF_GITHUB_PEM_PATH "$CF_GITHUB_PEM_PATH"
fi
ok "GitHub App credentials collected"

# ── Step 6: skill repo ────────────────────────────────────────────────────────
header "[step 6/17] Skill repo"
if [[ -z "${CF_SKILL_REPO:-}" ]]; then
  read -rp "Skill repo (owner/repo, e.g. acme/cronfoundry-skills): " CF_SKILL_REPO
  save CF_SKILL_REPO "$CF_SKILL_REPO"
fi
if [[ -z "${CF_INSTALLATION_ID:-}" ]]; then
  read -rp "GitHub App Installation ID (number from the install URL): " CF_INSTALLATION_ID
  save CF_INSTALLATION_ID "$CF_INSTALLATION_ID"
fi
ok "Skill repo: $CF_SKILL_REPO (installation $CF_INSTALLATION_ID)"

# ── Step 7: reports repo ──────────────────────────────────────────────────────
header "[step 7/17] Reports repo"
if [[ -z "${CF_REPORTS_REPO:-}" ]]; then
  read -rp "Reports repo (owner/repo, e.g. acme/cronfoundry-reports): " CF_REPORTS_REPO
  save CF_REPORTS_REPO "$CF_REPORTS_REPO"
fi
ok "Reports repo: $CF_REPORTS_REPO"

# ── Step 8: master key ────────────────────────────────────────────────────────
header "[step 8/17] Generate master key"
if [[ -z "${CF_MASTER_KEY:-}" ]]; then
  CF_MASTER_KEY=$(openssl rand -base64 32)
  save CF_MASTER_KEY "$CF_MASTER_KEY"
  warn "SAVE THIS KEY -- if lost, encrypted secrets are unrecoverable."
  echo "  Master key: $CF_MASTER_KEY"
fi
ok "Master key ready"

# ── Step 9: env suffix ────────────────────────────────────────────────────────
header "[step 9/17] Environment suffix"
if [[ -z "${CF_ENV:-}" ]]; then
  while true; do
    read -rp "Env suffix (<=10 chars, lowercase/numbers/hyphens, default: copilot1): " CF_ENV
    CF_ENV="${CF_ENV:-copilot1}"
    if [[ "$CF_ENV" =~ ^[a-z0-9-]{1,10}$ ]]; then
      break
    fi
    warn "Invalid suffix '$CF_ENV'. Use only lowercase letters, numbers, and hyphens, max 10 chars."
  done
  save CF_ENV "$CF_ENV"
fi
warn "Key Vault soft-delete retains the name 'cf-kv-${CF_ENV}' for 7 days after teardown."
warn "Re-runs after teardown need a new suffix (e.g. copilot2)."
ok "Env: $CF_ENV"

# ── Step 10: region ───────────────────────────────────────────────────────────
header "[step 10/17] Region"
if [[ -z "${CF_REGION:-}" ]]; then
  read -rp "Azure region (default: swedencentral): " CF_REGION
  CF_REGION="${CF_REGION:-swedencentral}"
  save CF_REGION "$CF_REGION"
fi
info "Note: Postgres Flexible Server offer restrictions vary by subscription."
info "swedencentral is known-good for Microsoft-internal subs. See §10 of $GUIDE_URL"
ok "Region: $CF_REGION"

# ── Step 11: image tag ────────────────────────────────────────────────────────
header "[step 11/17] Image tag"
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

# ── Step 12: postgres password ────────────────────────────────────────────────
header "[step 12/17] Generate Postgres password"
if [[ -z "${CF_PG_PASSWORD:-}" ]]; then
  CF_PG_PASSWORD=$(openssl rand -base64 48 | tr -dc 'A-Za-z0-9' | head -c24)
  save CF_PG_PASSWORD "$CF_PG_PASSWORD"
fi
ok "Postgres password generated (saved to state file)"

# ── Step 13: build params file ────────────────────────────────────────────────
header "[step 13/17] Build params file"
PARAMS_FILE="deploy/params.quickstart-${CF_ENV}.json"
ADMIN_LOGIN=$(git config user.name 2>/dev/null) || {
  warn "git config user.name is not set; using 'admin' as adminLogins value."
  warn "You may want to set it: git config --global user.name 'Your GitHub login'"
  ADMIN_LOGIN="admin"
}
PARAMS_TMP="${PARAMS_FILE}.tmp.$$"
python3 - "$CF_GITHUB_PEM_PATH" "$PARAMS_TMP" \
    "$CF_ENV" "$CF_REGION" "$CF_IMAGE_TAG" \
    "$CF_GITHUB_APP_ID" "$CF_GITHUB_CLIENT_ID" "$CF_GITHUB_CLIENT_SECRET" \
    "$CF_PG_PASSWORD" "$CF_MASTER_KEY" "$ADMIN_LOGIN" << 'PYEOF'
import json, sys
pem_path, out_path, env, region, tag, app_id, client_id, client_secret, pg_pw, master_key, admin = sys.argv[1:]
with open(pem_path) as pf:
    pem = pf.read()
params = {
  "$schema": "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
  "contentVersion": "1.0.0.0",
  "parameters": {
    "env":                        {"value": env},
    "location":                   {"value": region},
    "imageTag":                   {"value": tag},
    "githubAppId":                {"value": app_id},
    "githubAppOAuthClientId":     {"value": client_id},
    "githubAppOAuthClientSecret": {"value": client_secret},
    "postgresAdminPassword":      {"value": pg_pw},
    "masterKey":                  {"value": master_key},
    "adminLogins":                {"value": admin},
    "viewerLogins":               {"value": ""},
    "ingressExternal":            {"value": True},
    "githubAppPem":               {"value": pem},
  }
}
with open(out_path, "w") as f:
    json.dump(params, f, indent=2)
print(f"Wrote {out_path}")
PYEOF
mv "$PARAMS_TMP" "$PARAMS_FILE"
chmod 600 "$PARAMS_FILE"
ok "Params file: $PARAMS_FILE"

# ── Step 14: deploy ───────────────────────────────────────────────────────────
header "[step 14/17] Deploy to Azure (~10 min)"
if [[ "$DRY_RUN" == "true" ]]; then
  warn "--dry-run: skipping deploy; steps 16–17 will use a placeholder FQDN"
  CF_FQDN="<not-yet-deployed>"
else
  az deployment sub create \
    --location "$CF_REGION" \
    --template-file deploy/main.bicep \
    --parameters "@$PARAMS_FILE"
  CF_FQDN=$(az containerapp show \
    --resource-group "rg-cronfoundry-${CF_ENV}" \
    --name "cf-serve-${CF_ENV}" \
    --query properties.configuration.ingress.fqdn -o tsv 2>/dev/null || echo "")
  [[ -n "$CF_FQDN" ]] || die "Could not retrieve FQDN after deploy. Check rg-cronfoundry-${CF_ENV}.\nSee §14 of $GUIDE_URL"
fi
save CF_FQDN "$CF_FQDN"
ok "Deployed. FQDN: $CF_FQDN"

# ── Step 15: admin init ───────────────────────────────────────────────────────
header "[step 15/17] Initialize database"
if [[ "$DRY_RUN" != "true" ]]; then
  # WSL2-safe: use broad rule -- WSL2 NAT may present a different source IP to Azure
  warn "Opening Postgres firewall to 0.0.0.0/0 for WSL2 NAT compatibility."
  warn "Tighten this after setup: az postgres flexible-server firewall-rule update --rule-name AllowOperator ..."
  az postgres flexible-server firewall-rule create \
    --resource-group "rg-cronfoundry-${CF_ENV}" \
    --name "cf-pg-${CF_ENV}" \
    --rule-name AllowOperator \
    --start-ip-address "0.0.0.0" \
    --end-ip-address "255.255.255.255" \
    || warn "Firewall rule creation failed (may already exist) — database init may fail if connectivity is blocked."

  CF_DB_URL="postgres://cfadmin:${CF_PG_PASSWORD}@cf-pg-${CF_ENV}.postgres.database.azure.com:5432/cronfoundry?sslmode=require"

  if ! make build; then
    warn "make build failed; falling back to go build"
    go build -o cronfoundry ./cmd/cronfoundry \
      || die "Binary build failed. Ensure go >= 1.21 is installed.\nSee §15 of $GUIDE_URL"
  fi

  CRONFOUNDRY_DATABASE_URL="$CF_DB_URL" \
  CRONFOUNDRY_MASTER_KEY="$CF_MASTER_KEY" \
  ./cronfoundry admin init

  RESTART_TS=$(date +%s)
  az containerapp update \
    --resource-group "rg-cronfoundry-${CF_ENV}" \
    --name "cf-serve-${CF_ENV}" \
    --set-env-vars "RESTART_TRIGGER=${RESTART_TS}" \
    || die "Failed to trigger Container App restart.\nSee §15 of $GUIDE_URL"

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
    warn "Check: az containerapp logs show --resource-group rg-cronfoundry-${CF_ENV} --name cf-serve-${CF_ENV} --follow"
    warn "Continuing, but the app may not be ready. See §15 of $GUIDE_URL"
  else
    ok "Container App health: $HEALTH"
  fi
fi

# ── Step 16: update GitHub App URLs ──────────────────────────────────────────
header "[step 16/17] Update GitHub App URLs"
echo ""
echo "  Go to your GitHub App settings and update these three URLs:"
echo ""
echo "  Homepage URL:  https://${CF_FQDN}"
echo "  Callback URL:  https://${CF_FQDN}/oauth/callback"
echo "  Webhook URL:   https://${CF_FQDN}/webhook/github"
echo ""
read -rp "Press Enter once you have updated the GitHub App URLs..."

# ── Step 17: UI checklist ─────────────────────────────────────────────────────
header "[step 17/17] Complete setup in the web UI"
echo ""
echo "  Open: https://${CF_FQDN}/"
echo ""
echo "  a) Log in via GitHub"
echo "  b) Providers -> GitHub Copilot Enterprise -> Connect"
echo "     Enter a prefix (e.g. 'copilot'), open the verification URL,"
echo "     enter the code shown, and authorize in your browser."
echo "  c) Repos -> Connect repo -> paste '${CF_SKILL_REPO}' and installation ID '${CF_INSTALLATION_ID}'"
echo "  d) Secrets -> Add 'github_webhook_secret' (the value from your GitHub App webhook config)"
echo "  e) Push a cronfoundry.yaml to your skill repo using:"
echo "       provider: copilot-enterprise"
echo "       copilot_prefix: <prefix from step b>"
echo ""
echo "  Full guide: $GUIDE_URL"
echo ""

echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  CronFoundry deployed successfully!"
echo "  URL:         https://${CF_FQDN}/"
echo "  State file:  $STATE_FILE"
echo "  Guide:       $GUIDE_URL"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
