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

For a ready-to-use demo and the steps to provision another store, see [local store setup](docs/local-store-setup.md).

## Configuration

[`.env.example`](.env.example) lists development configuration and disposable local credentials. `make` supplies those development defaults; direct `go run` commands require the variables in the process environment.

Important boundaries:

- `TENANT_BASE_DOMAIN` and `AUTH_HOST` define trusted host routing.
- `PLATFORM_ADMIN_TOKEN` protects base-domain store provisioning and has no Makefile default; supply 32 random bytes encoded as unpadded base64url through the environment or a secret manager. The value in `.env.example` is for local development only.
- `TRUSTED_PROXY_CIDRS` controls when forwarded client addresses may influence rate limiting; it never affects tenant host selection.
- `CORS_ALLOWED_ORIGINS` is a comma-separated exact HTTP(S) origin allowlist for browser frontends; empty disables cross-origin access and wildcards are rejected.
- `DATABASE_URL` is runtime state; `TEST_DATABASE_URL` must identify a loopback database ending in `_test` before integration mutation is allowed.
- `INVENTORY_EXPIRY_BATCH` and `INVENTORY_EXPIRY_POLL` bound reservation-expiry work after migration 5.
- `CART_EXPIRY_BATCH` and `CART_EXPIRY_POLL` bound inactive-cart expiry work after migration 6.
- `PAYMENT_CONFIG_KEYS` is a comma-separated versioned AES-256 keyring and `PAYMENT_CONFIG_ACTIVE_KEY_VERSION` selects the write key; the checked-in value is development-only.
- `MOMO_API_BASE_URL` is restricted to MoMo's official production or sandbox origin. `MOMO_PUBLIC_URL` must be the public HTTPS API origin used for signed IPNs, and `MOMO_REDIRECT_URL` is the browser return target.
- Google or Facebook login remains disabled unless its complete provider configuration is present and valid.
- Provider, database, Redis, and token configuration must come from deployment secrets/configuration, never committed production values.
- Store-owned VietQR beneficiary details and MoMo credentials are encrypted at rest. API reads expose only masked public identifiers and never return payment secrets.

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

Live MoMo acceptance uses an isolated test order, the MoMo Test app, a publicly reachable HTTPS API, and sandbox merchant credentials. Set the `MOMO_SANDBOX_*` variables consumed by `cmd/momo-sandbox`, then run:

```bash
make test-momo-sandbox
```

The runner configures the sandbox merchant through the API, creates the attempt, waits for the signed IPN, and verifies the resulting payment, order, reservation, ledger, and webhook receipt in the guarded test database. Keep credentials in the process environment or a secret manager; do not commit them.

Required inputs are `MOMO_SANDBOX_APP_URL`, `MOMO_SANDBOX_STORE_HOST`, `MOMO_SANDBOX_STORE_ID`, `MOMO_SANDBOX_ORDER_ID`, `MOMO_SANDBOX_OWNER_TOKEN`, `MOMO_SANDBOX_SHOPPER_TOKEN`, `MOMO_SANDBOX_PARTNER_CODE`, `MOMO_SANDBOX_ACCESS_KEY`, `MOMO_SANDBOX_SECRET_KEY`, and `MOMO_SANDBOX_DATABASE_URL`. The API must run with `MOMO_API_BASE_URL=https://test-payment.momo.vn`, a `MOMO_PUBLIC_URL` reachable by MoMo over HTTPS, and an active reservation on the pending MoMo test order. `MOMO_SANDBOX_CONFIG_VERSION` is optional when replacing an existing configuration.

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

Delivery is manual. Invoke `$ec-plan`, `$ec-build`, `$ec-review`, `$ec-fix`, or `$ec-test` when you want that stage; each stops at its boundary. Ordinary requests and `continue` do not activate the workflow. `$ecommerce-delivery` explicitly selects the full sequence for a specified task. See the [development workflow](docs/development-workflow.md) and [task index](tasks/README.md).
