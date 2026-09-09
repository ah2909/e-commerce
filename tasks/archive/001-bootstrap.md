# Task: Bootstrap the Go service

## Context

Read `AGENTS.md`, `docs/architecture.md`, and `docs/testing-strategy.md`.

## Outcome

A new developer can start PostgreSQL, Redis, and Kafka locally, run the API, call liveness/readiness endpoints, and execute one verification command.

## In scope

- Initialize the Go module using the repository's chosen module path.
- Add `cmd/api` and `cmd/worker` entry points.
- Add typed environment configuration with startup validation.
- Add structured logging and graceful shutdown.
- Add `/health/live` and `/health/ready` endpoints.
- Add Docker Compose for PostgreSQL, Redis, and Kafka.
- Add Make targets for formatting, linting, unit/integration tests, vulnerability checks, and verification.
- Complete the existing `.github/workflows/ci.yml` backend contract: `make tools`, `make dev-deps`, the five verification targets, `make verify`, and `make dev-deps-down`.
- Pin linter and vulnerability scanner versions in `make tools`; keep local and CI tool versions identical.
- Make `dev-deps` wait for dependency readiness with a bounded timeout, and make integration tests use the documented environment variables.
- Make `dev-deps-down` clean up only this project's disposable local/CI dependencies.
- Add a small architecture skeleton without empty speculative layers.

## Out of scope

- Authentication
- Tenant middleware
- Business modules or database tables
- Kafka topics, producers, or consumers
- Kubernetes and cloud deployment

## Acceptance criteria

- [x] `make dev-deps` starts all local dependencies.
- [x] The API starts using documented environment variables.
- [x] Liveness reports process health.
- [x] Readiness fails when a required dependency is unavailable.
- [x] Shutdown honors cancellation and a bounded timeout.
- [x] `make verify` passes from a clean checkout.
- [x] The `Backend verification` CI job passes; required checks fail on errors or missing prerequisites.
- [x] Unit tests use the race detector where supported; integration tests execute against real PostgreSQL and fail if required dependencies are unavailable.
- [x] README documents setup and troubleshooting.
- [x] No credentials are committed.

## Prompt to give Codex

Historical invocation used before archival: `$ecommerce-delivery tasks/archive/001-bootstrap.md`. The task is now complete; do not restart it to authorize adjacent work.

## Delivery record

- Active task: no
- Execution mode: automatic
- Stage: complete
- Plan revision and approval: approved by the user on 2026-09-06
- Go module path: `github.com/ah2909/e-commerce`, supplied by the user on 2026-09-06
- Pending gates: none

### Proposed plan

1. Initialize patched Go 1.26.6 with module `github.com/ah2909/e-commerce`. Add only `pgx/v5` for PostgreSQL pooling and `go-redis/v9` for Redis connectivity; use the standard library for HTTP, configuration parsing, structured logging, signals, request IDs, and shutdown.
2. Add `cmd/api` and `cmd/worker`. Keep wiring in the commands and add cohesive platform packages only for typed configuration, dependency health checks, and HTTP lifecycle behavior. The worker remains an observable, gracefully stoppable process because Kafka producers/consumers are outside this task.
3. Validate environment configuration at startup, including supported log levels, addresses, URLs, nonempty Kafka brokers, and positive bounded durations. Return errors without exposing credentials.
4. Implement `GET /health/live` as process-only health and `GET /health/ready` as bounded concurrent PostgreSQL and Redis checks. Kafka will not gate API readiness because the architecture keeps asynchronous delivery outside the synchronous API path; Compose will still verify Kafka health.
5. Add structured JSON `slog` output, request/correlation ID propagation for HTTP requests, bounded server timeouts, and graceful shutdown using `SHUTDOWN_TIMEOUT`. Health responses expose dependency names and status without raw connection errors.
6. Add Docker Compose with project-scoped PostgreSQL 18.6, Redis 8.8.2, and single-node Kafka 4.3.1 in KRaft mode. Pin versioned image tags, add health checks, persist disposable development data in named volumes, and keep credentials limited to documented local defaults.
7. Add a cross-platform Makefile and pinned tool configuration. `make tools` installs `golangci-lint` v2.13.2 and `govulncheck` v1.7.0 into the repository's ignored `.tools` directory. Add `fmt`, `fmt-check`, `lint`, race-enabled `test`, `dev-deps`, `test-integration`, `vulncheck`, `verify`, and project-scoped `dev-deps-down` targets.
8. Complete the existing GitHub Actions backend job around those targets, including `make verify`, deterministic tool versions, dependency readiness, cleanup, read-only permissions, pinned actions, and failure propagation.
9. Document Windows/Linux setup, environment variables, startup, health behavior, verification, cleanup, and common dependency failures in the README.

### Verification plan

- Unit tests: configuration defaults/validation/redaction, liveness and readiness success/failure, request ID behavior, and bounded HTTP shutdown.
- Integration tests: real PostgreSQL and Redis connectivity through the readiness path, plus an unavailable dependency returning HTTP 503 without leaking its error.
- Compose validation and readiness: `docker compose config`, `make dev-deps`, service health inspection, and `make dev-deps-down` cleanup.
- Required final commands: `make fmt-check`, `make lint`, `make test`, `make test-integration`, `make vulncheck`, and `make verify`.
- Runtime smoke test: launch the API with documented configuration, call both health endpoints, interrupt it, and verify bounded clean shutdown. Launch and interrupt the worker as well.
- Independent gates: architect and security reviewers inspect the stable implementation in parallel; supported findings are fixed and affected reviewers re-check them before final verification.

### Design boundaries and risks

- No tenant-owned resources, schema migrations, business modules, topics, producers, consumers, authentication, or public `/api/v1` business endpoints are introduced.
- Operational health endpoints remain outside `/api/v1` and do not expose secrets or raw dependency errors.
- Kafka unavailability must not make the API unready; later worker/event tasks will define Kafka runtime readiness and retry behavior.
- Docker image tags and Go module/tool versions are pinned for reproducibility. Updating them remains a maintenance responsibility.
- The workspace is initialized on `main` and published to the private `ah2909/e-commerce` GitHub repository.

### Implementation and review results

- Local implementation, runtime smoke tests, and dependency failure checks completed on 2026-09-06.
- Architect review found three medium issues: readiness/write-timeout mismatch, incomplete port validation, and missing forced-shutdown coverage. All were fixed; architect re-review passed.
- Security review found one medium issue: disposable service ports were published on all host interfaces with automatic restart. The ports are now loopback-only with restart disabled; security re-review passed.
- Final `make verify` passed against PostgreSQL 18.6, Redis 8.8.2, and Kafka 4.3.1. `docker compose ps` showed all services healthy and bound only to `127.0.0.1`.
- The API returned HTTP 200 from liveness and readiness, HTTP 503 when Redis was unavailable, and logged a clean signal-driven stop. The worker also logged a clean signal-driven stop.
- Native Windows race testing was unavailable because CGO and GCC are absent. The Makefile enables `-race` on the Ubuntu CI runner and other supported non-Windows hosts.
- Disposable containers, volumes, and the Compose network were removed after verification.
- GitHub Actions run `34041968279` passed the clean Ubuntu backend verification and dependency cleanup in 5m31s on commit `e4eaccd`.
