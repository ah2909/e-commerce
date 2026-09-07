# Task: Establish store context and tenant isolation

## Context

Read [product requirements](../docs/product-requirements.md), [architecture](../docs/architecture.md), [domain model](../docs/domain-model.md), [testing strategy](../docs/testing-strategy.md), [shared-schema tenancy ADR](../docs/decisions/002-shared-schema-tenancy.md), and [roadmap](../docs/implementation-roadmap.md). This is roadmap slice 2 and builds on the completed service bootstrap in [task 001](001-bootstrap.md).

## Outcome

The backend can create and persist a store with its creator's membership, resolve an immutable tenant context for a previously authenticated user with one bounded indexed query, and deny cross-store access consistently. Vietnam-oriented stores receive suitable defaults while the model remains configurable for other markets. Later modules receive a store-scoped application and repository boundary plus a reusable PostgreSQL isolation-test fixture.

## In scope

- Add the Stores module with store and membership domain types, application use cases, PostgreSQL repositories, and explicit ports.
- Create a store and its initial active membership atomically.
- Resolve tenant context from a trusted authenticated-principal boundary and an explicitly selected store ID.
- Add HTTP tenant middleware that injects immutable context or returns the standard problem shape.
- Add forward-only schema migrations and migration tooling for stores and memberships.
- Add an integration-test fixture that creates two isolated stores and authenticated users for later tenant-owned modules.
- Add structured tenant-resolution logging without credentials or unnecessary user data.
- Default omitted store settings to `VND`, `vi-VN`, and `Asia/Ho_Chi_Minh`, while accepting other valid ISO currencies, BCP 47 locales, and IANA timezones.
- Preserve Vietnamese Unicode names and normalize them to NFC before persistence.
- Add a bounded tenant-resolution database timeout and performance evidence for query count, index eligibility, latency, and allocations.

## Out of scope

- Credentials, sessions, token issuance/validation, refresh behavior, or any header/cookie that selects a store; the Identity task owns these public authentication choices.
- Store roles, permission policy, invitations, or membership administration beyond the creator membership.
- Public store-management endpoints or OpenAPI operations; no authenticated business route can be exposed safely before the Identity slice.
- Catalog or other tenant-owned business resources, row-level security, Redis caching, Kafka events, audit records, or store deletion.
- Checkout enforcement for suspended stores; this slice carries store status in context so the owning use case can enforce that rule later.
- Vietnamese address, phone-number, tax, invoice, payment, shipping, search/collation, and regulatory behavior; those belong to their owning roadmap slices.
- Redis membership caching or replicated in-process caches; authorization freshness and invalidation policy must be designed with the Identity/authorization slice before caching access decisions.

## Invariants and security constraints

- Use PostgreSQL `uuid` identifiers and typed Go `StoreID` and `UserID` values; `stores.store_id` is the tenant key.
- A membership is identified by `(store_id, user_id)`, and every membership repository operation requires both values explicitly.
- A trusted principal supplies the user and selected store IDs. Tenant middleware never accepts either value from request bodies and does not implement a temporary user-ID header.
- Store creation persists the store and creator membership in one PostgreSQL transaction.
- Only active membership resolves a tenant. Missing stores, non-members, and inactive memberships produce the same access-denied result so callers cannot enumerate stores.
- Suspended stores still resolve for future administrative access; the immutable context includes status so checkout and other capabilities can reject prohibited actions.
- Store names are not globally unique. The same user may belong to multiple stores.
- Store timestamps are UTC. Currency is an actual ISO 4217 code, locale is a valid BCP 47 tag, timezone is an IANA location, and `settings_version` starts at one.
- Omitted settings use `VND`, `vi-VN`, and `Asia/Ho_Chi_Minh`; explicit valid settings are never overwritten by Vietnam defaults.
- Vietnamese names are stored and returned without losing diacritics. Length validation counts normalized Unicode characters rather than UTF-8 bytes.
- Tenant resolution performs one parameterized PostgreSQL statement bounded by request cancellation and a configured timeout; it never scans memberships without both `store_id` and `user_id`.
- Domain and application packages do not depend on HTTP, SQL, logging, or migration-library types.

