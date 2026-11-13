#!/usr/bin/env bash
# too-long.sh <url-over-20-min> — the job fails with VIDEO_TOO_LONG
# (Constraint A); the duration check runs before captions are fetched.
source "$(dirname "$0")/lib.sh"
[ $# -eq 1 ] || { echo "usage: $0 <youtube-url longer than 20 min>" >&2; exit 2; }

job=$(submit "$1")
echo "job $job submitted"
status=$(wait_terminal "$job" 120)
code=$(job_view "$job" | jq -r '.error.code // ""')
if [ "$status" != FAILED ] || [ "$code" != VIDEO_TOO_LONG ]; then
  fail "got $status ${code:-<no error code>}"
fi
pass "FAILED VIDEO_TOO_LONG ($(job_view "$job" | jq -r '.duration_sec')s video)"
