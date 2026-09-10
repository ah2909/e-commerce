# Task: Adopt Gin and GORM across the existing Go backend

## Context

The current backend uses Go's `net/http`, handwritten `pgx/v5` repositories, Goose migrations, PostgreSQL, and Redis. It has implemented Foundation, tenant context, platform administration, Identity, Stores, and Audit behavior, with two deferred Identity findings documented in [development status](../../docs/development-status.md).

The user wants the project to teach widely adopted Go tools as well as deliver the product. This task therefore migrates the complete existing HTTP transport to Gin and the complete existing PostgreSQL persistence layer to GORM. It preserves domain/application boundaries, public behavior, PostgreSQL schema, Goose migration history, tenant isolation, and current security semantics.

The prior pilot Catalog plan was never approved or implemented and is superseded. Catalog work resumes as a new task after this migration passes its gates.

## Outcome

- Every existing API route is registered and served through Gin while preserving its current path, method, host boundary, middleware order, response contract, and timeout behavior.
- Every existing production PostgreSQL repository and cross-module unit of work uses GORM rather than handwritten `pgx` query/scanning code.
- Domain and application packages remain independent of Gin and GORM.
- Goose remains the only production schema migration mechanism; GORM `AutoMigrate` is absent and prohibited.
- Existing unit, PostgreSQL, Redis, migration, API, concurrency, lint, and vulnerability evidence passes against the migrated implementation.
- The codebase includes concise learning documentation explaining Gin/GORM placement, transaction handling, tenant-safe repository patterns, and when raw SQL remains appropriate.

## In scope

- Add pinned Gin, GORM, and GORM PostgreSQL-driver dependencies.
- Replace `http.ServeMux` route registration and transport adapters for health, onboarding, Identity, Stores, and memberships with Gin.
- Preserve the standard `http.Server` lifecycle and graceful shutdown around the Gin engine.
- Replace production PostgreSQL repository implementations for Stores, Identity, Onboarding, and Audit with GORM-backed adapters.
- Add one shared infrastructure database bootstrap that configures GORM and its underlying `database/sql` pool explicitly.
- Replace cross-module `pgx.Tx` orchestration with a narrow application unit-of-work abstraction backed by one GORM transaction.
- Use parameterized GORM clauses or `Raw` SQL for guarded updates, database time, locking, atomic token rotation, concurrency-sensitive membership changes, and other behavior that should remain explicit.
- Update tests, test fixtures, composition, Make targets, dependency checks, architecture/development documentation, and an ADR for the framework/persistence decision.
- Remove direct production `pgx` usage and obsolete adapters only after equivalent behavior is proven. Transitive `pgx` use by the GORM driver and Goose is acceptable.

## Out of scope

- Database schema changes, data backfills, migration-history edits, or use of GORM `AutoMigrate`.
- New API endpoints, changed request/response schemas, new authentication behavior, or new commerce modules.
- Fixing the two deferred Identity findings unless the framework migration exposes a regression directly caused by this task.
- Catalog, Pricing, Inventory, Customers, Carts, Orders, Payments, Refunds, Shipping, Eventing, admin UI, or storefront UI.
- Echo, Fiber, Chi, Ent, sqlc, GraphQL, dependency injection frameworks, or generated HTTP servers.
- Production deployment, production migration execution, or destructive database reset.

## Why Goose remains

GORM and Goose have separate responsibilities:

- GORM maps runtime Go operations to an already-defined schema.
- Goose stores and applies the ordered, reviewable history that changes that schema between releases.

GORM `AutoMigrate` is intentionally insufficient for this project because it cannot represent the complete deployment intent for data backfills, staged constraints, concurrent indexes, explicit lock behavior, compatibility windows, recovery decisions, or forward fixes. The official GORM documentation also states that `AutoMigrate` does not delete unused columns and that projects may need a versioned migration strategy.

Policy:

- Goose SQL files remain authoritative for tables, columns, indexes, foreign keys, checks, data migration, and schema version.
- `AutoMigrate`, `Migrator().Create*`, and `Migrator().Alter*` are forbidden in runtime, tests, and development commands.
- GORM model tags describe runtime mapping and must be checked against Goose-created schema; they do not define deployment state.
- Migration tests continue applying Goose from empty and populated schemas.

## Invariants and security constraints

