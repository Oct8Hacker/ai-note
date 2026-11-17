#!/usr/bin/env bash
# kill-worker.sh <url> — SIGKILL the worker holding a job in
# EXTRACTING_FRAMES. Another worker must take the job over within 15 s (it
# holds the job's lease and resumes the stage from the last checkpoint), and
# the job must end with exactly one Notion page.
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
lease_owner() {
  compose exec -T postgres psql -U "${pg_user:-notes}" -d "${pg_db:-notes}" -tAc \
    "SELECT coalesce(lease_owner, '') FROM jobs WHERE id = '$job'" | tr -d '[:space:]'
}
owner=$(lease_owner)
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

# Taken over = another worker now holds the job's lease. It can claim the job
# once the dead worker's heartbeat key expires (WORKER_HEARTBEAT_TTL, 6 s).
recovered=""
new_owner=""
while [ "$(elapsed "$killed")" -lt 120 ]; do
  new_owner=$(lease_owner)
  if [ -n "$new_owner" ] && [ "$new_owner" != "$owner" ]; then
    recovered=$(elapsed "$killed")
    break
  fi
  if is_terminal "$(job_status "$job")"; then recovered=$(elapsed "$killed"); break; fi
  sleep 0.5
done
[ -n "$recovered" ] || fail "no other worker took the job within 120s of the kill"
echo "worker ${new_owner:-?} took over in ${recovered}s"
# Progress moves again once the new worker finishes its next frame.
while [ "$(job_view "$job" | jq -r .progress)" -le "$before" ] && [ "$(elapsed "$killed")" -lt 120 ]; do
  sleep 0.5
done
echo "progress moved past $before% after $(elapsed "$killed")s"
docker start "$container" >/dev/null || true

status=$(wait_terminal "$job" 900)
case "$status" in
  COMPLETED|COMPLETED_WITH_WARNINGS) ;;
  *) fail "job ended $status" ;;
esac

# Notion's search index lags behind page creation, so search again until the
# page shows up, then once more to catch a late duplicate.
short=${job:0:6}
count_pages() {
  notion_curl --fail-with-body "$GW/notion/pages" |
    jq --arg s "· $short" '[.pages[] | select(.title | endswith($s))] | length'
}
searched=$(date +%s)
pages=$(count_pages)
while [ "$pages" = 0 ] && [ "$(elapsed "$searched")" -lt 90 ]; do
  sleep 5
  pages=$(count_pages)
done
sleep 5
pages=$(count_pages)
echo "Notion pages for job $short: $pages"
[ "$recovered" -le 15 ] || fail "taken over in ${recovered}s, want <= 15s"
[ "$pages" = 1 ] || fail "found $pages pages for the job, want exactly 1"
pass "taken over in ${recovered}s; one Notion page"
