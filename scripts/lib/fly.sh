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