- Store scope still comes from the validated direct host before authentication and never from Gin parameters, headers, query values, or bodies.
- Every tenant repository method requires an explicit `store_id`; there is no unscoped tenant method or exported raw `*gorm.DB`.
- Every GORM read, preload, association, update, delete/archive, count, conflict lookup, and raw query carries store scope.
- Database composite tenant keys and constraints remain the final defense against cross-store relationships.
- Gin is transport-only. `gin.Context` cannot cross into application or domain packages.
- GORM is infrastructure-only. `gorm.DB`, clauses, model tags, and persistence models cannot cross into application or domain packages.
- Persistence models are separate from aggregates and API DTOs; GORM hooks do not implement domain behavior or authorization.
- Existing middleware order remains host validation, tenant resolution where applicable, authentication, membership resolution, authorization, timeout/rate policy, use case, and response mapping.
- Gin trusted-proxy behavior is disabled unless it exactly delegates to the existing bounded trusted-proxy resolver; forwarded host values never select a tenant.
- GORM logging excludes SQL arguments and sensitive values in production and carries bounded correlation context without credentials or personal data.
- All I/O uses request context. GORM calls use `WithContext`, and transactions preserve error identity for `errors.Is`/`errors.As` classification.
- Connection-pool limits, lifetimes, readiness checks, shutdown, and test-database guards remain explicit and observable.

## Technical solution

### Gin transport

1. Create one configured Gin engine in platform composition without Gin's default logger or recovery output.
2. Port request ID, structured logging, recovery/problem mapping, body limits, trusted client address, host selection, authentication, authorization, and timeouts as explicit middleware while preserving their established semantics.
3. Organize routes by host/security boundary rather than convenience alone: base-domain provisioning, fixed auth-host callbacks, tenant-public routes, tenant-user routes, tenant-guest routes, and tenant-workforce routes.
4. Convert handlers incrementally from `http.ResponseWriter`/`*http.Request` to `*gin.Context`, keeping decoding and response mapping thin and invoking unchanged application interfaces.
5. Use Gin path parameters only for resource identity. Store identity remains server-resolved from the direct host.
6. Configure method-not-allowed and not-found responses to retain the documented problem schema and avoid route enumeration differences.

### GORM infrastructure

1. Initialize GORM with the PostgreSQL driver over a deliberately configured `database/sql` pool. Reuse this pool for Goose and readiness where practical; do not create hidden independent pools.
2. Define module-local persistence models that exactly match Goose tables, composite keys, nullability, timestamps, and explicit table names without embedding `gorm.Model`.
3. Implement tenant-scoped repositories with consumer-owned interfaces unchanged. Map between persistence rows and domain/application records at the infrastructure boundary.
4. Use a mandatory scoped helper to reduce repeated predicates, but require `store_id` in every public repository method and verify emitted SQL; a helper is not treated as the security boundary.
5. Implement one GORM-backed unit of work that supplies transaction-bound module writers for tenant provisioning, membership/audit changes, and session/token rotations without letting modules query one another's tables.
6. Translate not-found, uniqueness, foreign-key, serialization/deadlock, cancellation, and dependency failures into the current wrapped/sentinel error contracts. Do not expose raw GORM errors through HTTP.
7. Prefer explicit `Where` predicates, selected columns, guarded `Updates`, `RowsAffected`, and parameterized raw SQL over implicit association saving. Disable default transactions only if benchmarks and atomicity tests justify it later.
8. Use PostgreSQL integration tests to establish every lock, constraint, transaction, and concurrency claim; GORM mocks are not substitutes.

### Compatibility and deployment

- This is a code-only persistence/transport migration: no new Goose migration is expected.
- Public OpenAPI paths, operation behavior, security schemes, problem responses, tokens, Redis keys, and stored data remain compatible.
- Deploy the migrated binary against schema version 2. Startup must verify the expected Goose version and fail safely on incompatible schema.
- A rollback deploys the previous binary against the unchanged schema. If implementation discovers a required schema change, stop and revise the plan before adding a forward migration.
- Existing migration 1/2 files are immutable for this task; no production data transformation is authorized.

## New dependencies

- Gin: popular HTTP framework used for routing, middleware, binding, response handling, and framework learning.
- GORM: popular ORM used for repository implementation, transactions, query construction, and persistence learning.
- GORM PostgreSQL driver: connects GORM to the existing PostgreSQL database.

Versions are pinned during implementation after checking compatibility with the Go version in `go.mod`. Existing Goose, Redis, OAuth/OIDC, crypto, text, lint, and vulnerability tools remain.

## Ordered implementation plan

1. Capture characterization evidence for all current routes, SQL-visible repository behavior, errors, transactions, middleware order, pool settings, and cross-store/concurrency cases.
2. Add pinned Gin/GORM dependencies, shared database bootstrap, redacted GORM logger, and configuration/pool tests without switching production call sites.
3. Migrate the HTTP composition and health routes to Gin; prove lifecycle, readiness, 404/405, request ID, recovery, and timeout compatibility.
4. Migrate base/auth/tenant middleware and Onboarding routes; prove direct-host authority, platform separation, trusted-proxy behavior, rate limiting, and provisioning rollback.
5. Migrate Stores/membership routes and repositories; prove role authorization, current membership, last-owner concurrency, audit rollback, and cross-store denial.
6. Migrate Identity routes and repositories in bounded groups: users/passwords, sessions/tokens, guests, social identities, then refresh/reuse and state-sensitive operations.
7. Replace transaction orchestration with the GORM unit of work and explicit locking/guarded SQL where needed; rerun every relevant real-PostgreSQL concurrency test after each group.
8. Migrate Audit persistence, consolidate shared error mapping, verify no Gin/GORM types cross their boundaries, and remove obsolete production adapters/imports.
9. Update architecture, database, development-status, learning documentation, ADR, dependency manifests, Make/CI commands, and OpenAPI only where generated metadata changes without public behavior.
10. Run focused development checks and stop before independent architect/security review as required by stage-only delivery.

