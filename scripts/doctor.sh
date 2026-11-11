#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
exec docker compose -f deploy/docker-compose.yml --env-file .env exec -T worker /app/worker doctor
