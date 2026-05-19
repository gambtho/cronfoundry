#!/usr/bin/env bash
# CronFoundry — Fly.io quickstart.
#
# Usage:
#   scripts/fly-quickstart.sh [--image REF] [--fresh] [--non-interactive]
#
# Reads .env at repo root. Prompts for any missing required keys and
# persists them back. Idempotent — re-running rolls forward.
set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'

info()    { echo -e "${CYAN}[info]${RESET}  $*"; }
ok()      { echo -e "${GREEN}[ok]${RESET}    $*"; }
warn()    { echo -e "${YELLOW}[warn]${RESET}  $*"; }
die()     { echo -e "${RED}[error]${RESET} $*" >&2; exit 1; }
header()  { echo -e "\n${BOLD}$*${RESET}"; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
export ENV_FILE="${REPO_ROOT}/.env"

# shellcheck source=lib/dotenv.sh
source "${SCRIPT_DIR}/lib/dotenv.sh"
# shellcheck source=lib/fly.sh
source "${SCRIPT_DIR}/lib/fly.sh"
# shellcheck source=lib/state.sh
source "${SCRIPT_DIR}/lib/state.sh"

# ── arg parsing ─────────────────────────────────────────────────────────────
IMAGE_OVERRIDE=""
FRESH=0
export DOTENV_NON_INTERACTIVE=0
for arg in "$@"; do
  case "$arg" in
    --image=*)         IMAGE_OVERRIDE="${arg#*=}" ;;
    --image)           die "--image requires a value (use --image=REF)" ;;
    --fresh)           FRESH=1 ;;
    --non-interactive) DOTENV_NON_INTERACTIVE=1 ;;
    -h|--help)
      sed -n '2,7p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *) die "unknown argument: ${arg}" ;;
  esac
done

# ── preflight ───────────────────────────────────────────────────────────────
header "[1/9] Preflight"

command -v flyctl >/dev/null || die "flyctl not on PATH (https://fly.io/docs/flyctl/install/)"
command -v jq >/dev/null     || die "jq not on PATH"
command -v openssl >/dev/null || die "openssl not on PATH"
command -v curl >/dev/null   || die "curl not on PATH"

flyctl auth whoami >/dev/null 2>&1 || die "flyctl is not authenticated. Run: flyctl auth login"
ok "flyctl auth: $(flyctl auth whoami 2>/dev/null)"

if ! command -v gh >/dev/null; then
  warn "gh not on PATH — fly-smoke-assert.sh will not work, but provisioning will."
elif ! gh auth status >/dev/null 2>&1; then
  warn "gh is not authenticated — fly-smoke-assert.sh will not work."
fi

dotenv_load
ok "loaded .env from ${ENV_FILE}"

# ── resolve required values ─────────────────────────────────────────────────
header "[2/9] Resolve config from .env (prompt if missing)"

# Defaults — operator can override by setting them in .env or the shell.
FLY_API_APP=$(dotenv_require    FLY_API_APP    "Fly api app name"    "cronfoundry-api")
FLY_RUNNER_APP=$(dotenv_require FLY_RUNNER_APP "Fly runner app name" "cronfoundry-runner")
FLY_REGION=$(dotenv_require     FLY_REGION     "Fly region"          "iad")

fly_validate_app_name "$FLY_API_APP"    || die "FLY_API_APP '${FLY_API_APP}' is not a valid fly app name"
fly_validate_app_name "$FLY_RUNNER_APP" || die "FLY_RUNNER_APP '${FLY_RUNNER_APP}' is not a valid fly app name"

# IMAGE: --image flag wins; else .env; else default.
if [[ -n "$IMAGE_OVERRIDE" ]]; then
  IMAGE="$IMAGE_OVERRIDE"
  info "using --image override: ${IMAGE}"
else
  IMAGE=$(dotenv_require IMAGE "Container image" "ghcr.io/gambtho/cronfoundry:latest")
fi
fly_warn_if_runner_image "$IMAGE" || true

