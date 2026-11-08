#!/bin/sh
set -eu

DOCKER=${DOCKER:-docker}
project="ozon-demo-check-$$"
records=$(mktemp)
export RELEASE=demo-check
export BIND_IP=127.0.0.1
export MEMORY_PORT=${DEMO_TEST_MEMORY_PORT:-28080}
export POSTGRES_PORT=${DEMO_TEST_POSTGRES_PORT:-28081}
export MEMORY_PUBLIC_BASE_URL="http://127.0.0.1:$MEMORY_PORT"
export POSTGRES_PUBLIC_BASE_URL="http://127.0.0.1:$POSTGRES_PORT"
POSTGRES_PASSWORD=$(openssl rand -hex 32)
export POSTGRES_PASSWORD

compose() {
  "$DOCKER" compose -p "$project" --env-file deploy/demo/.env.example --env-file deploy/demo/images.env -f deploy/demo/compose.yml "$@"
}

cleanup() {
  compose down --volumes
  rm -f "$records"
}
trap cleanup EXIT
trap 'exit 1' INT TERM

compose config --quiet
compose up --build -d --wait --wait-timeout 180
python3 scripts/check-demo.py --memory "$MEMORY_PUBLIC_BASE_URL" --postgres "$POSTGRES_PUBLIC_BASE_URL" --save "$records"
compose stop api-postgres api-memory postgres
compose up -d --no-build --force-recreate --wait --wait-timeout 180 postgres api-memory api-postgres
# The gateway resolves recreated API addresses through Docker DNS; expire its cache.
compose restart gateway
compose up -d --no-build --wait --wait-timeout 180
python3 scripts/check-demo.py --memory "$MEMORY_PUBLIC_BASE_URL" --postgres "$POSTGRES_PUBLIC_BASE_URL" --after-restart "$records"
