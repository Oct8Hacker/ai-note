# shellcheck shell=bash
# Shared helpers for the demo scripts (SRS §15.4). Source it; don't run it.
#
# Environment:
#   NOTION_TEST_TOKEN  Notion integration token, for scripts that submit jobs.
#   NOTION_PARENT_ID   optional destination; default is NOTION_DEFAULT_PARENT_ID.
#   GATEWAY_URL        default http://localhost:8080
#   ADMIN_TOKEN        default: the ADMIN_TOKEN line in .env
#
# Secrets are never passed as command arguments: curl reads headers from a
# config on stdin (-K -) and jq reads the Notion token with env.*.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GW="${GATEWAY_URL:-http://localhost:8080}"
SCRIPT="$(basename "$0")"

for tool in curl jq; do
  command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 2; }
done

# env_value KEY prints KEY's value from .env (last occurrence, quotes stripped).
env_value() {
  [ -f "$ROOT/.env" ] || return 0
  grep -E "^$1=" "$ROOT/.env" | tail -1 | cut -d= -f2- | sed -E 's/^"(.*)"$/\1/; s/^'"'"'(.*)'"'"'$/\1/'
}
ADMIN_TOKEN="${ADMIN_TOKEN:-$(env_value ADMIN_TOKEN)}"

pass() { echo "PASS $SCRIPT: $*"; exit 0; }
# fail writes to stderr so the line still shows when it is called inside $(...),
# e.g. from submit or wait_terminal.
fail() { echo "FAIL $SCRIPT: $*" >&2; exit 1; }
elapsed() { echo $(( $(date +%s) - $1 )); }

# Git Bash on Windows rewrites /paths in docker arguments; turn that off.
export MSYS_NO_PATHCONV=1

compose() {
  docker compose -f "$ROOT/deploy/docker-compose.yml" --env-file "$ROOT/.env" "$@"
}

# admin_curl ARGS... — curl with the admin bearer token.
admin_curl() {
  [ -n "$ADMIN_TOKEN" ] || fail "ADMIN_TOKEN is not set (in the environment or .env)"
  printf 'header = "Authorization: Bearer %s"\n' "$ADMIN_TOKEN" | curl -sS -K - "$@"
}

# notion_curl ARGS... — curl with the Notion token, for /notion/pages.
notion_curl() {
  printf 'header = "Authorization: Bearer %s"\n' "$NOTION_TEST_TOKEN" | curl -sS -K - "$@"
}

health_json() { curl -sS "$GW/healthz"; }
provider_429_total() { health_json | jq -r '.provider_429_total // 0'; }

# set_faults JSON — PUT /admin/faults; values are strings, e.g. {"llm_429_rate":"0.7"}.
set_faults() {
  admin_curl --fail-with-body -X PUT "$GW/admin/faults" \
    -H 'Content-Type: application/json' --data-binary "$1" >/dev/null
}
reset_faults() {
  set_faults '{"llm_429_rate":"0","notion_429_rate":"0","youtube_429_rate":"0","frame_fail_rate":"0","frame_fail_all":"false"}'
}

# submit URL — POST /jobs; prints the job ID.
submit() {
  : "${NOTION_TEST_TOKEN:?NOTION_TEST_TOKEN must be set}"
  local body resp code
  body=$(jq -n --arg url "$1" '
    {youtube_url: $url, notion_token: env.NOTION_TEST_TOKEN}
    + (if (env.NOTION_PARENT_ID // "") != "" then {notion_parent_id: env.NOTION_PARENT_ID} else {} end)')
  resp=$(printf '%s' "$body" | curl -sS -X POST "$GW/jobs" \
    -H 'Content-Type: application/json' -H "Idempotency-Key: demo-$(date +%s)-$RANDOM$RANDOM" \
    --data-binary @- -w '\n%{http_code}')
  code=${resp##*$'\n'}
  resp=${resp%$'\n'*}
  [ "$code" = 202 ] || fail "POST /jobs returned HTTP $code: $resp"
  echo "$resp" | jq -r .job_id
}

job_view() { curl -sS --fail "$GW/jobs/$1"; }
job_status() { job_view "$1" | jq -r .status; }
is_terminal() {
  case "$1" in COMPLETED|COMPLETED_WITH_WARNINGS|FAILED|DEAD_LETTERED) return 0 ;; esac
  return 1
}

# wait_terminal JOB TIMEOUT_SEC — prints each status change to stderr and the
# final status to stdout. Fails the script on timeout.
wait_terminal() {
  local job=$1 timeout=$2 start last="" line status
  start=$(date +%s)
  while :; do
    line=$(job_view "$job" | jq -r '"\(.status) \(.progress)%"
      + (if .waiting then " (waiting: \(.waiting.reason) \(.waiting.provider // ""))" else "" end)')
    if [ "$line" != "$last" ]; then
      printf '  [%4ss] %s %s\n' "$(elapsed "$start")" "${job:0:8}" "$line" >&2
      last=$line
    fi
    status=${line%% *}
    if is_terminal "$status"; then echo "$status"; return 0; fi
    [ "$(elapsed "$start")" -lt "$timeout" ] || fail "job $job still $status after ${timeout}s"
    sleep 1
  done
}
