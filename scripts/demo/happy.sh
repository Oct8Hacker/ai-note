#!/usr/bin/env bash
# happy.sh <youtube-url> — submit one job through the gateway API, print each
# status change, then the results JSON and the end-to-end time (NFR-PERF-2).
#
# Environment:
#   NOTION_TEST_TOKEN  (required) Notion integration token. Read from the
#                      environment so it never appears in argv or shell history.
#   NOTION_PARENT_ID   (optional) destination page/database ID; when unset the
#                      worker uses NOTION_DEFAULT_PARENT_ID.
#   GATEWAY_URL        (optional) default http://localhost:8080
#   HAPPY_TIMEOUT      (optional) seconds to wait for a terminal status, default 600
set -euo pipefail

if [ $# -ne 1 ]; then
  echo "usage: NOTION_TEST_TOKEN=... $0 <youtube-url>" >&2
  exit 2
fi
: "${NOTION_TEST_TOKEN:?NOTION_TEST_TOKEN must be set}"
for tool in curl jq; do
  command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 2; }
done

url="$1"
gw="${GATEWAY_URL:-http://localhost:8080}"
timeout="${HAPPY_TIMEOUT:-600}"
idem="happy-$(date +%s)-$RANDOM$RANDOM"

# jq reads the token with env.*, so it is never passed as a command argument.
body=$(jq -n --arg url "$url" '
  {youtube_url: $url, notion_token: env.NOTION_TEST_TOKEN}
  + (if (env.NOTION_PARENT_ID // "") != "" then {notion_parent_id: env.NOTION_PARENT_ID} else {} end)')

start=$(date +%s)
resp=$(printf '%s' "$body" | curl -sS -X POST "$gw/jobs" \
  -H 'Content-Type: application/json' -H "Idempotency-Key: $idem" \
  --data-binary @- -w '\n%{http_code}')
code=${resp##*$'\n'}
resp=${resp%$'\n'*}
if [ "$code" != 202 ]; then
  echo "FAIL happy.sh: POST /jobs failed (HTTP $code):" >&2
  echo "$resp" | jq . >&2 2>/dev/null || echo "$resp" >&2
  exit 1
fi
job=$(echo "$resp" | jq -r .job_id)
echo "job $job submitted ($(echo "$resp" | jq -r .video_id))"

last=""
while :; do
  view=$(curl -sS --fail "$gw/jobs/$job")
  status=$(echo "$view" | jq -r .status)
  line=$(echo "$view" | jq -r '"\(.status) \(.progress)%"
    + (if .waiting then " (waiting: \(.waiting.reason) \(.waiting.provider // ""))" else "" end)')
  if [ "$line" != "$last" ]; then
    printf '[%4ss] %s\n' "$(( $(date +%s) - start ))" "$line"
    last="$line"
  fi
  case "$status" in
    COMPLETED|COMPLETED_WITH_WARNINGS|FAILED|DEAD_LETTERED|CANCELLED) break ;;
  esac
  if [ $(( $(date +%s) - start )) -ge "$timeout" ]; then
    echo "FAIL happy.sh: timed out after ${timeout}s in status $status" >&2
    exit 1
  fi
  sleep 1
done

elapsed=$(( $(date +%s) - start ))
echo
curl -sS --fail "$gw/jobs/$job/results" | jq .
echo
echo "end-to-end: ${elapsed}s (target <= 90s); final status: $status"
case "$status" in
  COMPLETED|COMPLETED_WITH_WARNINGS) echo "PASS happy.sh: $status in ${elapsed}s"; exit 0 ;;
  *) echo "FAIL happy.sh: final status $status after ${elapsed}s"; exit 1 ;;
esac
