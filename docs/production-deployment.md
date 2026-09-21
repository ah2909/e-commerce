# Production deployment baseline

## Supported topology and objectives

The initial topology is one PostgreSQL authority, Redis for rate limits and sessions, Kafka for post-commit delivery, stateless API replicas, and worker replicas. The launch objectives are 99.5% monthly API availability, non-provider API p95 below 500 ms, checkout p95 below one second, PostgreSQL RPO at most 15 minutes, and service RTO at most four hours. These are launch targets, not measured claims; the selected environment must supply the evidence described below.

`multi_store` routes `<store>.<TENANT_BASE_DOMAIN>` and reserves the exact platform and authentication hosts. `single_store` routes exactly `SINGLE_STORE_HOST` to `SINGLE_STORE_ID`. In both modes the direct HTTP `Host` is authoritative; forwarded-host headers never select a tenant.

## Release order

1. Pin `APP_IMAGE` to the reviewed image digest and inject secrets from the deployment secret manager. Do not copy a populated environment file into the image.
2. Render `deploy/compose.production.yml`, run `/config-check`, and confirm the combined API and worker replica pool maximum remains below the PostgreSQL connection budget with administrative reserve. Each API replica reserves one pool connection for its payment-key writer fence, so `DATABASE_MAX_OPEN` is at least two and that reservation is included in the budget.
3. Back up PostgreSQL, record backup age, confirm PITR coverage, and retain every payment key version referenced by the database.
4. Run `/migrate status`, then the one-shot `/migrate up`. Migration 13 only creates two partial retention indexes. On large tables, create those indexes concurrently in a separate change window before enabling retention.
5. Start API replicas, wait for `/health/ready`, verify the internal `/metrics` listener and trace collector, then start workers. Kafka failure does not make checkout unready.
6. Exercise one storefront read, authenticated mutation, checkout fixture, worker publication, and consumer projection. Confirm alerts are routed to an owned receiver.
7. Replace replicas one at a time. A termination must drain inside `SHUTDOWN_TIMEOUT` and the deployment's 45-second grace period.

The example is rendered with:

```text
RUNTIME_ENV_FILE=production.env docker compose --env-file deploy/production.env -f deploy/compose.production.yml config
RUNTIME_ENV_FILE=production.env docker compose --env-file deploy/production.env -f deploy/compose.production.yml --profile migration run --rm migrate
RUNTIME_ENV_FILE=production.env docker compose --env-file deploy/production.env -f deploy/compose.production.yml up -d api worker
```

`production.env` is a local, uncommitted copy of `deploy/production.env.example`; secret-valued variables are supplied by the environment. The metrics ports in the example bind only to loopback and must be reachable only by the monitoring plane.

## Rollback and forward fix

Stop the worker first, then return API replicas to the last image known to support schema 13. Do not run migration down or restore production as a routine rollback. Keep both payment encryption keys during an in-progress rotation. If a forward-only migration is not backward compatible, stop the rollout and deploy a forward fix. Retention stays on hold until restore, replay, and monitoring checks have passed.

## Release gates

Run `make verify`, `make container-build`, `make sbom`, `make container-scan`, and `make secret-scan`. Against a disposable API, run `make zap-baseline`; against production-like synthetic fixtures, run `make load-smoke`. A missing tool, failed scan, unavailable external environment, or unresolved critical/high finding is a failed gate rather than a pass.

Before launch, the environment owner must record DNS/TLS and direct-host preservation, trusted proxy ranges, network policy, secret injection, Kafka TLS/SASL/ACLs and 30-day topic retention, PostgreSQL encrypted PITR, paging delivery, image digest, schema version, pool arithmetic, and the measured restore and load reports.
