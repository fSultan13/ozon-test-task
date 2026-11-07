#!/bin/sh
set -eu
DOCKER=${DOCKER:-docker}
container="ozon-postgres-test-$$"
cleanup() { "$DOCKER" rm -f "$container" >/dev/null; }
trap cleanup EXIT

trap 'exit 1' INT TERM

"$DOCKER" run -d --name "$container" -e POSTGRES_PASSWORD=test -p 127.0.0.1::5432 postgres:17-alpine >/dev/null
attempt=0
until "$DOCKER" exec "$container" pg_isready -U postgres >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 30 ]; then echo 'PostgreSQL did not start' >&2; exit 1; fi
  sleep 1
done

# Apply, roll back and reapply to check both migration directions.
for migration in db/migrations/000001_create_links.up.sql db/migrations/000001_create_links.down.sql db/migrations/000001_create_links.up.sql; do
  "$DOCKER" exec -i "$container" psql -U postgres -v ON_ERROR_STOP=1 -1 < "$migration" >/dev/null
done

port=$("$DOCKER" port "$container" 5432/tcp)
export TEST_DATABASE_URL="postgres://postgres:test@${port}/postgres?sslmode=disable"
if [ "$#" -eq 0 ]; then
  set -- -race -count=3
fi

go test "$@" ./internal/shortener/adapters/postgres
