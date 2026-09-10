# ADR 005: Gin transport and GORM persistence adapters

Status: Accepted

## Context

The initial backend used `net/http` routing and direct pgx repositories. The product remains a modular monolith, but the project also serves as a practical Go learning codebase and benefits from consistent, widely used transport and persistence tooling.

## Decision

Use Gin for API route registration and middleware execution. Keep the standard `http.Server` for listener lifecycle, timeouts, and graceful shutdown. Gin remains inside transport and platform composition; application and domain packages do not depend on it.

Use GORM for runtime PostgreSQL repositories and transaction orchestration. Persistence models remain module-local and separate from domain types. Every tenant operation still accepts and applies `store_id`. Explicit parameterized SQL remains appropriate for joins, row locks, guarded updates, advisory locks, and other concurrency-sensitive operations.

Goose SQL migrations remain the only schema authority. Runtime and test code must not call GORM `AutoMigrate` or schema-changing migrator methods.

## Consequences

- Existing paths, host boundaries, payloads, errors, and schema-version-2 data remain compatible.
- GORM uses one explicitly configured `database/sql` pool with bounded connection settings and argument-free production error logging.
- Module transaction writers receive a transaction-bound GORM handle without exposing persistence types to application or domain code.
- Framework upgrades add dependency and vulnerability-review work.
- A rollback can deploy the previous binary against the unchanged Goose schema.
