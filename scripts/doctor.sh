#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
# Git Bash on Windows rewrites /app/worker into a Windows path; turn that off.
export MSYS_NO_PATHCONV=1
exec docker compose -f deploy/docker-compose.yml --env-file .env exec -T worker /app/worker doctor
