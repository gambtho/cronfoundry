#!/usr/bin/env bash
# CronFoundry — Fly.io smoke assertions.
#
# Polls /api/runs?limit=1 on the deployed api app until terminal state, then
# verifies tokens > 0, an issue filed in $CRONFOUNDRY_SKILLS_REPO, and a
# memory.md writeback commit on the default branch.
#
# Usage: scripts/fly-smoke-assert.sh
set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; BOLD='\033[1m'; RESET='\033[0m'
info() { echo -e "${CYAN}[info]${RESET}  $*"; }
ok()   { echo -e "${GREEN}[ok]${RESET}    $*"; }
warn() { echo -e "${YELLOW}[warn]${RESET}  $*"; }
die()  { echo -e "${RED}[error]${RESET} $*" >&2; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
export ENV_FILE="${REPO_ROOT}/.env"
# shellcheck source=lib/dotenv.sh
source "${SCRIPT_DIR}/lib/dotenv.sh"
dotenv_load

command -v jq >/dev/null   || die "jq not on PATH"
command -v gh >/dev/null   || die "gh not on PATH"
gh auth status >/dev/null 2>&1 || die "gh is not authenticated. Run: gh auth login"

: "${FLY_API_APP:?set FLY_API_APP in .env}"
: "${FLY_RUNNER_APP:?set FLY_RUNNER_APP in .env}"
: "${CRONFOUNDRY_SKILLS_REPO:?set CRONFOUNDRY_SKILLS_REPO in .env (e.g. owner/name)}"

TIMEOUT="${SMOKE_TIMEOUT_SECONDS:-900}"
RUNS_URL="https://${FLY_API_APP}.fly.dev/api/runs?limit=1"

info "polling ${RUNS_URL} (timeout ${TIMEOUT}s)"

# Snapshot the latest-run id at start. We only consider a run that arrived
# after this point — otherwise a stale terminal run from a prior round would
# pass/fail the assert without ever waiting for the new dispatch.
BASELINE_JSON=$(curl -fsS "$RUNS_URL" 2>/dev/null || echo '[]')
BASELINE_ID=$(echo "$BASELINE_JSON" | jq -r '.[0].id // empty')
if [[ -n "$BASELINE_ID" ]]; then
  info "baseline (must see a newer run id than this): ${BASELINE_ID}"
else
  info "no prior runs visible — any terminal run will count"
fi

DEADLINE=$(( $(date +%s) + TIMEOUT ))
RUN_JSON=""
while [[ $(date +%s) -lt $DEADLINE ]]; do
  RUNS=$(curl -fsS "$RUNS_URL" 2>/dev/null || echo '[]')
  CUR_ID=$(echo "$RUNS" | jq -r '.[0].id // empty')
  if [[ -z "$CUR_ID" || "$CUR_ID" == "$BASELINE_ID" ]]; then
    sleep 10; continue
  fi
  STATUS=$(echo "$RUNS" | jq -r '.[0].status // empty')
  case "$STATUS" in
    succeeded)
      RUN_JSON=$(echo "$RUNS" | jq '.[0]')
      break
      ;;
    failed|partial_failure)
      echo
      warn "newest run finished as ${STATUS} (id ${CUR_ID})"
      warn "dumping last 100 lines of runner logs:"
      flyctl logs --app "$FLY_RUNNER_APP" 2>/dev/null | tail -100 || true
      die "smoke run did not succeed"
      ;;
  esac
  sleep 10
done

if [[ -z "$RUN_JSON" ]]; then
  warn "no new terminal run within ${TIMEOUT}s; dumping last 100 lines of runner logs:"
  flyctl logs --app "$FLY_RUNNER_APP" 2>/dev/null | tail -100 || true
  die "timed out waiting for a successful run"
fi

RUN_ID=$(echo "$RUN_JSON" | jq -r '.id')
ok "run ${RUN_ID} succeeded"

# ── tokens ──────────────────────────────────────────────────────────────────
# Token + duration fields live on runDetailDTO (GET /api/runs/{id}), not on
# the runSummaryDTO returned by the list endpoint. Fetch the detail.
RUN_DETAIL_URL="https://${FLY_API_APP}.fly.dev/api/runs/${RUN_ID}"
RUN_DETAIL=$(curl -fsS "$RUN_DETAIL_URL" 2>/dev/null) \
  || die "failed to fetch run detail at ${RUN_DETAIL_URL}"

# Field names per internal/webapi/runs.go::runDetailDTO: tokens_in / tokens_out.
TOKENS_IN=$(echo  "$RUN_DETAIL" | jq -r '.tokens_in  // 0')
TOKENS_OUT=$(echo "$RUN_DETAIL" | jq -r '.tokens_out // 0')
DURATION_MS=$(echo "$RUN_DETAIL" | jq -r '.duration_ms // 0')

(( TOKENS_IN  > 0 )) || die "run ${RUN_ID} reports tokens_in=${TOKENS_IN} (expected > 0)"
(( TOKENS_OUT > 0 )) || die "run ${RUN_ID} reports tokens_out=${TOKENS_OUT} (expected > 0)"
ok "tokens: in=${TOKENS_IN} out=${TOKENS_OUT} duration=${DURATION_MS}ms"

# ── issue filed ─────────────────────────────────────────────────────────────
info "checking for issue 'smoke run ${RUN_ID}' in ${CRONFOUNDRY_SKILLS_REPO}"
ISSUE_JSON=$(gh issue list -R "$CRONFOUNDRY_SKILLS_REPO" \
  --search "smoke run ${RUN_ID} in:title" \
  --state all \
  --json number,title,url \
  --limit 5)
ISSUE_COUNT=$(echo "$ISSUE_JSON" | jq 'length')
(( ISSUE_COUNT >= 1 )) || die "no issue with title containing 'smoke run ${RUN_ID}' in ${CRONFOUNDRY_SKILLS_REPO}"
ISSUE_URL=$(echo "$ISSUE_JSON" | jq -r '.[0].url')
ok "issue filed: ${ISSUE_URL}"

# ── writeback commit ────────────────────────────────────────────────────────
EXPECTED_SUBJECT="chore(cronfoundry): update memory.md from run ${RUN_ID}"
info "checking default-branch commits for: ${EXPECTED_SUBJECT}"
COMMITS_JSON=$(gh api "repos/${CRONFOUNDRY_SKILLS_REPO}/commits?per_page=10")
MATCH_SHA=$(echo "$COMMITS_JSON" | jq -r --arg s "$EXPECTED_SUBJECT" \
  '.[] | select(.commit.message | startswith($s)) | .sha' | head -1)
[[ -n "$MATCH_SHA" ]] || die "no commit on default branch with subject '${EXPECTED_SUBJECT}'"
ok "writeback commit: ${MATCH_SHA}"

# ── final report ────────────────────────────────────────────────────────────
echo
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
echo "  Smoke assertions PASSED"
echo "  Run id:     ${RUN_ID}"
echo "  Tokens:     in=${TOKENS_IN}  out=${TOKENS_OUT}"
echo "  Duration:   ${DURATION_MS}ms"
echo "  Issue:      ${ISSUE_URL}"
echo "  Commit:     ${MATCH_SHA}"
echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