# Admin logins — required, no defaults. Asked before the App-registration
# step because the manifest flow doesn't need it and we want to fail fast
# under --non-interactive when it's missing.
CRONFOUNDRY_ADMIN_LOGINS=$(dotenv_require                  CRONFOUNDRY_ADMIN_LOGINS                  "Comma-separated admin GitHub logins")

# GitHub App / OAuth — register via manifest flow on first run (mirrors
# Azure's quickstart-copilot.sh step 16). After the flow completes,
# `cronfoundry setup github-app` writes its outputs into STATE_FILE
# under CF_GITHUB_* names; we copy them into the CRONFOUNDRY_GITHUB_*
# names that the deployed serve container actually reads.
STATE_FILE="${HOME}/.cronfoundry-quickstart-state-fly"
export STATE_FILE
state_init
state_load

if ! dotenv_has CRONFOUNDRY_GITHUB_APP_ID; then
  header "[2.5/9] Register GitHub App (manifest flow)"
  if [[ "${DOTENV_NON_INTERACTIVE:-0}" == "1" ]] && [[ -z "${CF_GITHUB_APP_ID:-}" ]]; then
    die "CRONFOUNDRY_GITHUB_APP_ID missing under --non-interactive. Run \
fly-quickstart.sh interactively once to register the GitHub App, or \
populate CRONFOUNDRY_GITHUB_APP_ID / _OAUTH_CLIENT_ID / _OAUTH_CLIENT_SECRET / \
_WEBHOOK_SECRET / _APP_PEM_PATH in .env from an out-of-band source."
  fi

  # Build the binary if needed — the manifest flow uses `cronfoundry setup github-app`.
  if [[ ! -x "${REPO_ROOT}/cronfoundry" ]]; then
    info "building cronfoundry binary for setup CLI"
    ( cd "$REPO_ROOT" && { make build || go build -o cronfoundry ./cmd/cronfoundry; } ) \
      || die "binary build failed; ensure go >= 1.21 is installed"
  fi

  if [[ -z "${CF_GITHUB_APP_ID:-}" ]]; then
    FQDN="${FLY_API_APP}.fly.dev"
    "${REPO_ROOT}/cronfoundry" setup github-app \
      --state-file "$STATE_FILE" \
      --pem-dir "${HOME}/.cronfoundry" \
      --homepage-url "https://${FQDN}" \
      --callback-url "https://${FQDN}/oauth/callback" \
      --webhook-url "https://${FQDN}/webhook/github" \
      || die "GitHub App manifest flow failed. Re-run scripts/fly-quickstart.sh to retry."
    state_load
    [[ -n "${CF_GITHUB_APP_ID:-}" ]] || die "manifest flow returned but CF_GITHUB_APP_ID missing in $STATE_FILE"
  fi

  dotenv_set CRONFOUNDRY_GITHUB_APP_ID              "${CF_GITHUB_APP_ID}"
  dotenv_set CRONFOUNDRY_GITHUB_APP_PEM_PATH        "${CF_GITHUB_PEM_PATH}"
  dotenv_set CRONFOUNDRY_GITHUB_OAUTH_CLIENT_ID     "${CF_GITHUB_CLIENT_ID}"
  dotenv_set CRONFOUNDRY_GITHUB_OAUTH_CLIENT_SECRET "${CF_GITHUB_CLIENT_SECRET}"
  dotenv_set CRONFOUNDRY_GITHUB_WEBHOOK_SECRET      "${CF_GITHUB_WEBHOOK_SECRET}"
  ok "GitHub App registered: App ID ${CF_GITHUB_APP_ID} (credentials persisted to .env)"
fi

CRONFOUNDRY_GITHUB_APP_ID=$(dotenv_require                 CRONFOUNDRY_GITHUB_APP_ID                 "GitHub App ID")
CRONFOUNDRY_GITHUB_APP_PEM_PATH=$(dotenv_require           CRONFOUNDRY_GITHUB_APP_PEM_PATH           "Path to GitHub App PEM")
[[ -r "$CRONFOUNDRY_GITHUB_APP_PEM_PATH" ]] \
  || die "GitHub App PEM not readable at: ${CRONFOUNDRY_GITHUB_APP_PEM_PATH}"