## Acceptance criteria

- [x] Given valid store settings and an authenticated creator, creating a store atomically persists an active store and active creator membership.
- [x] Given invalid currency, locale, timezone, name, status, or identifier input, creation fails without a partial write.
- [x] Given an active membership, resolving that store returns immutable context containing the typed user ID, store ID, store status, currency, locale, timezone, and settings version.
- [x] Given no verified principal, HTTP tenant middleware returns `401`; given malformed trusted identity data it fails closed.
- [x] Given a missing store, another store's user, or an inactive membership, resolution returns the same `403` problem response and does not reveal whether the store exists.
- [x] Given a suspended store and active membership, administrative tenant resolution succeeds and preserves the suspended status in context.
- [x] PostgreSQL constraints enforce valid store state and membership relationships, while one user can hold memberships in two stores.
- [x] Repository and middleware tests prove Store A cannot obtain Store B context, including attempts that reuse Store A's authenticated user with Store B's ID.
- [x] The reusable integration fixture creates two stores with overlapping tenant-local values and cleans up only its own records.
- [x] Migrations apply to an empty database, are safe to run again, and the relevant project verification commands pass.
- [x] Given omitted regional settings, store creation persists `VND`, `vi-VN`, and `Asia/Ho_Chi_Minh`; given explicit valid non-Vietnam settings, it preserves them.
- [x] Vietnamese names such as `Cửa hàng Ánh Dương` round-trip through domain, PostgreSQL, and tenant context with diacritics intact and canonical NFC storage.
- [x] Tenant resolution issues one bounded parameterized SQL query whose `(store_id, user_id)` predicate uses a composite membership index; timeout and cancellation return promptly without leaking database errors.
- [x] Focused benchmarks record tenant middleware allocations and real-PostgreSQL resolution latency as a baseline; CI does not use a machine-dependent wall-clock threshold.

## Proposed technical solution

### Module and boundaries

- Add `internal/stores/domain` for typed identifiers, `Store`, `Membership`, validation, and store status. Keep constructors explicit and inject clocks and ID generation into application use cases for deterministic tests.
- Add `internal/stores/application` with `CreateStore` and `ResolveTenant` use cases. Define repository and transaction interfaces here because the application consumes them.
- Add `internal/stores/infrastructure/postgres` as the sole owner of store-table SQL. Queries take `StoreID` explicitly; no unscoped tenant-owned lookup is exposed.
- Add a small authenticated-principal context contract under `internal/platform/authn`. The future Identity transport adapter will populate it after credential verification.
- Add tenant middleware and immutable request context under `internal/platform/tenant`. It consumes the Stores resolver, maps unauthenticated/access-denied/internal failures to `{code,message,details,request_id}`, and exposes a required-context accessor for downstream handlers.
- Do not register a public business endpoint in `cmd/api` during this slice. Unit/API tests wrap a probe handler with a controlled verified-principal provider; the Identity slice will wire the real authenticator and public routes.
- Apply regional defaults in the store-creation application command before domain construction. Use `golang.org/x/text` for BCP 47 parsing and NFC normalization; validate timezone names with Go's timezone database and keep all persisted instants in UTC.

### Performance design

- Resolve membership and store settings with one join keyed by `store_memberships(store_id, user_id)`. Select only fields required by tenant context and avoid an application-level membership-then-store round trip.
- Add `TENANT_RESOLUTION_TIMEOUT` with a 500 ms default, a 5 second maximum, and startup validation. Derive the query context from the request so client cancellation stops database work.
- Use the existing `pgxpool`; do not create per-request pools, transactions, prepared-statement registries, goroutines, or Redis calls. PostgreSQL/pgx can reuse the stable parameterized statement through its normal statement cache.
- Keep store creation as two inserts in one transaction. It is an administrative path, while tenant resolution is the optimized request path.
- Record a reproducible benchmark baseline and the PostgreSQL query plan/query count. Treat later query-count growth, sequential scans at representative cardinality, unbounded waits, or material benchmark regressions as findings; avoid fixed CI latency limits that fluctuate with runner load.

