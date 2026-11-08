#!/bin/sh
set -eu

DOCKER=${DOCKER:-docker}
project="ozon-scaffold-check-$$"

compose() {
  "$DOCKER" compose -p "$project" "$@"
}

cleanup() {
  compose --profile memory --profile postgres down --volumes --remove-orphans
}
trap cleanup EXIT
trap 'exit 1' INT TERM

compose --profile memory --profile postgres config --quiet
compose --profile memory up --build --wait --wait-timeout 90
response=$(compose exec -T api-memory wget -q -O - http://127.0.0.1:8080/readyz)
test "$response" = '{"status":"ok"}'
compose exec -T api-memory test -s /app/data/links.db
container=$(compose ps -q api-memory)
compose stop api-memory
test "$("$DOCKER" inspect --format '{{.State.ExitCode}}' "$container")" = 0
compose logs api-memory | grep -q 'http server stopped'
compose --profile memory up -d --no-deps --wait --wait-timeout 90 api-memory
response=$(compose exec -T api-memory wget -q -O - http://127.0.0.1:8080/readyz)
test "$response" = '{"status":"ok"}'
compose stop api-memory

compose --profile postgres up --build --wait --wait-timeout 90
response=$(compose exec -T api-postgres wget -q -O - http://127.0.0.1:8080/readyz)
test "$response" = '{"status":"ok"}'
compose stop postgres
if compose exec -T api-postgres wget -q -O /dev/null http://127.0.0.1:8080/readyz; then
  echo 'Readiness unexpectedly succeeded without PostgreSQL' >&2
  exit 1
fi
response=$(compose exec -T api-postgres wget -q -O - http://127.0.0.1:8080/healthz)
test "$response" = '{"status":"ok"}'
# Use up: older Compose v2 releases support --wait for up, but not start.
compose --profile postgres up -d --no-deps --wait --wait-timeout 90 postgres
response=$(compose exec -T api-postgres wget -q -O - http://127.0.0.1:8080/readyz)
test "$response" = '{"status":"ok"}'
container=$(compose ps -q api-postgres)
compose stop api-postgres
test "$("$DOCKER" inspect --format '{{.State.ExitCode}}' "$container")" = 0
compose logs api-postgres | grep -q 'http server stopped'
echo 'PASS: memory restart, postgres, readiness failure/recovery, liveness, graceful shutdown'
