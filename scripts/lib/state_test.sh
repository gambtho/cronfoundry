#!/usr/bin/env bash
# Plain-bash fallback runner for state.sh tests, mirroring state_test.bats.
# Used when `bats` is not available. Each test runs in a subshell.
set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

PASS=0
FAIL=0
FAILED_TESTS=()

run_test() {
  local name="$1"; shift
  local STATE_DIR
  STATE_DIR=$(mktemp -d)
  export STATE_FILE="${STATE_DIR}/state-test"
  if (
    set -e
    # shellcheck disable=SC1091
    source "${SCRIPT_DIR}/state.sh"
    "$@"
  ); then
    PASS=$((PASS+1))
    echo "ok - $name"
  else
    FAIL=$((FAIL+1))
    FAILED_TESTS+=("$name")
    echo "not ok - $name"
  fi
  rm -rf "${STATE_DIR}"
  unset STATE_FILE
}

test_round_trip() {
  state_init
  state_save "CF_FOO" "bar"
  unset CF_FOO
  state_load
  [[ "$CF_FOO" == "bar" ]]
}

test_special_chars() {
  state_init
  state_save "CF_PASSWORD" 'hunter2$#!'
  unset CF_PASSWORD
  state_load
  [[ "$CF_PASSWORD" == 'hunter2$#!' ]]
}

test_mode_600() {
  state_init
  local perms
  perms=$(stat -c '%a' "$STATE_FILE" 2>/dev/null || stat -f '%Lp' "$STATE_FILE")
  [[ "$perms" == "600" ]]
}

test_state_clear() {
  state_init
  state_save "CF_FOO" "bar"
  state_clear
  [[ ! -f "$STATE_FILE" ]]
}

test_state_path_for() {
  unset STATE_FILE
  local result
  result=$(state_path_for "copilot1")
  [[ "$result" == *".cronfoundry-quickstart-state-copilot1" ]]
}

run_test "state_save and reload round-trips a value" test_round_trip
run_test "state_save quotes special characters" test_special_chars
run_test "state_init creates file with mode 600" test_mode_600
run_test "state_clear removes the file" test_state_clear
run_test "state_path_for env returns per-env file" test_state_path_for

echo
echo "${PASS} passed, ${FAIL} failed"
if (( FAIL > 0 )); then
  printf '  - %s\n' "${FAILED_TESTS[@]}"
  exit 1
fi
exit 0
