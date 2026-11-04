#!/usr/bin/env sh
# Restores a dump made by backup.sh into the running stack, replacing the
# current data. Services are stopped during the restore.
#
#   ./restore.sh backups/hookshield-20261103T030000Z.dump
#
# The install must use the same encryption key as when the dump was made
# (HS_ENCRYPTION_KEY in .env, or the key file in the "keys" volume) — the
# services refuse to start with another key.
set -eu
cd "$(dirname "$0")"

file=${1:?usage: ./restore.sh <file.dump>}
[ -f "$file" ] || { echo "✗ $file not found" >&2; exit 1; }

printf 'This replaces ALL current HookShield data with %s. Type "restore" to continue: ' "$file"
read -r answer
[ "$answer" = "restore" ] || { echo "aborted"; exit 1; }

# Redis key prefix (HS_REDIS_PREFIX, default "hs:"): from the environment, else from ./.env.
prefix=${HS_REDIS_PREFIX:-$(sed -n 's/^HS_REDIS_PREFIX=//p' .env 2>/dev/null | tail -n 1 | tr -d "\"'")}
prefix=${prefix:-hs:}

# If anything below fails, bring the services back up: --single-transaction
# rolls the database back to its state before the restore.
stopped=0
on_exit() {
  status=$?
  if [ "$stopped" = 1 ]; then
    echo "✗ restore failed — nothing was changed; restarting the services" >&2
    docker compose start api ingest worker || true
  fi
  exit "$status"
}
trap on_exit EXIT
trap 'exit 130' INT TERM

stopped=1
docker compose stop api ingest worker
docker compose exec -T postgres pg_restore -U hookshield -d hookshield --clean --if-exists --no-owner --single-transaction < "$file"
# The ingest caches endpoints in Redis: drop the cached copies so they are
# reloaded from the restored database.
docker compose exec -T redis sh -c 'redis-cli --scan --pattern "$1" | xargs -r -n 100 redis-cli DEL >/dev/null' sh "${prefix}cache:ep:*"
stopped=0
docker compose start api ingest worker
echo "✓ restored $file"
