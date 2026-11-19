#!/usr/bin/env bash
# burst.sh <url> [url...] — 6 jobs back to back (URLs are reused in turn if
# fewer than 6 are given). PASS if all complete and provider_429_total did
# not change: the shared Redis limiter kept every provider under its limit.
source "$(dirname "$0")/lib.sh"
[ $# -ge 1 ] || { echo "usage: $0 <youtube-url> [more urls...]   (each <= 20 min)" >&2; exit 2; }
urls=("$@")

before=$(provider_429_total)
echo "provider_429_total before: $before"
ids=()
for i in 0 1 2 3 4 5; do
  ids+=("$(submit "${urls[$((i % ${#urls[@]}))]}")")
  echo "submitted ${ids[$i]}"
done

failed=0
for job in "${ids[@]}"; do
  status=$(wait_terminal "$job" 1800)
  case "$status" in
    COMPLETED|COMPLETED_WITH_WARNINGS) ;;
    *) failed=$((failed + 1)) ;;
  esac
done
after=$(provider_429_total)
echo "provider_429_total after: $after"

[ "$failed" = 0 ] || fail "$failed of 6 jobs did not complete"
[ "$after" = "$before" ] || fail "provider_429_total went $before -> $after"
pass "6/6 completed, provider_429_total unchanged ($after)"
