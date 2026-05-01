#!/usr/bin/env bash
# scripts/quickstart-down.sh
# Per-env teardown for cronfoundry quickstart.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/state.sh
source "${SCRIPT_DIR}/lib/state.sh"

ENV_SUFFIX="${1:-}"
[[ -z "$ENV_SUFFIX" ]] && { echo "usage: $0 <env-suffix>"; exit 1; }

# state.sh helpers read STATE_FILE from the environment.
STATE_FILE="$(state_path_for "$ENV_SUFFIX")"
export STATE_FILE
# state_load tolerates a missing file, so no init required.
state_load

# 1. Delete resource group (no-wait)
echo "Deleting resource group rg-cronfoundry-${ENV_SUFFIX}..."
az group delete --name "rg-cronfoundry-${ENV_SUFFIX}" --yes --no-wait || true

# 2. Revoke GitHub App installation
if [[ -n "${CF_GITHUB_APP_ID:-}" && -n "${CF_INSTALLATION_ID:-}" && -n "${CF_GITHUB_PEM_PATH:-}" ]]; then
  echo "Revoking GitHub App installation ${CF_INSTALLATION_ID}..."
  CF_BIN="${SCRIPT_DIR}/../cronfoundry"
  if [[ ! -x "$CF_BIN" ]]; then
    CF_BIN="$(cd "${SCRIPT_DIR}/.." && pwd)/cronfoundry"
  fi
  if [[ -x "$CF_BIN" ]]; then
    JWT=$("$CF_BIN" setup mint-jwt --app-id "$CF_GITHUB_APP_ID" --pem "$CF_GITHUB_PEM_PATH") || true
    if [[ -n "${JWT:-}" ]]; then
      curl -fsS -X DELETE \
        -H "Accept: application/vnd.github+json" \
        -H "Authorization: Bearer ${JWT}" \
        "https://api.github.com/app/installations/${CF_INSTALLATION_ID}" || true
    fi
  else
    echo "cronfoundry binary not found; skipping App install revoke."
  fi
fi

# 3. Remove state file
state_clear

echo "Done. The GitHub App registration itself remains; delete it manually if desired:"
echo "  https://github.com/settings/apps/${CF_GITHUB_APP_SLUG:-<your-app-name>}"
