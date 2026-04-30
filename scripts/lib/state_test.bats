#!/usr/bin/env bats

setup() {
  STATE_DIR=$(mktemp -d)
  export STATE_FILE="${STATE_DIR}/state-test"
  source "${BATS_TEST_DIRNAME}/state.sh"
}

teardown() {
  rm -rf "${STATE_DIR}"
}

@test "state_save and reload round-trips a value" {
  state_init
  state_save "CF_FOO" "bar"
  unset CF_FOO
  state_load
  [ "$CF_FOO" = "bar" ]
}

@test "state_save quotes special characters" {
  state_init
  state_save "CF_PASSWORD" 'hunter2$#!'
  unset CF_PASSWORD
  state_load
  [ "$CF_PASSWORD" = 'hunter2$#!' ]
}

@test "state_init creates file with mode 600" {
  state_init
  perms=$(stat -c '%a' "$STATE_FILE" 2>/dev/null || stat -f '%Lp' "$STATE_FILE")
  [ "$perms" = "600" ]
}

@test "state_clear removes the file" {
  state_init
  state_save "CF_FOO" "bar"
  state_clear
  [ ! -f "$STATE_FILE" ]
}

@test "state_path_for env returns per-env file" {
  unset STATE_FILE
  result=$(state_path_for "copilot1")
  [[ "$result" == *".cronfoundry-quickstart-state-copilot1" ]]
}
