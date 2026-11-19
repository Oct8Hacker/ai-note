#!/usr/bin/env bash
# setup.sh — create .env from .env.example and fill the values that can be
# generated (FR-SETUP-2). Safe to run again: it only fills values that are
# empty and never overwrites one you have set.
#
#   scripts/setup.sh             # uses ./.env
#   ENV_FILE=path scripts/setup.sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ENV_FILE="${ENV_FILE:-$ROOT/.env}"
EXAMPLE="$ROOT/.env.example"

if [ ! -f "$ENV_FILE" ]; then
  cp "$EXAMPLE" "$ENV_FILE"
  echo "created $ENV_FILE from .env.example"
fi

# current KEY — prints KEY's value in the env file (empty if unset or missing).
current() { grep -E "^$1=" "$ENV_FILE" | tail -1 | cut -d= -f2- || true; }

# fill KEY VALUE — set KEY only if it is empty or missing. Rewrites the file
# with awk instead of sed -i, which differs between GNU and macOS.
fill() {
  local key=$1 value=$2 tmp
  [ -z "$(current "$key")" ] || return 0
  if grep -qE "^$key=" "$ENV_FILE"; then
    tmp="$ENV_FILE.tmp.$$"
    KEY="$key" VALUE="$value" awk '
      BEGIN { k = ENVIRON["KEY"] "=" }
      index($0, k) == 1 && length($0) == length(k) { print k ENVIRON["VALUE"]; next }
      { print }' "$ENV_FILE" > "$tmp"
    mv "$tmp" "$ENV_FILE"
  else
    printf '%s=%s\n' "$key" "$value" >> "$ENV_FILE"
  fi
  echo "set $key"
}

random_base64() {
  if command -v openssl >/dev/null; then openssl rand -base64 32
  else head -c 32 /dev/urandom | base64 | tr -d '\n'; fi
}
random_hex() {
  if command -v openssl >/dev/null; then openssl rand -hex 24
  else head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n'; fi
}

fill TOKEN_ENC_KEY "$(random_base64)"
fill ADMIN_TOKEN "$(random_hex)"

provider=$(current LLM_PROVIDER)
provider=${provider:-gemini}
case "$provider" in
  gemini) key_var=GEMINI_API_KEY ;;
  groq) key_var=GROQ_API_KEY ;;
  openrouter) key_var=OPENROUTER_API_KEY ;;
  *) key_var="" ;;
esac
if [ -n "$key_var" ] && [ -z "$(current "$key_var")" ]; then
  if [ -t 0 ]; then
    printf '%s (input hidden, Enter to skip): ' "$key_var"
    read -rs api_key
    echo
    if [ -n "$api_key" ]; then fill "$key_var" "$api_key"; fi
  fi
  [ -n "$(current "$key_var")" ] || echo "note: $key_var is empty; add it to $ENV_FILE before starting the stack"
fi

cat <<EOF

Next steps:
  1. docker compose -f deploy/docker-compose.yml --env-file .env up -d
  2. curl http://localhost:8080/healthz          (wait for "status": "ok")
  3. scripts/doctor.sh                           (checks the LLM key, yt-dlp, ffmpeg, YouTube)
  4. Load the extension and connect Notion       (README → Quick start)
EOF
