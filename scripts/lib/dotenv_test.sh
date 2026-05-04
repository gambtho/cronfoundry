#!/usr/bin/env bash
# Plain-bash test runner for dotenv.sh, same shape as state_test.sh.
set -u

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

PASS=0; FAIL=0; FAILED_TESTS=()

run_test() {
  local name="$1"; shift
  local TMP
  TMP=$(mktemp -d)
  export ENV_FILE="${TMP}/.env"
  if (
    set -e
    # shellcheck disable=SC1091
    source "${SCRIPT_DIR}/dotenv.sh"
    "$@"
  ); then
    PASS=$((PASS+1)); echo "ok - $name"
  else
    FAIL=$((FAIL+1)); FAILED_TESTS+=("$name"); echo "not ok - $name"
  fi
  rm -rf "${TMP}"; unset ENV_FILE
}

test_load_missing_file_is_noop() {
  dotenv_load
  [[ -z "${FOO:-}" ]]
}

test_load_reads_simple_pairs() {
  printf 'FOO=bar\nBAZ=qux\n' > "$ENV_FILE"
  dotenv_load
  [[ "$FOO" == "bar" ]] && [[ "$BAZ" == "qux" ]]
}

test_load_ignores_blank_and_comments() {
  printf '# top comment\n\nFOO=bar\n  # indented\nBAZ=qux\n' > "$ENV_FILE"
  dotenv_load
  [[ "$FOO" == "bar" ]] && [[ "$BAZ" == "qux" ]]
}

test_load_preserves_quoted_values() {
  printf 'FOO="hello world"\nBAR=\x27a$b\x27\n' > "$ENV_FILE"
  dotenv_load
  [[ "$FOO" == "hello world" ]] && [[ "$BAR" == 'a$b' ]]
}

test_load_does_not_override_existing() {
  export FOO=preset
  printf 'FOO=fromfile\n' > "$ENV_FILE"
  dotenv_load
  # Existing exported values win — operator overrides via shell env.
  [[ "$FOO" == "preset" ]]
}

run_test "dotenv_load on missing file is a no-op" test_load_missing_file_is_noop
run_test "dotenv_load reads simple key=value pairs" test_load_reads_simple_pairs
run_test "dotenv_load ignores blanks and comments" test_load_ignores_blank_and_comments
run_test "dotenv_load preserves single- and double-quoted values" test_load_preserves_quoted_values
run_test "dotenv_load does not override existing exported values" test_load_does_not_override_existing

echo; echo "${PASS} passed, ${FAIL} failed"
if (( FAIL > 0 )); then printf '  - %s\n' "${FAILED_TESTS[@]}"; exit 1; fi
exit 0
