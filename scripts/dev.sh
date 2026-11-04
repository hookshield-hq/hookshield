#!/usr/bin/env bash
# Native development mode: builds the Go binaries and runs api, ingest, worker
# and the demo sink in this terminal, with prefixed, colored logs.
# Ctrl+C stops everything.
#
# Needs Go 1.24+ and PostgreSQL + Redis reachable through .env
# (`make dev-up` starts them with Docker). Portable to macOS bash 3.2.
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT=$(pwd)

./scripts/ensure-env.sh

set -a
# shellcheck disable=SC1091
. ./.env
set +a

echo "• building binaries…"
(cd backend && go build -o "$ROOT/bin/" ./cmd/...)

PIDS=""
cleanup() {
  trap - INT TERM EXIT
  echo
  echo "• stopping…"
  # PIDS holds the service processes themselves, so nothing is left behind
  # even if a log prefixer died first.
  for pid in $PIDS; do kill -TERM "$pid" 2>/dev/null || true; done
  wait $PIDS 2>/dev/null || true
}
trap cleanup INT TERM EXIT

# start NAME COLOR CMD... — runs CMD in the background with every output line
# prefixed by NAME; $! is the service itself (the prefixer is a process substitution).
start() {
  name=$1; color=$2; shift 2
  "$@" > >(while IFS= read -r line; do printf '\033[%sm%-7s\033[0m %s\n' "$color" "$name" "$line"; done) 2>&1 &
  PIDS="$PIDS $!"
}

wait_http() {
  url=$1; tries=0
  until curl -fsS "$url" >/dev/null 2>&1; do
    tries=$((tries + 1))
    if [ "$tries" -gt 60 ]; then echo "✗ $url did not become healthy" >&2; exit 1; fi
    sleep 0.5
  done
}

API_PORT=${HS_API_ADDR##*:}
INGEST_PORT=${HS_INGEST_ADDR##*:}
WORKER_PORT=${HS_WORKER_HEALTH_ADDR##*:}
SINK_ADDR=${HS_SINK_ADDR:-:4000}

start api 36 ./bin/api
wait_http "http://localhost:${API_PORT}/healthz"   # migrations are applied before the api listens
start ingest 32 ./bin/ingest
start worker 35 ./bin/worker
start sink 33 ./bin/hookshield sink --addr "$SINK_ADDR" --fail-first "${HS_SINK_FAIL_FIRST:-0}"

wait_http "http://localhost:${INGEST_PORT}/readyz"
wait_http "http://localhost:${WORKER_PORT}/readyz"

cat <<EOF

  HookShield is running (native mode)
    ingest   http://localhost:${INGEST_PORT}/v1/{slug}
    api      http://localhost:${API_PORT}
    worker   http://localhost:${WORKER_PORT}/readyz
    sink     http://localhost${SINK_ADDR}   (demo destination — deliveries are printed below;
             HS_SINK_FAIL_FIRST=N make dev makes it fail the first N requests to show retries)

  In another terminal:  make smoke   (seed + send a webhook + check it was delivered)
  Ctrl+C to stop.

EOF

wait
