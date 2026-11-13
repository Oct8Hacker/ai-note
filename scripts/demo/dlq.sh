#!/usr/bin/env bash
# dlq.sh <private-url> — a private video exhausts its retries and is
# dead-lettered within 60 s (demo profile: RETRY_DELAYS=3s,6s,12s,24s), then
# shows up in GET /admin/dlq. Also visible in the RabbitMQ UI (localhost:15672).
source "$(dirname "$0")/lib.sh"
[ $# -eq 1 ] || { echo "usage: $0 <private youtube-url>" >&2; exit 2; }

start=$(date +%s)
job=$(submit "$1")
echo "job $job submitted"
status=$(wait_terminal "$job" 180)
took=$(elapsed "$start")
[ "$status" = DEAD_LETTERED ] || fail "got $status, want DEAD_LETTERED"

listed=$(admin_curl --fail-with-body "$GW/admin/dlq?limit=50" |
  jq --arg id "$job" '[.jobs[] | select(.job_id == $id)] | length')
[ "$listed" = 1 ] || fail "job not listed in /admin/dlq"
[ "$took" -le 60 ] || fail "dead-lettered after ${took}s (want <= 60s; is deploy/demo.env loaded?)"
pass "DEAD_LETTERED after ${took}s and listed in /admin/dlq"
