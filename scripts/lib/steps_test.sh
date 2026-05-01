#!/usr/bin/env bash
# Plain-bash fallback runner for steps.sh tests, mirroring steps_test.bats.
# Used when `bats` is not available. Each test runs in a subshell.
set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

PASS=0
FAIL=0
FAILED_TESTS=()

run_test() {
  local name="$1"; shift
  local WORK
  WORK=$(mktemp -d)
  export WORK
  if (
    set -e
    # shellcheck disable=SC1091
    source "${SCRIPT_DIR}/steps.sh"
    "$@"
  ); then
    PASS=$((PASS+1))
    echo "ok - $name"
  else
    FAIL=$((FAIL+1))
    FAILED_TESTS+=("$name")
    echo "not ok - $name"
  fi
  rm -rf "${WORK}"
  unset WORK
}

test_skips_body_when_verifier_passes() {
  ran=0
  step_run "noop" "true" "ran=1" >/dev/null
  [[ "$ran" == "0" ]]
}

test_runs_body_then_reverifies() {
  step_run "create marker" "test -f $WORK/marker" "touch $WORK/marker" >/dev/null
  [[ -f "$WORK/marker" ]]
}

test_aborts_when_verifier_still_fails() {
  local output status
  output=$(step_run "broken" "false" "true" 2>&1) && status=0 || status=$?
  [[ "$status" -ne 0 ]] && [[ "$output" == *"verifier failed after body"* ]]
}

test_prints_step_name_on_failure() {
  local output status
  output=$(step_run "broken" "false" "echo doing stuff; false" 2>&1) && status=0 || status=$?
  [[ "$status" -ne 0 ]] && [[ "$output" == *"step: broken"* ]]
}

run_test "step_run skips body when verifier passes" test_skips_body_when_verifier_passes
run_test "step_run runs body when verifier fails, then re-verifies" test_runs_body_then_reverifies
run_test "step_run aborts when verifier still fails after body" test_aborts_when_verifier_still_fails
run_test "step_run prints expected/got on failure" test_prints_step_name_on_failure

echo
echo "${PASS} passed, ${FAIL} failed"
if (( FAIL > 0 )); then
  printf '  - %s\n' "${FAILED_TESTS[@]}"
  exit 1
fi
exit 0