CRONFOUNDRY_GITHUB_OAUTH_CLIENT_ID=$(dotenv_require        CRONFOUNDRY_GITHUB_OAUTH_CLIENT_ID        "GitHub OAuth client ID")
CRONFOUNDRY_GITHUB_OAUTH_CLIENT_SECRET=$(dotenv_require    CRONFOUNDRY_GITHUB_OAUTH_CLIENT_SECRET    "GitHub OAuth client secret" "" --secret)
CRONFOUNDRY_GITHUB_WEBHOOK_SECRET=$(dotenv_require         CRONFOUNDRY_GITHUB_WEBHOOK_SECRET         "GitHub webhook secret"      "" --secret)

# Generated keys — auto-generate if absent and persist back.
if ! dotenv_has CRONFOUNDRY_MASTER_KEY; then
  info "generating CRONFOUNDRY_MASTER_KEY (32 bytes hex)"
  dotenv_set CRONFOUNDRY_MASTER_KEY "$(openssl rand -hex 32)"
fi
if ! dotenv_has CRONFOUNDRY_RUNNER_API_KEY; then
  info "generating CRONFOUNDRY_RUNNER_API_KEY (32 bytes hex)"
  dotenv_set CRONFOUNDRY_RUNNER_API_KEY "$(openssl rand -hex 32)"
fi

ok "config resolved: api=${FLY_API_APP} runner=${FLY_RUNNER_APP} region=${FLY_REGION} image=${IMAGE}"

# ── --fresh teardown ────────────────────────────────────────────────────────
if (( FRESH )); then
  header "[3/9] --fresh: destroying existing apps + Postgres"
  warn "this is irreversible — Postgres data will be lost"
  if [[ "${DOTENV_NON_INTERACTIVE:-0}" != "1" ]]; then
    read -rp "type 'destroy' to continue: " confirm
    [[ "$confirm" == "destroy" ]] || die "aborted"
  fi
  fly_app_destroy "$FLY_API_APP"
  fly_app_destroy "$FLY_RUNNER_APP"
  fly_pg_destroy "cronfoundry-db"
  ok "--fresh teardown complete"
else
  header "[3/9] (skipping --fresh teardown)"
fi

# ── apps ────────────────────────────────────────────────────────────────────
header "[4/9] Apps"
fly_app_create "$FLY_API_APP"
fly_app_create "$FLY_RUNNER_APP"

# ── Postgres ────────────────────────────────────────────────────────────────
header "[5/9] Postgres"
fly_pg_create "cronfoundry-db" "$FLY_REGION"
fly_pg_attach "cronfoundry-db" "$FLY_API_APP"

# ── secrets ─────────────────────────────────────────────────────────────────
header "[6/9] Secrets"

# Read PEM contents for the secret payload (flyctl wants the value, not a path).
PEM_CONTENTS=$(cat "$CRONFOUNDRY_GITHUB_APP_PEM_PATH")

fly_secrets_set_batch "$FLY_API_APP" \
  "CRONFOUNDRY_MASTER_KEY=${CRONFOUNDRY_MASTER_KEY}" \
  "CRONFOUNDRY_RUNNER_API_KEY=${CRONFOUNDRY_RUNNER_API_KEY}" \
  "CRONFOUNDRY_GITHUB_APP_ID=${CRONFOUNDRY_GITHUB_APP_ID}" \
  "CRONFOUNDRY_GITHUB_APP_PEM=${PEM_CONTENTS}" \
  "CRONFOUNDRY_GITHUB_OAUTH_CLIENT_ID=${CRONFOUNDRY_GITHUB_OAUTH_CLIENT_ID}" \
  "CRONFOUNDRY_GITHUB_OAUTH_CLIENT_SECRET=${CRONFOUNDRY_GITHUB_OAUTH_CLIENT_SECRET}" \
  "CRONFOUNDRY_GITHUB_WEBHOOK_SECRET=${CRONFOUNDRY_GITHUB_WEBHOOK_SECRET}" \
  "CRONFOUNDRY_ADMIN_LOGINS=${CRONFOUNDRY_ADMIN_LOGINS}" \
  "FLY_RUNNER_APP=${FLY_RUNNER_APP}" \
  "FLY_RUNNER_IMAGE=${IMAGE}" \
  "FLY_API_TOKEN=$(flyctl auth token)"

