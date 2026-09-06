# Multi-store E-commerce Backend

Codex-ready foundation for a configurable multi-store e-commerce backend written in Go.

## Product goal

Build one backend that serves many independent stores. Each store can configure its catalog, prices, inventory, checkout, payment, shipping, and staff access without exposing data to another store.

## Initial architecture

- Go modular monolith
- PostgreSQL as the system of record
- Redis for cache, rate limiting, and short-lived coordination
- Kafka for asynchronous side effects
- Transactional outbox for reliable event publication
- REST API documented with OpenAPI
- Docker Compose for local dependencies

## Local development

Prerequisites:

- Go 1.26.6 or newer compatible patch release
- GNU Make
- Docker with Compose

Start the disposable dependencies and wait for them to become healthy:

```bash
make dev-deps
```

Compose publishes PostgreSQL, Redis, and Kafka only on the host loopback interface and does not restart them automatically.

Start the API:

```bash
make run-api
```

The Makefile supplies the development defaults from `.env.example`. Direct `go run` commands require those environment variables to be exported first. Check the service from another terminal:

```bash
curl -i http://localhost:8080/health/live
curl -i http://localhost:8080/health/ready
```

Liveness reports whether the API process can serve requests. Readiness checks PostgreSQL and Redis with a bounded timeout and returns HTTP 503 if either is unavailable. It never includes raw dependency errors. Kafka is asynchronous infrastructure and does not gate API readiness.

The worker entry point currently exercises configuration, structured logging, and signal-driven shutdown; event processing is a later roadmap task:

```bash
make run-worker
```

Stop either process with Ctrl+C. Stop the dependencies and delete only this Compose project's disposable volumes with:

```bash
make dev-deps-down
```

## Configuration

| Variable | Development default | Purpose |
| --- | --- | --- |
| `APP_ENV` | `development` | Runtime environment label used in logs. |
| `HTTP_ADDRESS` | `:8080` | API listen address. |
| `LOG_LEVEL` | `debug` | `slog` level such as `debug`, `info`, `warn`, or `error`. |
| `DATABASE_URL` | Local PostgreSQL URL | PostgreSQL connection URL; required outside Make defaults. |
| `REDIS_ADDRESS` | `localhost:6379` | Redis host and port; required outside Make defaults. |
| `KAFKA_BROKERS` | `localhost:9092` | Comma-separated Kafka brokers; required outside Make defaults. |
| `SHUTDOWN_TIMEOUT` | `10s` | Maximum graceful API shutdown time; must be positive and no more than two minutes. |
| `DEPENDENCY_TIMEOUT` | `2s` | Readiness-check deadline; must be positive and no more than 30 seconds. |

`.env.example` contains disposable local credentials only. Keep production credentials in the deployment environment and out of this repository.

## Verification

With the dependencies running, execute the complete local gate:

```bash
make verify
```

It installs pinned tools under the ignored `.tools` directory, checks formatting and lint, runs unit and PostgreSQL/Redis integration tests, and scans reachable Go code for known vulnerabilities. On supported non-Windows hosts, tests use the Go race detector. Run the verification target in WSL or CI when a native Windows C toolchain for `-race` is unavailable.

Individual targets are `make fmt-check`, `make lint`, `make test`, `make test-integration`, and `make vulncheck`. Use `make fmt` to apply Go formatting.

## Troubleshooting

- If `make dev-deps` cannot connect to Docker, start Docker Desktop and retry.
- If a container cannot bind port 5432, 6379, or 9092, stop the conflicting local service or change both Compose and the matching connection variable.
- If readiness returns HTTP 503, run `docker compose ps` and inspect the unhealthy PostgreSQL or Redis container with `docker compose logs <service>`.
- If old disposable data prevents a clean start, run `make dev-deps-down` before `make dev-deps`.
- If `make verify` cannot download tools or vulnerability data, restore network access and rerun the failed target; do not treat that check as passing.

Read these files before implementation:

1. `AGENTS.md`
2. `docs/product-requirements.md`
3. `docs/architecture.md`
4. `docs/domain-model.md`
5. `docs/testing-strategy.md`
6. `docs/implementation-roadmap.md`

## Project workflow with Codex

1. Select one roadmap slice.
2. Copy `tasks/TEMPLATE.md` to a new task file.
3. Fill in scope, acceptance criteria, and invariants.
4. Ask: `Use $ecommerce-delivery for tasks/001-bootstrap.md. Clarify requirements and propose the plan first.`
5. Approve the plan, including its technical solution and test strategy.
6. Codex implements, runs independent architect and security reviews in parallel, fixes findings, and completes testing automatically.
7. Inspect the completion evidence before merging.

See [the delivery workflow](docs/development-workflow.md) for approval rules, review gates, CI setup, and bootstrap limitations. This workflow is configured only inside this project.

For stage-by-stage control, send one command at a time:

| Command | Result |
| --- | --- |
| `$ec-plan tasks/001-bootstrap.md` | Prepare the plan and stop for inspection. |
| `$ec-build` | After plan approval, implement and run focused tests, then stop. |
| `$ec-review` | Run architect/security reviews in parallel and report findings. |
| `$ec-fix` | Fix findings and run regression tests, then stop. |
| `$ec-test` | Run final checks and report results without repairing code. |

Task paths are optional after a task is unambiguously selected. Approve the plan before `$ec-build`. After fixes, use `$ec-review` again to verify them before completion. Each stage command stops without automatically starting the next stage; approving the plan alone also preserves that behavior. Use `$ecommerce-delivery` to explicitly return the active task to automatic delivery.

These are project skills invoked with `$`, not custom slash aliases. If newly added skills are not listed, restart the Codex session.

## First task

Use `tasks/001-bootstrap.md`. It deliberately creates infrastructure and one health endpoint only. Domain implementation starts after the repository can be built and verified consistently.

## Definition of done

- Acceptance criteria are satisfied.
- Tenant boundaries and business invariants are preserved.
- Unit and integration tests cover success and failure paths.
- API/schema changes are documented.
- Independent architect and security reviews have no unresolved blocking findings on the final implementation.
- Formatting, static analysis, tests, and vulnerability checks pass.
- No credentials or generated build artifacts are committed.
