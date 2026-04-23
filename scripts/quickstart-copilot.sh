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

STATE_FILE="${HOME}/.cronfoundry-quickstart-state"
[[ -f "$STATE_FILE" ]] && source "$STATE_FILE"

save() { echo "$1=\"$2\"" >> "$STATE_FILE"; }

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
  az bicep install
fi
BICEP_VER=$(az bicep version 2>/dev/null | grep -oP '\d+\.\d+' | head -1 || echo "0.0")
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
  az account set --subscription "$CF_SUBSCRIPTION_ID"
  save CF_SUBSCRIPTION_ID "$CF_SUBSCRIPTION_ID"
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
  read -rp "Env suffix (<=10 chars, default: copilot1): " CF_ENV
  CF_ENV="${CF_ENV:-copilot1}"
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
    | python3 -c "import sys,json; print(json.load(sys.stdin)['tag_name'].lstrip('v'))" 2>/dev/null || echo "latest")
  save CF_IMAGE_TAG "$CF_IMAGE_TAG"
fi
ok "Image tag: $CF_IMAGE_TAG"

# ── Step 12: postgres password ────────────────────────────────────────────────
header "[step 12/17] Generate Postgres password"
if [[ -z "${CF_PG_PASSWORD:-}" ]]; then
  CF_PG_PASSWORD=$(openssl rand -base64 18 | tr -dc 'A-Za-z0-9' | head -c24)
  save CF_PG_PASSWORD "$CF_PG_PASSWORD"
fi
ok "Postgres password generated (saved to state file)"
