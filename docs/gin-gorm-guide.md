# Gin and GORM placement guide

## Gin

Gin owns route matching and middleware execution in the API adapter. The standard `http.Server` still owns network timeouts and graceful shutdown. Host selection runs before tenant authentication; a path parameter, query, header, or body value never supplies `store_id`.

Handlers decode and validate transport input, invoke application services with the request context, and map results to the established response contract. `gin.Context` must not enter application or domain packages. Trusted proxies are disabled in Gin because the bounded project resolver is the only authority for `X-Forwarded-For`; forwarded host values never select a store.

## GORM

GORM belongs in infrastructure adapters and composition. Module-local persistence rows contain explicit table and column mappings and are converted to domain or application records at the repository boundary. Repositories do not expose `*gorm.DB`, and every tenant-owned public method requires `store_id`.

Use `WithContext` for every operation. Ordinary inserts and scoped updates use GORM models and predicates. Parameterized raw SQL is preferable when the database behavior should remain visible, including composite joins, `FOR UPDATE`, advisory locks, guarded state changes, and atomic token-family operations.

Cross-module workflows open one GORM transaction and pass its transaction-bound handle only to narrow infrastructure writer interfaces. External calls must not occur inside that transaction. Preserve sentinel errors with wrapping so HTTP mapping can continue using `errors.Is` and database errors can be classified by SQLSTATE.

## Goose remains authoritative

GORM models describe runtime mapping, not deployment state. Goose migrations define all tables, indexes, constraints, triggers, backfills, and version history. Do not use `AutoMigrate`, `Migrator().Create*`, or `Migrator().Alter*` in runtime, tests, or development commands.

Migration order is unchanged: apply Goose before deploying code that requires the resulting version. The original adapter migration did not change schema version 2. Current rollback compatibility must be checked against the release image and all subsequent Goose migrations.
