#!/usr/bin/env bash
# Runs a backend binary natively with the variables from .env loaded,
# e.g. ./scripts/run-native.sh api seed --env
set -euo pipefail
cd "$(dirname "$0")/.."
./scripts/ensure-env.sh >&2
set -a
# shellcheck disable=SC1091
. ./.env
set +a
bin=$1; shift
cd backend
exec go run "./cmd/$bin" "$@"
