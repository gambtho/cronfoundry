#!/usr/bin/env bash
# Patch cronfoundry.yaml on the configured skills repo with one of the
# pre-baked cron schedules (cron-2min.yaml or cron-daily.yaml).
#
# Why this exists: the dogfood loop needs to flip the cron between a
# 2-minute trigger (smoke verification) and the daily resting cadence.
# Inlining base64-encoded YAML in ad-hoc `gh api -X PUT` calls means
# every invocation looks novel to permission allowlists, generating
# repeated approval prompts. A stable script with a small, reviewable
# allowlist signature avoids that.
#
# Usage:
#   scripts/dogfood/patch-cron.sh 2min     # trigger smoke
#   scripts/dogfood/patch-cron.sh daily    # revert to resting
#   scripts/dogfood/patch-cron.sh --repo gambtho/skills 2min
#
# Requires: gh (authenticated), base64.

set -euo pipefail

REPO="${CRONFOUNDRY_SKILLS_REPO:-gambtho/skills}"
PATH_IN_REPO="${CRONFOUNDRY_SKILLS_PATH:-cronfoundry.yaml}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage() {
  cat <<'USAGE' >&2
Usage: patch-cron.sh [--repo owner/name] [--path file] {2min|daily|<file>}

  2min   Use scripts/dogfood/cron-2min.yaml  (smoke trigger every 2 minutes).
  daily  Use scripts/dogfood/cron-daily.yaml (daily-09:00 UTC resting cadence).
  <file> Path to any other YAML to PUT verbatim.

Env:
  CRONFOUNDRY_SKILLS_REPO  default gambtho/skills
  CRONFOUNDRY_SKILLS_PATH  default cronfoundry.yaml
USAGE
  exit 2
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --repo)
      [[ $# -ge 2 ]] || { echo "--repo requires a value (owner/name)" >&2; exit 2; }
      REPO="$2"; shift 2 ;;
    --path)
      [[ $# -ge 2 ]] || { echo "--path requires a value" >&2; exit 2; }
      PATH_IN_REPO="$2"; shift 2 ;;
    -h|--help) usage ;;
    -*)
      echo "unknown option: $1" >&2
      usage
      ;;
    *)
      MODE="$1"; shift
      ;;
  esac
done

[[ -n "${MODE:-}" ]] || usage

case "$MODE" in
  2min)  FILE="${SCRIPT_DIR}/cron-2min.yaml" ;;
  daily) FILE="${SCRIPT_DIR}/cron-daily.yaml" ;;
  *)     FILE="$MODE" ;;
esac

[[ -f "$FILE" ]] || { echo "no such file: $FILE" >&2; exit 1; }

# Always re-fetch SHA right before PUT — GitHub returns 409 on stale SHA.
SHA=$(gh api "repos/${REPO}/contents/${PATH_IN_REPO}" --jq .sha) \
  || { echo "could not fetch current sha for ${REPO}/${PATH_IN_REPO}" >&2; exit 1; }

CONTENT=$(base64 -w0 "$FILE")
MSG="dogfood: patch-cron $(basename "$FILE")"

gh api -X PUT "repos/${REPO}/contents/${PATH_IN_REPO}" \
  -f message="$MSG" \
  -f content="$CONTENT" \
  -f sha="$SHA" \
  --jq '.commit.sha'
