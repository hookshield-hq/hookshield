#!/usr/bin/env bash
# Alert end-to-end demo/test: sends a webhook to the always-failing demo
# endpoint (ep_failing) and waits until the alert email shows up in Mailpit.
# Exits non-zero on failure (used in CI).
#
# Environment:
#   SEED_CMD        command printing KEY=value lines (default: native `api seed --env`)
#   HS_INGEST_URL   default http://localhost:8080
#   MAILPIT_URL     default http://localhost:8025
#   ALERT_TIMEOUT   seconds to wait (default 30)
set -euo pipefail

cd "$(dirname "$0")/.."

INGEST=${HS_INGEST_URL:-http://localhost:8080}
MAILPIT=${MAILPIT_URL:-http://localhost:8025}
TIMEOUT=${ALERT_TIMEOUT:-30}
SEED_CMD=${SEED_CMD:-"./scripts/run-native.sh api seed --env"}

fail() { echo "✗ alert demo failed: $*" >&2; exit 1; }

total() { curl -fsS "$MAILPIT/api/v1/messages?limit=1" 2>/dev/null | sed -n 's/.*"total":\([0-9]*\).*/\1/p'; }

before=$(total) || true
[ -n "$before" ] || fail "Mailpit is not reachable at $MAILPIT (make up / make dev-up)"

echo "• seeding demo data…"
seed_out=$(eval "$SEED_CMD") || fail "seed command failed: $SEED_CMD"
SLUG=$(printf '%s\n' "$seed_out" | sed -n 's/^HS_FAILING_SLUG=//p')
TOKEN=$(printf '%s\n' "$seed_out" | sed -n 's/^HS_FAILING_TOKEN=//p')
[ -n "$TOKEN" ] || fail "seed did not print HS_FAILING_TOKEN"

echo "• sending a webhook to $SLUG (its destination always refuses the connection)…"
code=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$INGEST/v1/$SLUG" \
  -H "X-HookShield-Token: $TOKEN" -H 'Content-Type: application/json' -d '{"type":"payment.failed","demo":true}') \
  || fail "ingest unreachable at $INGEST"
[ "$code" = "202" ] || fail "ingest answered $code"

echo "• waiting for the alert email in Mailpit (up to ${TIMEOUT}s)…"
start=$(date +%s)
while :; do
  now=$(total || echo "$before")
  if [ "${now:-0}" -gt "$before" ]; then
    subject=$(curl -fsS "$MAILPIT/api/v1/messages?limit=1" | sed -n 's/.*"Subject":"\([^"]*\)".*/\1/p')
    echo "✓ alert email received: $subject"
    echo "  open $MAILPIT to read it"
    exit 0
  fi
  [ $(( $(date +%s) - start )) -lt "$TIMEOUT" ] || fail "no alert email within ${TIMEOUT}s (check the worker logs)"
  sleep 0.5
done
