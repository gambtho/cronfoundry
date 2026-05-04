#!/usr/bin/env bash
set -u
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PASS=0; FAIL=0; FAILED_TESTS=()

run_test() {
  local name="$1"; shift
  if (
    set -e
    # shellcheck disable=SC1091
    source "${SCRIPT_DIR}/fly.sh"
    "$@"
  ); then
    PASS=$((PASS+1)); echo "ok - $name"
  else
    FAIL=$((FAIL+1)); FAILED_TESTS+=("$name"); echo "not ok - $name"
  fi
}

test_validate_app_name_accepts_hyphens() { fly_validate_app_name "cronfoundry-api"; }
test_validate_app_name_rejects_underscore() { ! fly_validate_app_name "cron_foundry"; }
test_validate_app_name_rejects_uppercase() { ! fly_validate_app_name "CronFoundry"; }
test_validate_app_name_rejects_empty() { ! fly_validate_app_name ""; }

test_runner_image_warning_on_dash_runner() {
  local out; out=$(fly_warn_if_runner_image "ghcr.io/foo/cronfoundry-runner:1.2.3" 2>&1 >/dev/null) || true
  [[ "$out" == *"-runner image is dead"* ]]
}

test_runner_image_silent_on_normal() {
  local out; out=$(fly_warn_if_runner_image "ghcr.io/foo/cronfoundry:1.2.3" 2>&1 >/dev/null) || true
  [[ -z "$out" ]]
}

run_test "fly_validate_app_name accepts hyphenated names" test_validate_app_name_accepts_hyphens
run_test "fly_validate_app_name rejects underscores" test_validate_app_name_rejects_underscore
run_test "fly_validate_app_name rejects uppercase" test_validate_app_name_rejects_uppercase
run_test "fly_validate_app_name rejects empty" test_validate_app_name_rejects_empty
run_test "fly_warn_if_runner_image warns on -runner suffix" test_runner_image_warning_on_dash_runner
run_test "fly_warn_if_runner_image silent on plain image" test_runner_image_silent_on_normal

echo; echo "${PASS} passed, ${FAIL} failed"
if (( FAIL > 0 )); then printf '  - %s\n' "${FAILED_TESTS[@]}"; exit 1; fi
exit 0