# Runner side gets only the shared API key — that's how it auths back to api.
fly_secrets_set_batch "$FLY_RUNNER_APP" \
  "CRONFOUNDRY_RUNNER_API_KEY=${CRONFOUNDRY_RUNNER_API_KEY}"

# ── deploy ──────────────────────────────────────────────────────────────────
header "[7/9] Deploy"
fly_deploy "${REPO_ROOT}/deploy/fly/fly.api.toml"    "$FLY_API_APP"    "$IMAGE"
fly_deploy "${REPO_ROOT}/deploy/fly/fly.runner.toml" "$FLY_RUNNER_APP" "$IMAGE" --no-ha

# ── healthcheck ─────────────────────────────────────────────────────────────
header "[8/9] Healthcheck"
fly_healthcheck "${FLY_API_APP}.fly.dev" 120

# ── starter PR ──────────────────────────────────────────────────────────────
header "[9/9] Starter PR (first run only)"

CRONFOUNDRY_SKILLS_REPO=$(dotenv_require CRONFOUNDRY_SKILLS_REPO \
  "Skills repo (owner/name) where CronFoundry will read schedules and write back" "")

if ! command -v gh >/dev/null; then
  warn "gh not installed — skipping starter PR. Manually copy:"
  warn "  scripts/templates/fly-cronfoundry.yaml -> ${CRONFOUNDRY_SKILLS_REPO}:cronfoundry.yaml"
  warn "  scripts/templates/fly-smoke-skill.md   -> ${CRONFOUNDRY_SKILLS_REPO}:skills/smoke.md"
elif gh api "repos/${CRONFOUNDRY_SKILLS_REPO}/contents/cronfoundry.yaml" >/dev/null 2>&1; then
  ok "${CRONFOUNDRY_SKILLS_REPO}:cronfoundry.yaml already exists — skipping starter PR"
else
  info "opening starter PR against ${CRONFOUNDRY_SKILLS_REPO}"
  TMP=$(mktemp -d)
  trap 'rm -rf "$TMP"' EXIT

  pushd "$TMP" >/dev/null
  gh repo clone "$CRONFOUNDRY_SKILLS_REPO" repo -- --depth=1
  cd repo
  BRANCH="cronfoundry/quickstart-$(date +%s)"
  git checkout -b "$BRANCH"
  cp "${REPO_ROOT}/scripts/templates/fly-cronfoundry.yaml" cronfoundry.yaml
  mkdir -p skills
  cp "${REPO_ROOT}/scripts/templates/fly-smoke-skill.md" skills/smoke.md
  git add cronfoundry.yaml skills/smoke.md
  git -c user.email='cronfoundry-quickstart@example.invalid' \
      -c user.name='cronfoundry quickstart' \
      commit -m "Add starter cronfoundry.yaml + smoke skill"
  git push -u origin "$BRANCH"
  PR_URL=$(gh pr create \
    --title "CronFoundry quickstart: starter cronfoundry.yaml + smoke skill" \
    --body "Seeded by scripts/fly-quickstart.sh. Merge to enable the daily smoke run.")
  popd >/dev/null

  ok "opened PR: ${PR_URL}"
  if [[ "${DOTENV_NON_INTERACTIVE:-0}" != "1" ]]; then
    echo
    echo "  Merge the starter PR to enable the smoke schedule."
    read -rp "Press Enter once merged (or to continue regardless)... " _
  fi
fi

# ── final report ────────────────────────────────────────────────────────────
echo
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  CronFoundry deployed on Fly.io"
echo "  URL:   https://${FLY_API_APP}.fly.dev/"
echo "  Image: ${IMAGE}"
echo "  Next:  scripts/fly-smoke-assert.sh"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
