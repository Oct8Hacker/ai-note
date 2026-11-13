#!/usr/bin/env bash
# kill-worker.sh <url> — SIGKILL the worker holding a job in
# EXTRACTING_FRAMES. Another worker must resume it within 15 s (progress
# moves again) and the job must end with exactly one Notion page.
# Run with deploy/demo.env loaded so frames are slow enough to catch.
source "$(dirname "$0")/lib.sh"
[ $# -eq 1 ] || { echo "usage: $0 <youtube-url>" >&2; exit 2; }

job=$(submit "$1")
echo "job $job submitted; waiting for EXTRACTING_FRAMES"
start=$(date +%s)
while :; do
  status=$(job_status "$job")
  [ "$status" != EXTRACTING_FRAMES ] || break
  if is_terminal "$status"; then fail "job reached $status before EXTRACTING_FRAMES"; fi
  [ "$(elapsed "$start")" -lt 300 ] || fail "job never reached EXTRACTING_FRAMES"
  sleep 0.5
done

pg_user=$(env_value POSTGRES_USER)
pg_db=$(env_value POSTGRES_DB)
owner=$(compose exec -T postgres psql -U "${pg_user:-notes}" -d "${pg_db:-notes}" -tAc \
  "SELECT lease_owner FROM jobs WHERE id = '$job'" | tr -d '[:space:]')
[ -n "$owner" ] || fail "job has no lease_owner"
container=""
for c in $(compose ps -q worker); do
  if [ "$(docker inspect -f '{{.Config.Hostname}}' "$c")" = "$owner" ]; then container=$c; fi
done
[ -n "$container" ] || fail "no worker container with hostname $owner"

before=$(job_view "$job" | jq -r .progress)
docker kill -s KILL "$container" >/dev/null
killed=$(date +%s)
echo "killed worker $owner at progress $before%"

recovered=""
while [ "$(elapsed "$killed")" -lt 120 ]; do
  view=$(job_view "$job")
  status=$(echo "$view" | jq -r .status)
  progress=$(echo "$view" | jq -r .progress)
  if [ "$progress" -gt "$before" ] || [ "$status" != EXTRACTING_FRAMES ]; then
    recovered=$(elapsed "$killed")
    break
  fi
  sleep 0.5
done
[ -n "$recovered" ] || fail "no progress within 120s of the kill"
echo "recovered in ${recovered}s"
docker start "$container" >/dev/null || true

status=$(wait_terminal "$job" 900)
case "$status" in
  COMPLETED|COMPLETED_WITH_WARNINGS) ;;
  *) fail "job ended $status" ;;
esac
short=${job:0:6}
pages=$(notion_curl --fail-with-body "$GW/notion/pages" |
  jq --arg s "· $short" '[.pages[] | select(.title | endswith($s))] | length')
echo "Notion pages for job $short: $pages"
[ "$recovered" -le 15 ] || fail "recovered in ${recovered}s, want <= 15s"
[ "$pages" = 1 ] || fail "found $pages pages for the job, want exactly 1"
pass "recovered in ${recovered}s; one Notion page"
