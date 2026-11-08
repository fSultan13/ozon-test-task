# Repository Guidelines

## Scope and requirements

- This repository contains a Go URL shortener. Read `TASK.md` for the assignment and inspect the relevant implementation, tests, and `README.md` before changing behavior.
- Implement or change business logic only when the user requests it. Keep changes focused on the requested outcome; avoid unrelated refactoring and speculative features.
- Preserve the established HTTP contract, error format, and GET behavior. Ask before implementing requirements that leave these decisions undefined or contradictory; do not invent a contract.
- Preserve unrelated working-tree changes. Do not commit, push, or publish unless the user explicitly requests it.

## Architecture and dependencies

Use the existing layout, following the trade-service architecture:

```text
internal/<module>/domain/       Domain types and invariants
internal/<module>/ports/        Dependency contracts
internal/<module>/application/  Use cases
internal/<module>/adapters/     Port implementations and module HTTP handlers
internal/platform/             Infrastructure and configuration
internal/app/                  Explicit dependency wiring and lifecycle
internal/transport/http/       Shared routing and HTTP middleware
```

- Keep `domain` independent of HTTP, SQL, and infrastructure. Application code uses domain types and ports; adapters implement ports. Wire concrete dependencies explicitly in `internal/app`.
- Use Go's standard `net/http` and `http.ServeMux`; do not introduce an API framework.
- Memory and PostgreSQL adapters must implement the same storage port and preserve its semantics. Select the adapter in `app`; do not branch on storage mode in business logic.
- Prefer small, concrete types and interfaces justified by their callers. Do not introduce generic repositories, dependency containers, Unit of Work, or shared models without a demonstrated need.
- Remove meaningful duplication, but do not merge code solely because it looks similar.

## Persistence and generated files

- Keep migrations in `db/migrations`, SQL queries in `db/query/<module>`, and generated sqlc code in `internal/<module>/adapters/postgres/sqlc`.
- Change SQL sources and run `make sqlc`; never edit generated code manually.
- Keep storage invariants atomic under concurrent access. PostgreSQL uniqueness must be enforced by database constraints, not only by application checks.
- Update Swagger from handler annotations with `make swagger`. Document only implemented routes and keep the specification aligned with actual behavior.

## Runtime behavior and data protection

- Propagate `context.Context` through use cases and dependencies. Bound external operations with timeouts and respect earlier caller deadlines.
- Close acquired resources on initialization failures and during shutdown. Preserve the existing shutdown order and resource ownership.
- Use `log/slog`. Never log DSNs, secrets, request bodies, original URLs, or shortened URLs, including through wrapped errors or diagnostic fields.
- Add useful context to internal errors while preserving error identity where callers need it. Return only the established public error details to clients.
- Do not add authentication, CORS, or rate limiting without requirements.
- Bound resource usage: database connections, request body sizes, and operation durations.
- Before expanding memory storage, clarify data lifetime and capacity requirements. Do not introduce arbitrary deletion, TTL, or eviction, or change the existing capacity policy without agreement.

## Tests and performance

- Add meaningful tests for new behavior and regression tests for fixes. Assert observable behavior and invariants rather than duplicating implementation details.
- Cover relevant failure paths, cancellation, and concurrency. Test concurrent memory access and PostgreSQL uniqueness, including conflicting writes.
- Aim for high throughput under concurrent load while preserving correctness and bounded resource use.
- Justify performance changes with representative benchmarks using `-benchmem`, repeated measurements, and CPU/heap profiles as appropriate.
- Before changing struct layout or optimizing CPU cache locality, measure the workload and the size of hot data. Do not change semantics based on cache assumptions.
- Do not introduce `unsafe`, `sync.Pool`, sharding, or lock-free structures without measured necessity.
- Do not infer HTTP RPS or supported user counts from microbenchmarks. Agree on a load profile and acceptance criteria before making service-level performance claims.

## Verification and delivery

- Before finishing, run `make check`: formatting, `go vet`, golangci-lint, race tests, checkptr, govulncheck, and build.
- For configuration changes, also run `make fuzz`.
- For hot-path changes, also run `make bench`; use `make profile` or `make profile-memory` when profiling is needed.
- For PostgreSQL behavior or migration changes, run `make test-postgres`. For container startup or Compose changes, run `make test-docker`.
- Inspect the final diff for unintended changes. Keep affected documentation and generated artifacts consistent with the implementation.
- Report what changed, which checks passed, and any checks that failed, were skipped, or could not run. Do not describe skipped integration tests as verified behavior.
