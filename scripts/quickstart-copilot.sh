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