## Verification strategy

| Requirement or risk | Required evidence |
| --- | --- |
| HTTP compatibility | Table-driven and API tests for every route/method, path parameter, body limit, status, header, problem body, 404/405, callback redirect, and OpenAPI operation |
| Middleware security | Tests proving direct-host tenant authority, base/auth/tenant separation, trusted-proxy rules, auth/authz order, request IDs, timeouts, rate limits, and panic redaction |
| Tenant isolation | Existing plus SQL-capture and real-PostgreSQL tests for every repository read/write/relationship/uniqueness path with Store A targeting Store B |
| Persistence mapping | Tests for every nullable field, enum/status, UTC timestamp, identifier, digest, version, and database/domain conversion |
| Transaction atomicity | Real PostgreSQL rollback tests for provisioning, membership/audit, session rotation/reuse, disable/revoke, and social state persistence |
| Concurrency | Existing synchronized PostgreSQL tests for last owner, refresh rotation/reuse, disable/suspension interactions, duplicates, guarded updates, and deadlock/error classification |
| GORM safety | Emitted-SQL assertions for tenant predicates; no `AutoMigrate`; no implicit association writes; no exported raw DB; sensitive parameter logging disabled |
| Migration compatibility | Goose fresh-schema, populated version-1 upgrade, version-2 status/repeat, schema inspection, and previous-binary rollback reasoning |
| Pool and failure behavior | Tests for pool configuration, readiness, cancellation, timeouts, shutdown, unavailable database, and preserved retryable/permanent error mapping |
| Boundary enforcement | Import/static checks proving Gin exists only in transport/composition and GORM only in infrastructure/composition |
| Complete gate | `make fmt-check`, `make lint`, `make test`, `make test-integration`, `make vulncheck`, and `git diff --check` |

## Acceptance criteria

- [x] Given any currently documented request, when it is served through Gin, then its public behavior and security boundary remain compatible.
- [x] Given valid existing schema-version-2 data, when GORM repositories read or mutate it, then results match the previous repository behavior without data migration.
- [x] Given Store A credentials or identifiers, when any migrated handler/repository path targets Store B, then access is denied or non-enumerating and no state changes.
- [x] Given every current atomic workflow and concurrency race, when executed against real PostgreSQL through GORM, then rollback, locking, idempotency, and error semantics remain correct.
- [x] Given application/domain packages, when imports are checked, then neither Gin nor GORM appears outside its allowed adapter boundary.
- [x] Given runtime and test startup, when code paths are inspected/executed, then GORM `AutoMigrate` and schema-changing Migrator calls are absent.
- [x] Given migrations from empty and populated schemas, when Goose commands run, then schema version and constraints remain unchanged and compatible.
- [x] Exact focused and complete verification commands pass, followed by independent architecture and security reviews.

## Required completion report

- Behavior and design summary
- Files/modules changed
- Commands executed with results
- Risks, assumptions, and follow-ups

## Delivery record

Follow [the project workflow](../../docs/development-workflow.md). Keep this record concise and update it as work progresses.

- Stage: complete
- Active task: no
- Execution mode: automatic delivery
- Last request, completed stage, pending gates, and next action: `continue`; implementation, independent reviews, fixes, and final verification complete; archive the accepted task.
- Plan revision and approval: revision 1 prepared on 2026-09-09 and approved by the user on 2026-09-09.
- Plan: code-compatible project-wide Gin transport and GORM persistence migration while retaining Goose as schema authority, as specified above.
- Verification: focused unit and integration checks passed throughout implementation and fixes. The final `make verify` passed formatting, zero-issue lint, all unit tests, all integration tests against Goose schema version 2, and vulnerability scanning.
- Architect review: passed with no findings after confirming bounded GORM startup, native Gin boundaries, redirect and method compatibility, exact GET/HEAD middleware chains, documentation consistency, and the final dependency graph.
- Security review: passed with no findings after confirming tenant isolation, authentication and authorization order, parameterized SQL, transaction safety, redacted logging, redirect hardening, GET/HEAD access equivalence, and the patched dependency graph.
- Final evidence: `make verify` passed on 2026-09-10. The first final run found reachable `GO-2026-5676` in transitive `quic-go v0.54.0`; upgrading to `quic-go v0.59.1` and `qpack v0.6.0` cleared it. The clean rerun reported zero vulnerabilities in called code or imported packages. Final documentation link, ADR identifier, and diff checks passed before archiving.