### Persistence and migrations

- Add `stores` with `store_id`, name, status, default currency, locale, timezone, positive settings version, and UTC created/updated timestamps.
- Add `store_memberships` with `store_id`, `user_id`, status, and UTC created/updated timestamps; use primary key `(store_id, user_id)`, a foreign key to `stores(store_id)`, and an index beginning with `user_id` for future membership discovery.
- Introduce pinned `github.com/pressly/goose/v3` migration tooling and forward-only numbered SQL migrations. Add Make targets used by development and integration tests; do not add down migrations that imply destructive rollback support.
- Apply migrations before deploying code that uses the tables. Both tables are new, so the initial migration has no backfill and only short catalog/table-creation locks. Recovery is a forward-fix migration; production restore remains part of operational hardening.
- Keep row-level security deferred per ADR 002 until transaction-scoped settings and pooled-connection behavior are designed and tested.

### Failure and compatibility behavior

- Normalize not-found and inactive-membership outcomes to an application-level access-denied error. Wrap operational database failures so `errors.Is`/`errors.As` remain usable, while HTTP responses and logs never expose SQL or connection details.
- Treat ID collisions and database constraint failures as failed atomic creation. Do not retry inside the use case; callers may retry with a new command/ID under a later idempotency policy.
- This adds schema and internal contracts without changing an existing public API. The future Identity plan must decide how a client selects one of several memberships and then populate the trusted principal contract.
- Vietnam defaults affect only omitted creation settings. Existing or explicitly configured international stores retain their own currency, locale, and timezone, so this revision does not turn the platform into a Vietnam-only model.

## Ordered implementation plan

1. Add typed IDs, Vietnam-oriented defaults, Unicode normalization, and store/membership domain validation with deterministic unit tests.
2. Define application ports, errors, store creation, and tenant resolution; test atomic orchestration and fail-closed outcome normalization with controlled repositories.
3. Add forward-only migrations, the pinned migration command, and Makefile integration.
4. Implement PostgreSQL transaction/repository adapters with only explicitly scoped membership operations and one joined tenant-resolution query.
5. Add the authenticated-principal and immutable tenant context contracts, standard problem writer, and tenant middleware.
6. Add PostgreSQL integration fixtures for two-store tests, migration repeatability, constraints, transaction rollback, membership overlap, and cross-store denial.
7. Add the tenant-resolution timeout, query-count/query-plan checks, and focused benchmarks; document how to reproduce the baseline.
8. Wire only migration/runtime dependencies needed by this slice and document migration/test commands. Leave business route registration to Identity.
9. Run focused development checks during build, then hand the stable artifacts to separate architect and security review stages before final verification.

## Verification plan

| Acceptance or risk | Evidence |
| --- | --- |
| Domain values and store invariants | Table-driven unit tests for IDs, status, ISO currency, BCP 47 locale, IANA timezone, settings version, and timestamps. |
| Atomic store plus membership creation | Application test with transaction spies and a real-PostgreSQL integration test that forces membership failure and proves no store remains. |
| Tenant resolution and enumeration resistance | Application and middleware tests covering active, inactive, non-member, missing-store, malformed-principal, and suspended-store cases with identical denial contracts where required. |
| Tenant isolation | Real-PostgreSQL test fixture creates Store A and Store B, then proves Store A's user cannot resolve Store B and that all membership repository calls require the requested store ID. |
| Database guarantees | Integration tests for primary/foreign/check constraints, membership overlap across stores, UTC timestamp persistence, and migration application/repeatability. |
| HTTP and observability behavior | `httptest` checks `401`, `403`, `500`, problem fields/request ID, immutable context propagation, and absence of database details or user identifiers from denial logs. |
| Vietnam defaults and Unicode | Unit and PostgreSQL integration tests cover omitted/explicit settings, actual ISO `VND`, `vi-VN`, `Asia/Ho_Chi_Minh`, NFC normalization, Vietnamese character limits, and diacritic-preserving round trips. |
| Hot-path performance | A pgx tracer proves one SQL statement; 10,000 representative memberships plus `EXPLAIN (FORMAT JSON)` proves the composite membership index is selected; cancellation/timeout tests and Go benchmarks record latency and allocations without a flaky absolute CI threshold. |
| Focused build checks | `gofmt`, affected package unit tests, affected integration tests, and migration status/up commands. |
| Final gate after reviews | `make fmt-check`, `make lint`, `make test`, `make test-integration`, `make vulncheck`, and `make verify`. |

