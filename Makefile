.PHONY: run build test test-docker vet fmt fmt-check lint lint-fix vuln checkptr fuzz bench profile check swagger up-memory up-postgres down

DOCKER ?= docker
# Build the pinned linter with the project's Go toolchain.
LINT_CMD = go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0

run:
	go run ./cmd/api -storage=memory

build:
	go build -trimpath -o bin/api ./cmd/api

test:
	go test -race ./...

test-docker:
	DOCKER="$(DOCKER)" sh scripts/test-compose.sh

vet:
	go vet ./...

fmt:
	$(LINT_CMD) fmt ./...

fmt-check:
	$(LINT_CMD) fmt --diff ./...

lint:
	$(LINT_CMD) run

lint-fix:
	$(LINT_CMD) run --fix

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.4.0 ./...

checkptr:
	go test -gcflags=all=-d=checkptr=2 ./...

fuzz:
	go test ./internal/platform/config -run='^$$' -fuzz=FuzzLoad -fuzztime=10s

bench:
	go test ./... -run='^$$' -bench=. -benchmem -count=5

profile:
	mkdir -p bin
	go test ./internal/transport/http/middleware -run='^$$' -bench=BenchmarkWrap -benchmem -cpuprofile=bin/cpu.pprof -memprofile=bin/mem.pprof -o bin/middleware.test

profile-memory:
	mkdir -p bin
	go test ./internal/shortener/adapters/memory -run='^$$' -bench=BenchmarkStorage -benchmem -cpuprofile=bin/memory-cpu.pprof -memprofile=bin/memory-heap.pprof -o bin/memory.test

.PHONY: profile-memory

check: fmt-check vet lint test checkptr vuln build

swagger:
	go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g main.go -d cmd/api,internal/transport/http --parseInternal --output docs/swagger --outputTypes json,yaml

up-memory:
	$(DOCKER) compose --profile memory up --build -d

up-postgres:
	$(DOCKER) compose --profile postgres up --build -d

down:
	$(DOCKER) compose --profile memory --profile postgres down

# Keep the generator pinned; generated files are committed.
sqlc:
	go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.30.0 generate

# Applied separately from API startup; requires DATABASE_URL.
migrate-up:
	@test -n "$$DATABASE_URL" || (echo 'DATABASE_URL is required' >&2; exit 1)
	go run -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@v4.18.3 -path db/migrations -database "$$DATABASE_URL" up

migrate-down:
	@test -n "$$DATABASE_URL" || (echo 'DATABASE_URL is required' >&2; exit 1)
	go run -tags postgres github.com/golang-migrate/migrate/v4/cmd/migrate@v4.18.3 -path db/migrations -database "$$DATABASE_URL" down 1

test-postgres:
	DOCKER="$(DOCKER)" sh scripts/test-postgres.sh

.PHONY: sqlc migrate-up migrate-down test-postgres

bench-postgres:
	DOCKER="$(DOCKER)" sh scripts/test-postgres.sh -run='^$$' -bench=. -benchmem -count=5

.PHONY: bench-postgres
