#!/usr/bin/env bash
# frames-fail.sh <url> — frame_fail_rate=0.5 gives COMPLETED_WITH_WARNINGS;
# then frame_fail_all=true gives a text-only page (frames_published == 0).
source "$(dirname "$0")/lib.sh"
[ $# -eq 1 ] || { echo "usage: $0 <youtube-url>" >&2; exit 2; }
trap reset_faults EXIT

result() { job_view "$1" | jq -c '.result | {frames_requested, frames_published, notion_page_url}'; }

set_faults '{"frame_fail_rate":"0.5"}'
echo "== frame_fail_rate=0.5"
job=$(submit "$1")
s1=$(wait_terminal "$job" 600)
echo "  $(result "$job")"
[ "$s1" = COMPLETED_WITH_WARNINGS ] || fail "with frame_fail_rate=0.5 got $s1"

reset_faults
set_faults '{"frame_fail_all":"true"}'
echo "== frame_fail_all=true"
job=$(submit "$1")
s2=$(wait_terminal "$job" 600)
r2=$(result "$job")
echo "  $r2"
published=$(echo "$r2" | jq -r '.frames_published // -1')
if [ "$s2" != COMPLETED_WITH_WARNINGS ] || [ "$published" != 0 ]; then
  fail "with frame_fail_all got $s2, frames_published=$published"
fi
pass "partial failure -> warnings; total failure -> text-only page"
