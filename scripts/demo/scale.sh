#!/usr/bin/env bash
# scale.sh N — run N worker replicas and wait until /healthz reports
# workers_alive == N (heartbeats expire after WORKER_HEARTBEAT_TTL, 6 s).
source "$(dirname "$0")/lib.sh"
if [ $# -ne 1 ] || ! [ "$1" -ge 1 ] 2>/dev/null; then
  echo "usage: $0 N   (N >= 1)" >&2
  exit 2
fi
n=$1

compose up -d --no-recreate --scale "worker=$n" worker
start=$(date +%s)
while :; do
  alive=$(health_json | jq -r '.workers_alive // 0')
  echo "  [$(elapsed "$start")s] workers_alive=$alive"
  [ "$alive" != "$n" ] || pass "workers_alive=$n"
  [ "$(elapsed "$start")" -lt 60 ] || fail "workers_alive=$alive after 60s, want $n"
  sleep 2
done
