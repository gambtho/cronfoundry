#!/usr/bin/env bats

setup() {
  source "${BATS_TEST_DIRNAME}/steps.sh"
  WORK=$(mktemp -d)
}

teardown() {
  rm -rf "$WORK"
}

@test "step_run skips body when verifier passes" {
  ran=0
  step_run "noop" "true" "ran=1"
  [ "$ran" = "0" ]
}

@test "step_run runs body when verifier fails, then re-verifies" {
  step_run "create marker" 'echo x >> "$WORK/calls"; test -f "$WORK/marker"' 'touch "$WORK/marker"'
  [ -f "$WORK/marker" ]
  [ "$(wc -l < "$WORK/calls")" -eq 2 ]
}

@test "step_run aborts when verifier still fails after body" {
  run bash -c "source '${BATS_TEST_DIRNAME}/steps.sh'; step_run 'broken' 'false' 'true' 2>&1"
  [ "$status" -ne 0 ]
  [[ "$output" == *"verifier failed after body"* ]]
}

@test "step_run prints expected/got on failure" {
  run bash -c "source '${BATS_TEST_DIRNAME}/steps.sh'; step_run 'broken' 'false' 'echo doing stuff; false' 2>&1"
  [ "$status" -ne 0 ]
  [[ "$output" == *"step: broken"* ]]
}
