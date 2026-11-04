#!/usr/bin/env sh
# Creates .env for deploy/docker-compose.yml: your domain and a random
# database password. Run once, then: docker compose up -d
#
#   ./setup.sh hooks.example.com
set -eu
cd "$(dirname "$0")"

if [ -f .env ]; then
  echo "✗ .env already exists — edit it instead (deleting it would lose the database password)." >&2
  exit 1
fi

domain=${1:-}
if [ -z "$domain" ]; then
  printf 'Domain for HookShield (DNS must point to this server), e.g. hooks.example.com: '
  read -r domain
fi
domain=$(printf '%s' "$domain" | sed -e 's#^https\{0,1\}://##' -e 's#/.*$##')
if [ -z "$domain" ]; then
  echo "✗ a domain is required" >&2
  exit 1
fi

password=$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')

umask 077
cat > .env <<ENV
# HookShield production settings (docs/SELF_HOSTING.md, docs/CONFIGURATION.md).
# Keep this file private. NEVER commit it.

HS_DOMAIN=$domain
HS_POSTGRES_PASSWORD=$password
# Image version (a release like 1.2.0, or latest)
HS_VERSION=latest

# ── Email (account emails and alert emails) ───────────────
# Resend: create an API key and verify the domain of HS_EMAIL_FROM.
HS_RESEND_API_KEY=
HS_EMAIL_FROM="HookShield <no-reply@$domain>"
# Or any SMTP server instead of Resend:
# HS_SMTP_URL=smtp://user:password@smtp.example.com:587

# ── Google sign-in (optional) ─────────────────────────────
# Redirect URI: https://$domain/v1/auth/google/callback
GOOGLE_CLIENT_ID=
GOOGLE_CLIENT_SECRET=

# ── Encryption key ────────────────────────────────────────
# Empty = generated on the first start into the "keys" volume. Back it up:
#   docker compose exec api cat /var/lib/hookshield/encryption.key
# HS_ENCRYPTION_KEY=

# How long webhook logs are kept
HS_RETENTION=720h
ENV

echo "✓ .env written for https://$domain"
echo "  next: docker compose up -d   (then open https://$domain and create your account)"
