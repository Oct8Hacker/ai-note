#!/usr/bin/env bash
# run-all.sh [--fresh] — one demo round (SRS §15.5, acceptance criterion 2):
# every demo script in presentation order, a PASS/FAIL summary at the end,
# and the full output in tmp/demo-round-<time>.log.
#
#   --fresh   wipe the stack first (docker compose down -v: deletes all jobs
#             and queues; Notion pages are kept), then start it with the demo
#             profile. Without it, the running stack is used as it is.
#
# Asks for the Notion token (hidden) unless NOTION_TEST_TOKEN is already set.
# Videos default to docs/test-videos.md; override with VIDEO_1..VIDEO_5.
# One round makes about 13 LLM jobs, two calls each (summary + frame verification).
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT" || exit 1
export MSYS_NO_PATHCONV=1
DEMO="$ROOT/scripts/demo"

V1="${VIDEO_1:-https://www.youtube.com/watch?v=RT-hUXUWQ2I}"    # ~6 min, slides
V2="${VIDEO_2:-https://www.youtube.com/watch?v=-4E2-0sxVUM}"    # ~11 min
V3="${VIDEO_3:-https://www.youtube.com/watch?v=BnJ013X02b8}"    # ~18 min
V_LONG="${VIDEO_4:-https://www.youtube.com/watch?v=6FkRvTtUc-o}" # > 20 min
V_GONE="${VIDEO_5:-https://www.youtube.com/watch?v=aaaaaaaaaaa}" # unavailable

compose() { docker compose -f deploy/docker-compose.yml --env-file .env "$@"; }

if [ -z "${NOTION_TEST_TOKEN:-}" ]; then
  if [ -t 0 ]; then
    printf 'Paste your Notion token (ntn_...) and press Enter. Input is hidden: '
    read -rs NOTION_TEST_TOKEN
    echo
  fi
  [ -n "${NOTION_TEST_TOKEN:-}" ] || { echo "NOTION_TEST_TOKEN is not set" >&2; exit 2; }
fi
export NOTION_TEST_TOKEN

mkdir -p tmp
log="tmp/demo-round-$(date +%Y%m%d-%H%M%S).log"

if [ "${1:-}" = "--fresh" ]; then
  echo "== wiping the stack (down -v) and starting it with the demo profile"
  compose down -v >>"$log" 2>&1
  compose --env-file deploy/demo.env up -d >>"$log" 2>&1 || { echo "compose up failed; see $log"; exit 1; }
  for _ in $(seq 1 60); do
    [ "$(curl -s localhost:8080/healthz | jq -r '.workers_alive // 0' 2>/dev/null)" = 2 ] && break
    sleep 3
  done
fi

worker=$(compose ps -q worker | head -1)
if [ "$(docker exec "$worker" printenv RETRY_DELAYS 2>/dev/null)" != "3s,6s,12s,24s" ]; then
  echo "WARNING: the demo profile isn't loaded, so dlq.sh and kill-worker.sh will likely fail."
  echo "         Rerun with --fresh, or start the stack with --env-file deploy/demo.env."
fi

results=()
run() {
  local name=$1 script=$2
  shift 2
  local start; start=$(date +%s)
  echo "== $name"
  echo "===== $name ($(date +%T))" >>"$log"
  local line
  line=$(bash "$DEMO/$script" "$@" 2>&1 | tee -a "$log" | grep -E '^(PASS|FAIL) ' | tail -1)
  [ -n "$line" ] || line="FAIL $script: no PASS/FAIL line (see log)"
  local took=$(( $(date +%s) - start ))
  echo "   $line  (${took}s)"
  results+=("$(printf '%-24s %4ss  %s' "$name" "$took" "$line")")
}

run "health"            health.sh
run "happy (video 1)"   happy.sh "$V1"
run "scale to 3"        scale.sh 3
run "scale to 2"        scale.sh 2
run "burst (6 jobs)"    burst.sh "$V1" "$V2" "$V3"
run "kill-worker"       kill-worker.sh "$V2"
run "force-429 llm"     force-429.sh llm "$V1"
run "force-429 notion"  force-429.sh notion "$V1"
run "force-429 youtube" force-429.sh youtube "$V1"
run "frames-fail"       frames-fail.sh "$V1"
run "too-long"          too-long.sh "$V_LONG"
run "dlq"               dlq.sh "$V_GONE"

echo
echo "================ summary ($(date '+%Y-%m-%d %H:%M')) ================"
printf '%s\n' "${results[@]}" | tee -a "$log"
fails=$(printf '%s\n' "${results[@]}" | grep -c ' FAIL ' || true)
echo "$(( ${#results[@]} - fails ))/${#results[@]} passed. Full output: $log"
[ "$fails" = 0 ]
