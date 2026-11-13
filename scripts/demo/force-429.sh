#!/usr/bin/env bash
# force-429.sh [llm|notion|youtube] <url> — inject 429s at rate 0.7 for one
# provider, show the job waiting (not failing), then clear the fault and
# check the job still completes.
source "$(dirname "$0")/lib.sh"
[ $# -eq 2 ] || { echo "usage: $0 [llm|notion|youtube] <youtube-url>" >&2; exit 2; }
provider=$1
case "$provider" in
  llm|notion|youtube) ;;
  *) echo "provider must be llm, notion or youtube" >&2; exit 2 ;;
esac

trap reset_faults EXIT
set_faults "{\"${provider}_429_rate\":\"0.7\"}"
echo "fault on: ${provider}_429_rate=0.7"

job=$(submit "$2")
echo "job $job submitted"
start=$(date +%s)
seen=0
while [ "$(elapsed "$start")" -lt 300 ]; do
  view=$(job_view "$job")
  if [ "$(echo "$view" | jq -r '.waiting != null')" = true ]; then
    echo "$view" | jq -c '{status, waiting}'
    seen=1
    break
  fi
  if is_terminal "$(echo "$view" | jq -r .status)"; then break; fi
  sleep 1
done

reset_faults
echo "fault off"
status=$(wait_terminal "$job" 600)
[ "$seen" = 1 ] || fail "never saw the job waiting on a rate limit (final status $status)"
case "$status" in
  COMPLETED|COMPLETED_WITH_WARNINGS) ;;
  *) fail "job ended $status" ;;
esac
pass "job waited on ${provider} 429s, then $status"
