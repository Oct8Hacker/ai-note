#!/usr/bin/env bash
# health.sh — /healthz is 200, every check is ok, and at least MIN_WORKERS
# (default 2) workers are alive.
source "$(dirname "$0")/lib.sh"
min="${MIN_WORKERS:-2}"

resp=$(curl -sS "$GW/healthz" -w '\n%{http_code}') || fail "gateway not reachable at $GW"
code=${resp##*$'\n'}
body=${resp%$'\n'*}
echo "$body" | jq '{status, checks, workers_alive, provider_429_total, version}'
bad=$(echo "$body" | jq -r '.checks | to_entries[] | select(.value != "ok") | "\(.key)=\(.value)"')
alive=$(echo "$body" | jq -r '.workers_alive')

[ "$code" = 200 ] || fail "HTTP $code; failing checks: ${bad:-none}"
[ -z "$bad" ] || fail "failing checks: $bad"
[ "$alive" -ge "$min" ] || fail "workers_alive=$alive, want >= $min"
pass "all checks ok, workers_alive=$alive"
