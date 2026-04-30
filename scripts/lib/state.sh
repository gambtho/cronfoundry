# scripts/lib/state.sh
# State-file helpers for the cronfoundry quickstart script.
#
# STATE_FILE may be set by the caller; otherwise default to
# ~/.cronfoundry-quickstart-state. Supports per-env suffix via
# state_path_for().

state_path_for() {
  local env="$1"
  echo "${HOME}/.cronfoundry-quickstart-state-${env}"
}

state_init() {
  : "${STATE_FILE:=${HOME}/.cronfoundry-quickstart-state}"
  if [[ ! -f "$STATE_FILE" ]]; then
    touch "$STATE_FILE"
  fi
  chmod 600 "$STATE_FILE"
}

state_load() {
  : "${STATE_FILE:=${HOME}/.cronfoundry-quickstart-state}"
  if [[ -f "$STATE_FILE" ]]; then
    # shellcheck disable=SC1090
    source "$STATE_FILE"
  fi
}

state_save() {
  local key="$1" val="$2"
  state_init
  # Remove any existing line with this key, then append.
  if grep -q "^${key}=" "$STATE_FILE" 2>/dev/null; then
    grep -v "^${key}=" "$STATE_FILE" > "${STATE_FILE}.tmp" && mv "${STATE_FILE}.tmp" "$STATE_FILE"
  fi
  printf '%s=%q\n' "$key" "$val" >> "$STATE_FILE"
  chmod 600 "$STATE_FILE"
  # Also export for in-process reads.
  export "${key}=${val}"
}

state_clear() {
  : "${STATE_FILE:=${HOME}/.cronfoundry-quickstart-state}"
  rm -f "$STATE_FILE"
}
