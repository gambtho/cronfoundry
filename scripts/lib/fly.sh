# scripts/lib/fly.sh
# Helpers around `flyctl` for fly-quickstart.sh.
#
# This file mixes pure helpers (validation, parsing) with side-effectful
# wrappers around flyctl. The pure helpers are unit-tested via fly_test.sh;
# the flyctl wrappers are exercised end-to-end via fly-quickstart.sh runs.

: "${RED:=$'\033[0;31m'}"
: "${GREEN:=$'\033[0;32m'}"
: "${YELLOW:=$'\033[1;33m'}"
: "${CYAN:=$'\033[0;36m'}"
: "${BOLD:=$'\033[1m'}"
: "${RESET:=$'\033[0m'}"

# Fly app-name rules: 3+ chars, lowercase letters/digits/hyphens, no leading
# or trailing hyphen. Source: https://fly.io/docs/flyctl/apps-create/
fly_validate_app_name() {
  local name="$1"
  [[ -n "$name" ]] || return 1
  [[ "$name" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]] || return 1
  (( ${#name} >= 3 )) || return 1
}

# Sharp edge: cronfoundry-runner image's entrypoint doesn't accept the
# `runner` subcommand. If the operator points FLY_RUNNER_IMAGE at a
# `*-runner:*` ref, dispatchers will silently fail. Warn loudly.
fly_warn_if_runner_image() {
  local ref="$1"
  if [[ "$ref" == *"-runner:"* || "$ref" == *"-runner" ]]; then
    echo -e "${YELLOW}[warn]${RESET}  ${ref}: -runner image is dead — the runner subcommand lives on the main binary. See PR #83." >&2
    return 1
  fi
  return 0
}

# ── flyctl wrappers ─────────────────────────────────────────────────────────
# All of these are idempotent: re-running them is a no-op on second invocation.

# fly_app_exists NAME → 0 if the current operator can see an app with NAME.
fly_app_exists() {
  flyctl apps list --json 2>/dev/null | jq -e --arg n "$1" '.[] | select(.Name == $n)' >/dev/null
}

# fly_app_create NAME [extra flyctl args...]
fly_app_create() {
  local name="$1"; shift
  if fly_app_exists "$name"; then
    echo -e "${GREEN}[ok]${RESET}    app ${name} already exists"
    return 0
  fi
  echo -e "${CYAN}[info]${RESET}  creating app ${name}"
  # shellcheck disable=SC2068
  flyctl apps create "$name" $@
}

# fly_app_destroy NAME — best-effort; missing app is success.
fly_app_destroy() {
  local name="$1"
  if ! fly_app_exists "$name"; then
    echo -e "${GREEN}[ok]${RESET}    app ${name} already absent"
    return 0
  fi
  echo -e "${YELLOW}[warn]${RESET}  destroying app ${name}"
  flyctl apps destroy "$name" --yes
}

# fly_pg_exists NAME → 0 if a Postgres cluster app named NAME exists.
fly_pg_exists() {
  flyctl postgres list --json 2>/dev/null | jq -e --arg n "$1" '.[] | select(.Name == $n)' >/dev/null
}

fly_pg_create() {
  local name="$1" region="$2"
  if fly_pg_exists "$name"; then
    echo -e "${GREEN}[ok]${RESET}    postgres ${name} already exists"
    return 0
  fi
  echo -e "${CYAN}[info]${RESET}  creating postgres ${name} in ${region}"
  flyctl postgres create --name "$name" --region "$region"
}

fly_pg_destroy() {
  local name="$1"
  if ! fly_pg_exists "$name"; then
    echo -e "${GREEN}[ok]${RESET}    postgres ${name} already absent"
    return 0
  fi
  echo -e "${YELLOW}[warn]${RESET}  destroying postgres ${name}"
  flyctl apps destroy "$name" --yes
}

# fly_pg_attach PG_NAME APP_NAME — sets DATABASE_URL on APP_NAME if not already.
fly_pg_attach() {
  local pg="$1" app="$2"
  # `flyctl secrets list --json` emits lowercase keys (.name) in v0.4.x.
  # Earlier versions emitted .Name. Match either so the idempotency guard
  # actually fires — without it, the attach below trips "already contains
  # a secret named DATABASE_URL" on every re-run of fly-quickstart.sh.
  if flyctl secrets list -a "$app" --json 2>/dev/null \
      | jq -e '.[] | select((.name // .Name) == "DATABASE_URL")' >/dev/null; then
    echo -e "${GREEN}[ok]${RESET}    DATABASE_URL already set on ${app}"
    return 0
  fi
  echo -e "${CYAN}[info]${RESET}  attaching ${pg} to ${app}"
  flyctl postgres attach --app "$app" "$pg"
}

# fly_secrets_set_batch APP KEY1=VAL1 KEY2=VAL2 ... — single rolling restart.
fly_secrets_set_batch() {
  local app="$1"; shift
  if (( $# == 0 )); then return 0; fi
  echo -e "${CYAN}[info]${RESET}  setting $# secret(s) on ${app}"
  flyctl secrets set --app "$app" "$@"
}

# fly_deploy CONFIG APP IMAGE [extra args]
fly_deploy() {
  local config="$1" app="$2" image="$3"; shift 3
  echo -e "${CYAN}[info]${RESET}  deploying ${app} with ${image}"
  flyctl deploy --config "$config" --app "$app" --image "$image" "$@"
}

# fly_healthcheck HOST [TIMEOUT_SECONDS=120] — polls https://HOST/healthz.
fly_healthcheck() {
  local host="$1" timeout="${2:-120}"
  local deadline=$(( $(date +%s) + timeout ))
  while [[ $(date +%s) -lt $deadline ]]; do
    local code
    code=$(curl -fsS -o /dev/null -w "%{http_code}" "https://${host}/healthz" 2>/dev/null || echo "000")
    if [[ "$code" == "200" ]]; then
      echo -e "${GREEN}[ok]${RESET}    https://${host}/healthz is 200"
      return 0
    fi
    sleep 5
  done
  echo -e "${RED}[error]${RESET} https://${host}/healthz did not return 200 within ${timeout}s" >&2
  return 1
}
