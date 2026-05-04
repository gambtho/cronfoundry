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