## Design decisions requiring plan approval

- Persist tenant and user identifiers as PostgreSQL UUIDs and represent them as typed Go values.
- Add Goose as the first migration dependency and use forward-only migrations with forward-fix recovery.
- Keep client store selection and credential authentication out of this task; accept only a verified principal contract and publish no business endpoint until Identity supplies authentication.
- Use Vietnam defaults (`VND`, `vi-VN`, `Asia/Ho_Chi_Minh`) only when values are omitted, normalize names to NFC, and retain global configurability.
- Keep authorization resolution database-backed and fresh with one indexed query per request; defer caching until membership invalidation semantics exist.

## Required completion report

- Behavior and design summary
- Files/modules changed
- Commands executed with results
- Risks, assumptions, and follow-ups

## Delivery record

- Active task: no
- Execution mode: automatic
- Last command: `$ecommerce-delivery`
- Completed stage: planning; revision 2 adds Vietnam defaults, Unicode behavior, and bounded performance requirements on 2026-09-07
- Stage: complete
- Pending gates: none
- Next action: none
- Plan revision and approval: revision 2 approved by the user on 2026-09-07; it supersedes unapproved revision 1
- Initial repository state: clean at `0ff9f26` before this task document was added; Git required a per-command safe-directory override because of sandbox ownership
- Plan: use the module, persistence, middleware, migration, Vietnam-default, Unicode, and performance steps above; no existing public API changes
- Verification: use the acceptance-to-evidence mapping above and the five required backend checks
- Implementation: Stores domain/application/PostgreSQL adapter, trusted principal and tenant middleware, problem responses, forward Goose migration command, two-store test fixture, Vietnam defaults/Unicode handling, and bounded one-query tenant projection are implemented
- Focused evidence: `make fmt-check`, `make lint`, `make test`, and `make test-integration` passed before review; after fixes, `make test`, `make lint`, guarded migration apply/repeat/status, and `make test-integration` passed against the separate `ecommerce_test` database
- Performance evidence: post-fix Windows baseline was 8,055 ns/op, 2,691 B/op, and 28 allocs/op for the middleware harness; local PostgreSQL resolution was 1,754,821 ns/op, 1,387 B/op, and 25 allocs/op
- Architect review: initial verdict changes required; all four findings were fixed, re-review passed, and targeted confirmation after the database-guard change passed with no new findings.
- Security review: initial findings for full-path denial evidence and unsafe runtime database use were fixed. Re-review found that pgx query parameters could override the URL host/database after guard validation; the guard now validates pgx's effective primary/fallback hosts and database, migration/test callers use the validated config, and regression tests cover host, database, and fallback overrides. Final targeted re-review passed with no remaining findings.
- Final evidence: `make verify` passed on 2026-09-07, including `make fmt-check`, `make lint` (0 issues), `make test`, guarded `make test-integration`, and `make vulncheck` (no vulnerabilities found). A fresh `make migrate-test-up` applied version 1, a second run was a no-op, and `make migrate-test-status` reported version 1. `make benchmark-store-context`, `docker compose config`, CI YAML parsing, and `git diff --check` passed. PostgreSQL 18.6, Redis 8.8.2, and Kafka 4.3.1 were healthy and loopback-only before disposable containers, network, and volumes were removed.
- Remaining limits: native Windows tests did not use the race detector because the existing Makefile enables it only on supported non-Windows hosts. Hosted CI was not run. The Identity slice must establish authenticated-principal provenance and client store selection before publishing business routes; production migrations and broader load/operational testing remain release activities.
