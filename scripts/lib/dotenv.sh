# scripts/lib/dotenv.sh
# Read/write .env files for fly-quickstart.sh.
#
# ENV_FILE may be set by the caller; defaults to "$PWD/.env".
#
# dotenv_load   — source ENV_FILE, ignoring blanks/comments. Existing exported
#                 values WIN (operator can override per-invocation).
# dotenv_get K  — print value of K (from environment, then ENV_FILE).
# dotenv_set K V — append/replace K=V in ENV_FILE (atomic, mode 600).
# dotenv_has K  — exit 0 if K is set in env or ENV_FILE.

dotenv_path() { echo "${ENV_FILE:-${PWD}/.env}"; }

dotenv_load() {
  local f; f=$(dotenv_path)
  [[ -f "$f" ]] || return 0
  # Read line-by-line so we can skip comments and avoid set -a side effects.
  local line key val
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line#"${line%%[![:space:]]*}"}"   # ltrim
    [[ -z "$line" || "$line" == \#* ]] && continue
    [[ "$line" != *=* ]] && continue
    key="${line%%=*}"
    val="${line#*=}"
    # Strip matching surrounding quotes.
    if [[ "$val" =~ ^\"(.*)\"$ ]]; then val="${BASH_REMATCH[1]}";
    elif [[ "$val" =~ ^\'(.*)\'$ ]]; then val="${BASH_REMATCH[1]}"; fi
    # Existing exported value wins.
    if [[ -z "${!key+x}" ]]; then
      export "${key}=${val}"
    fi
  done < "$f"
}

dotenv_get() {
  local key="$1"
  if [[ -n "${!key+x}" ]]; then printf '%s' "${!key}"; return 0; fi
  local f; f=$(dotenv_path)
  [[ -f "$f" ]] || return 1
  awk -v k="$key" 'BEGIN{p=k"="} index($0, p)==1 { sub(p, ""); print; exit }' "$f"
}

dotenv_has() {
  local key="$1"
  [[ -n "${!key+x}" ]] && return 0
  local v; v=$(dotenv_get "$key" || true)
  [[ -n "$v" ]]
}

dotenv_set() {
  local key="$1" val="$2"
  local f; f=$(dotenv_path)
  local tmp="${f}.tmp.$$"
  if [[ -f "$f" ]]; then
    awk -v k="$key" 'BEGIN{p=k"="} index($0, p)!=1' "$f" > "$tmp"
  else
    : > "$tmp"
  fi
  # Single-quote the value so dotenv_load's quote-stripping round-trips
  # cleanly. Embedded single quotes are escaped as '\''.
  local escaped="${val//\'/\'\\\'\'}"
  printf "%s='%s'\n" "$key" "$escaped" >> "$tmp"
  mv "$tmp" "$f"
  chmod 600 "$f"
  export "${key}=${val}"
}

# dotenv_require KEY PROMPT [DEFAULT] [--secret]
#
# Returns the value of KEY:
#   1. If already in env (or .env), prints and returns 0.
#   2. Else if DOTENV_NON_INTERACTIVE is set, prints diagnostic and returns 1.
#   3. Else prompts the operator on stderr (silent if --secret), persists
#      the answer to .env via dotenv_set, prints the value on stdout.
#
# Designed so callers can do: VAL=$(dotenv_require FOO "Enter foo")
dotenv_require() {
  local key="$1" prompt="$2" default="${3:-}" mode=""
  if [[ "${4:-}" == "--secret" ]]; then mode="secret"; fi

  if dotenv_has "$key"; then
    dotenv_get "$key"
    return 0
  fi

  if [[ "${DOTENV_NON_INTERACTIVE:-0}" == "1" ]]; then
    echo "dotenv_require: ${key} is required but missing (--non-interactive)" >&2
    return 1
  fi

  local label="$prompt"
  [[ -n "$default" ]] && label="$prompt [${default}]"

  local val
  if [[ "$mode" == "secret" ]]; then
    printf '%s: ' "$label" >&2
    IFS= read -rs val
    printf '\n' >&2
  else
    printf '%s: ' "$label" >&2
    IFS= read -r val
  fi

  if [[ -z "$val" && -n "$default" ]]; then
    val="$default"
  fi

  if [[ -z "$val" ]]; then
    echo "dotenv_require: empty value for ${key}" >&2
    return 1
  fi

  dotenv_set "$key" "$val"
  printf '%s' "$val"
}
