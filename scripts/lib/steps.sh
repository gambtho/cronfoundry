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
    echo -e "${YELLOW}resume with: bash $(basename "${0:-quickstart-copilot.sh}")${RESET}" >&2
    return 1
  fi

  if ! eval "$verifier" &>/dev/null; then
    echo -e "${RED}step: ${name}${RESET}" >&2
    echo -e "${RED}verifier failed after body. expected: ${verifier}${RESET}" >&2
    echo -e "${YELLOW}resume with: bash $(basename "${0:-quickstart-copilot.sh}")${RESET}" >&2
    return 1
  fi

  echo -e "  ${GREEN}✓ done${RESET}"
}
