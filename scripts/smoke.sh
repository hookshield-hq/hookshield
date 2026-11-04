#!/usr/bin/env bash
# End-to-end smoke test of a running HookShield (Docker or native):
#   sign up + session check on the api → create an endpoint through the API
#   (pointing at the demo sink) → POST a webhook to ingest with the token the
#   API returned → wait until the sink receives it → print the result.
# Exits non-zero on any failure, so it can gate CI and releases.
#
# Environment:
#   SEED_CMD        command printing KEY=value lines (default: native `api seed --env`)
#   HS_API_URL      default http://localhost:8081
#   HS_INGEST_URL   default http://localhost:8080
#   HS_SINK_URL     default http://localhost:4000
#   SMOKE_TIMEOUT   seconds to wait for delivery (default 30)
set -euo pipefail

cd "$(dirname "$0")/.."

API=${HS_API_URL:-http://localhost:8081}
INGEST=${HS_INGEST_URL:-http://localhost:8080}
SINK=${HS_SINK_URL:-http://localhost:4000}
TIMEOUT=${SMOKE_TIMEOUT:-30}
SEED_CMD=${SEED_CMD:-"./scripts/run-native.sh api seed --env"}

fail() { echo "✗ smoke test failed: $*" >&2; exit 1; }

echo "• signing up a test account on $API…"
jar=$(mktemp); trap 'rm -f "$jar"' EXIT
email="smoke-$(date +%s)-$$@example.com"
code=$(curl -sS -o /dev/null -w '%{http_code}' -c "$jar" -H 'Origin: http://localhost:3000' -H 'Content-Type: application/json' \
  -d "{\"email\":\"$email\",\"password\":\"smoke test passphrase\"}" "$API/v1/auth/register") || fail "api unreachable at $API"
[ "$code" = "201" ] || fail "sign-up answered $code"
me=$(curl -sS -b "$jar" "$API/v1/me") || fail "api unreachable at $API"
printf '%s' "$me" | grep -q "\"email\":\"$email\"" || fail "session not recognized by /v1/me: $me"
echo "  account created and signed in ($email)"

echo "• seeding demo data…"
seed_out=$(eval "$SEED_CMD") || fail "seed command failed: $SEED_CMD"
HS_DEMO_TARGET=$(printf '%s\n' "$seed_out" | sed -n 's/^HS_DEMO_TARGET=//p')
[ -n "$HS_DEMO_TARGET" ] || fail "seed did not print the demo target"

echo "• creating an endpoint through the API (→ $HS_DEMO_TARGET)…"
created=$(curl -sS -w '\n%{http_code}' -b "$jar" -H 'Origin: http://localhost:3000' -H 'Content-Type: application/json' \
  -d "{\"name\":\"Smoke test\",\"target_url\":\"$HS_DEMO_TARGET\",\"retry\":{\"initial_interval_ms\":1000}}" \
  "$API/v1/endpoints") || fail "api unreachable at $API"
[ "$(printf '%s\n' "$created" | tail -1)" = "201" ] || fail "endpoint creation answered: $created"
created=$(printf '%s\n' "$created" | head -1)
HS_DEMO_SLUG=$(printf '%s' "$created" | sed -n 's/.*"slug":"\([^"]*\)".*/\1/p')
HS_DEMO_TOKEN=$(printf '%s' "$created" | sed -n 's/.*"token":"\(hs_in_[^"]*\)".*/\1/p')
HS_DEMO_HEADER=X-HookShield-Token
[ -n "$HS_DEMO_SLUG" ] && [ -n "$HS_DEMO_TOKEN" ] || fail "endpoint created without slug/token: $created"
echo "  endpoint $HS_DEMO_SLUG created, token shown once"

marker="smoke-$(date +%s)-$$"
body="{\"type\":\"smoke.test\",\"marker\":\"$marker\"}"

echo "• sending webhook to $INGEST/v1/$HS_DEMO_SLUG…"
resp=$(curl -sS -w '\n%{http_code}' -X POST "$INGEST/v1/$HS_DEMO_SLUG" \
  -H "$HS_DEMO_HEADER: $HS_DEMO_TOKEN" -H 'Content-Type: application/json' -d "$body") || fail "ingest unreachable at $INGEST"
code=$(printf '%s\n' "$resp" | tail -1)
[ "$code" = "202" ] || fail "ingest answered $code: $(printf '%s\n' "$resp" | head -1)"
id=$(printf '%s\n' "$resp" | head -1 | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
echo "  accepted, id $id"

echo "• waiting for delivery at $SINK (up to ${TIMEOUT}s)…"
start=$(date +%s)
while :; do
  stats=$(curl -fsS "$SINK/_sink/stats" 2>/dev/null || true)
  # A successful delivery of this webhook: same id and a 2xx answer from the sink.
  delivered=$(printf '%s' "$stats" | grep -oE "\"id\":\"$id\",\"attempt\":\"[0-9]+\",\"event\":\"[^\"]*\",\"status\":2[0-9][0-9]" || true)
  if [ -n "$delivered" ]; then
    attempt=$(printf '%s' "$delivered" | sed -n 's/.*"attempt":"\([0-9]*\)".*/\1/p')
    elapsed=$(( $(date +%s) - start ))
    echo "  delivered to the sink in ~${elapsed}s on attempt ${attempt}"
    break
  fi
  [ $(( $(date +%s) - start )) -lt "$TIMEOUT" ] || fail "webhook $id not delivered within ${TIMEOUT}s (check: make logs / worker output)"
  sleep 0.5
done

echo "• checking the log in the request debugger ($API/v1/logs/$id)…"
for _ in 1 2 3 4 5 6 7 8 9 10; do
  detail=$(curl -sS -b "$jar" "$API/v1/logs/$id") || fail "api unreachable at $API"
  printf '%s' "$detail" | grep -q '"status":"delivered"' && break
  sleep 0.5
done
printf '%s' "$detail" | grep -q '"status":"delivered"' || fail "log $id is not marked delivered: $detail"
printf '%s' "$detail" | grep -q "\"marker\":\"$marker\"" || fail "decrypted payload not shown by the debugger: $detail"
echo "✓ end to end OK: sign-up → endpoint via API → ingest → worker → sink → debugger (id $id, event smoke.test)"
