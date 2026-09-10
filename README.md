# Multi-store e-commerce backend

Go backend for independent Vietnamese stores. It is designed for subdomain-isolated SaaS and customer-managed deployments, with PostgreSQL as the transactional source of truth.

The design covers the full MVP; the implementation does not yet. Read [development status](docs/development-status.md) before using or deploying the current code.

## Documentation

| Document | Purpose |
| --- | --- |
| [Product requirements](docs/product-requirements.md) | Actors, MVP behavior, exclusions, and success criteria |
| [System architecture](docs/architecture.md) | Components, module boundaries, deployment modes, security, reliability, and scaling |
| [Domain model](docs/domain-model.md) | Aggregates, invariants, and state transitions |
| [Database design](docs/database-design.md) | Table ownership, tenant keys, constraints, indexes, transactions, retention, and growth |
| [Event catalog](docs/event-catalog.md) | Versioned integration facts and delivery rules |
| [Testing strategy](docs/testing-strategy.md) | Required correctness, isolation, provider, concurrency, and operational tests |
| [Implementation roadmap](docs/implementation-roadmap.md) | Ordered delivery slices and release milestones |
| [Development status](docs/development-status.md) | Evidence-backed implemented, incomplete, and not-started state |
| [Architecture decisions](docs/decisions/) | Durable decisions and consequences |
| [Development workflow](docs/development-workflow.md) | Task approval, build, review, fix, and verification gates |
| [Task index](tasks/README.md) | Active work and archived delivery records |

## Architecture summary

- Stateless API and worker processes in a modular monolith
- PostgreSQL for authoritative state, transactions, idempotency, outbox/inbox, and audit
- Redis for rate limits, short-lived social state, and reconstructable cache only
- Kafka for asynchronous side effects after transactional outbox commit
- Shared-schema tenancy with mandatory store scope and composite tenant foreign keys
- Host-selected tenant context before user or guest authentication
- Integer VND money, atomic inventory reservations, immutable order snapshots, and idempotent payments/refunds

## Local development

Prerequisites:

- Go version declared in [go.mod](go.mod)
- GNU Make
- Docker with Compose

Start disposable PostgreSQL, Redis, and Kafka dependencies:

```bash
make dev-deps
```

Apply runtime migrations and run the API:

```bash
make migrate-up
make run-api
```

Run the worker in another terminal when needed:

```bash
make run-worker
```

Health endpoints:

```bash
curl -i http://localhost:8080/health/live
curl -i http://localhost:8080/health/ready
```

Stop dependencies without deleting their volumes:

```bash
make dev-deps-down
```

## Configuration

[`.env.example`](.env.example) lists development configuration and disposable local credentials. `make` supplies those development defaults; direct `go run` commands require the variables in the process environment.

Important boundaries:

- `TENANT_BASE_DOMAIN` and `AUTH_HOST` define trusted host routing.
- `PLATFORM_ADMIN_TOKEN` protects base-domain store provisioning and has no Makefile default; supply 32 random bytes encoded as unpadded base64url through the environment or a secret manager. The value in `.env.example` is for local development only.
- `TRUSTED_PROXY_CIDRS` controls when forwarded client addresses may influence rate limiting; it never affects tenant host selection.
- `CORS_ALLOWED_ORIGINS` is a comma-separated exact HTTP(S) origin allowlist for browser frontends; empty disables cross-origin access and wildcards are rejected.
- `DATABASE_URL` is runtime state; `TEST_DATABASE_URL` must identify a loopback database ending in `_test` before integration mutation is allowed.
- Google or Facebook login remains disabled unless its complete provider configuration is present and valid.
- Provider, database, Redis, and token configuration must come from deployment secrets/configuration, never committed production values.
- Future store-owned VietQR/MoMo secrets require encrypted persistence and an external key; that payment functionality is not implemented yet.

The public contract currently implemented is [api/openapi.json](api/openapi.json).

## Database operations

```bash
make migrate-up
make migrate-status
```

Migrations are forward-only. Use a forward fix for schema recovery. Production migration or database reset requires explicit operational authorization.

Integration tests use a separately guarded test database:

```bash
make migrate-test-up
make test-integration
```

## Verification

Run the complete local gate with dependencies available:

```bash
make verify
```

Individual checks are:

```bash
make fmt-check
make lint
make test
make test-integration
make vulncheck
```

On native Windows, race-enabled Go tests may be unavailable; use CI or WSL for race evidence. Never interpret an unavailable integration dependency or vulnerability database as a passing check.

## Delivery workflow

Describe the outcome you want in normal language. Codex resolves or creates the task record, investigates the codebase, presents one implementation plan, and waits for one approval.

After approval, say `continue`. Codex carries the task through implementation, independent architecture and security reviews, required fixes, and final verification.

```text
Describe work -> approve plan -> continue -> completion report
```

`$ecommerce-delivery` explicitly selects the same automatic behavior. The stage commands `$ec-plan`, `$ec-build`, `$ec-review`, `$ec-fix`, and `$ec-test` remain available as advanced controls. You normally do not need to choose a task path or workflow command. See the [development workflow](docs/development-workflow.md) for authoritative rules and [`tasks/`](tasks/) for delivery records.
