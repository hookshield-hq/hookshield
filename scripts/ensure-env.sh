#!/usr/bin/env bash
# Creates .env from .env.example on first run and generates HS_ENCRYPTION_KEY
# when it is empty. Safe to run any number of times; never overwrites values.
# Portable: works with the bash 3.2 shipped with macOS and with BSD/GNU tools.
set -euo pipefail

cd "$(dirname "$0")/.."

if [ ! -f .env ]; then
  cp .env.example .env
  echo "• created .env from .env.example"
fi

current=$(grep -E '^HS_ENCRYPTION_KEY=' .env | head -1 | cut -d= -f2- || true)
if [ -z "$current" ]; then
  if command -v openssl >/dev/null 2>&1; then
    key=$(openssl rand -base64 32)
  else
    key=$(head -c 32 /dev/urandom | base64 | tr -d '\n')
  fi
  tmp=$(mktemp)
  if grep -qE '^HS_ENCRYPTION_KEY=' .env; then
    awk -v k="$key" '/^HS_ENCRYPTION_KEY=/{print "HS_ENCRYPTION_KEY=" k; next} {print}' .env > "$tmp"
  else
    cat .env > "$tmp"
    echo "HS_ENCRYPTION_KEY=$key" >> "$tmp"
  fi
  cat "$tmp" > .env
  rm -f "$tmp"
  echo "• generated HS_ENCRYPTION_KEY in .env (back it up: losing it makes stored payloads unreadable)"
fi
