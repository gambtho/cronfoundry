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

test_set_round_trips() {
  dotenv_set FOO bar
  unset FOO
  dotenv_load
  [[ "$FOO" == "bar" ]]
}

test_set_replaces_existing() {
  dotenv_set FOO first
  dotenv_set FOO second
  unset FOO
  dotenv_load
  [[ "$FOO" == "second" ]]
}

test_set_special_chars() {
  dotenv_set FOO 'has space and $dollar'
  unset FOO
  dotenv_load
  [[ "$FOO" == 'has space and $dollar' ]]
}

test_set_prefix_collision() {
  dotenv_set FOO one
  dotenv_set FOO_BAR two
  dotenv_set FOO three
  unset FOO FOO_BAR
  dotenv_load
  [[ "$FOO" == "three" ]] && [[ "$FOO_BAR" == "two" ]]
}

test_set_mode_600() {
  dotenv_set FOO bar
  local perms
  perms=$(stat -c '%a' "$ENV_FILE" 2>/dev/null || stat -f '%Lp' "$ENV_FILE")
  [[ "$perms" == "600" ]]
}

test_has_true_when_in_file() {
  dotenv_set FOO bar
  unset FOO
  dotenv_has FOO
}

test_has_false_when_absent() {
  ! dotenv_has FOO
}

run_test "dotenv_set + dotenv_load round-trips" test_set_round_trips
run_test "dotenv_set replaces an existing key" test_set_replaces_existing
run_test "dotenv_set quotes special characters" test_set_special_chars
run_test "dotenv_set with prefix-colliding keys updates only the exact key" test_set_prefix_collision
run_test "dotenv_set leaves file mode 600" test_set_mode_600
run_test "dotenv_has returns 0 when key in file" test_has_true_when_in_file
run_test "dotenv_has returns nonzero when key absent" test_has_false_when_absent

test_require_returns_existing_silently() {
  dotenv_set FOO preset
  # Should not read from /dev/tty; piping nothing must still succeed.
  local out; out=$(dotenv_require FOO "Enter foo" </dev/null)
  [[ "$out" == "preset" ]]
}

test_require_non_interactive_fails_on_missing() {
  DOTENV_NON_INTERACTIVE=1
  ! dotenv_require FOO "Enter foo" </dev/null 2>/dev/null
}

test_require_with_default_uses_default_when_blank() {
  unset FOO
  # Empty stdin -> read returns empty -> default used.
  local out; out=$(printf '\n' | dotenv_require FOO "Enter foo" "thedefault")
  [[ "$out" == "thedefault" ]]
  unset FOO
  dotenv_load
  [[ "$FOO" == "thedefault" ]]
}

run_test "dotenv_require returns existing value without prompting" test_require_returns_existing_silently
run_test "dotenv_require fails when DOTENV_NON_INTERACTIVE and key missing" test_require_non_interactive_fails_on_missing
run_test "dotenv_require uses default on blank input and persists it" test_require_with_default_uses_default_when_blank

test_get_strips_quotes_from_file_lookup() {
  # Write a quoted value directly so we exercise the file-lookup branch
  # (dotenv_set goes through env, which would mask the bug).
  printf "FOO='hello world'\n" > "$ENV_FILE"
  unset FOO
  local v; v=$(dotenv_get FOO)
  [[ "$v" == "hello world" ]]
}

test_get_strips_double_quotes_from_file_lookup() {
  printf 'FOO="hi there"\n' > "$ENV_FILE"
  unset FOO
  local v; v=$(dotenv_get FOO)
  [[ "$v" == "hi there" ]]
}

run_test "dotenv_get strips single quotes when reading from file" test_get_strips_quotes_from_file_lookup
run_test "dotenv_get strips double quotes when reading from file" test_get_strips_double_quotes_from_file_lookup

echo; echo "${PASS} passed, ${FAIL} failed"
if (( FAIL > 0 )); then printf '  - %s\n' "${FAILED_TESTS[@]}"; exit 1; fi
exit 0
