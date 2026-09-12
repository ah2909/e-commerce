# Task: Build single-location Inventory

## Context

This is the next work explicitly selected by [development status](../../docs/development-status.md) after the accepted [MVP coupon deferral](010-defer-coupons-until-after-mvp.md). Requirements come from the [product requirements](../../docs/product-requirements.md), [architecture](../../docs/architecture.md), [domain model](../../docs/domain-model.md), [database design](../../docs/database-design.md), [event catalog](../../docs/event-catalog.md), [testing strategy](../../docs/testing-strategy.md), and [implementation roadmap](../../docs/implementation-roadmap.md).

Task 004 remains inactive with two accepted Identity risks. The pre-existing documentation and Task 010 working-tree changes are outside this task and must be preserved.

## Outcome

- Authorized owners and admins can inspect variant-level stock and apply idempotent, audited manual quantity adjustments for one logical store location.
- Storefront Catalog responses expose current in-stock status without disclosing exact stock counts.
- Inventory provides tenant-scoped application operations that atomically reserve available stock for ten minutes, confirm consumption, release reservations, and expire overdue reservations.
- Every physical quantity change has an immutable ledger entry; reservation and stock facts are appended to the transactional outbox without requiring Kafka.
- Concurrent operations cannot oversell, make availability negative, duplicate stock effects, or cross tenant boundaries.

## In scope

- Add a cohesive `internal/inventory` module with stock, ledger, and reservation domain rules, application services, GORM persistence, and Gin administration handlers.
- Add migration `00005` for tenant-owned stock items, reservations, and immutable ledger entries, including the audit action required for manual adjustment.
- Support exact admin stock reads, keyset listing, and delta-based manual adjustments guarded by optimistic version and an `Idempotency-Key` header.
- Add `inventory.view` and `inventory.manage` permissions for owner/admin; staff and non-workforce principals remain denied by default.
- Add a batch availability interface used by public Catalog list/detail responses to return `in_stock` for each otherwise visible variant.
- Add internal atomic reserve, confirm, release, and overdue-expiry operations with database-time deadlines, idempotent retry behavior, fixed ordering, ledger/outbox effects, and future transaction-coordinator seams.
- Add bounded expiry processing to the worker after schema 5, with cancellation-safe polling and structured outcome/error logging.
- Extend typed identifiers, OpenAPI, schema-gated API/worker composition, development status, and focused tests.

## Out of scope

- Carts, checkout endpoints, Orders, Payments, cancellation/refund orchestration, shipping, customer data, or guest order access.
- Public reservation, confirmation, release, or adjustment endpoints; only trusted in-process application interfaces expose reservation lifecycle operations.
- Multiple warehouses, transfers, backorders, purchase orders, suppliers, low-stock configuration/notifications, bulk import, or inventory valuation.
- Kafka publication, consumer inboxes, dead-letter handling, or general Eventing administration.
- Per-membership custom permission grants, fixing Task 004 risks, production migration execution, deployment, or external publishing.

## Proposed HTTP contract

Administrative routes use the resolved tenant host, user authentication, active workforce membership, and the new Inventory permissions.

- `GET /api/v1/admin/inventory/stock-items?limit=&cursor=` lists materialized stock items by `updated_at DESC, variant_id DESC`.
- `GET /api/v1/admin/inventory/stock-items/{variant_id}` returns exact `on_hand`, `reserved`, `available`, and optimistic `version`; a valid same-store variant without adjustments returns an implicit zero item.
- `POST /api/v1/admin/inventory/stock-items/{variant_id}/adjustments` applies `{quantity_delta, reason, expected_version}` and requires a bounded `Idempotency-Key` header.

Public Catalog variant responses gain only `in_stock: boolean`; exact `on_hand`, reserved quantity, availability, versions, ledger data, and reservation data remain administrative/internal. Unknown, archived, or cross-store variants use existing non-enumerating behavior. Validation uses 400, missing same-store resources use 404, stale version/idempotency mismatch/insufficient availability use 409, authentication uses 401, authorization uses 403, and dependency failures use 503.

## Invariants and security constraints

- Store scope comes only from validated host/authenticated context. Every Inventory table, key, constraint, repository method, query, mutation, job candidate, audit row, and outbox fact carries explicit `store_id`.
- A stock item is keyed by the same-store Catalog variant. Absence means zero stock; the first successful adjustment materializes it atomically. Archived or foreign variants cannot be adjusted or used to infer existence.
- Quantities are bounded non-negative integers. `available = on_hand - active_reserved`; neither `on_hand` nor `available` may become negative, and backorders are rejected.
- A non-zero manual delta locks the stock item, checks `expected_version`, rechecks active reservations, writes exactly one immutable ledger row, audit row, and `inventory.changed.v1` fact, and updates stock in one transaction.
- The idempotency identity is `(store_id, idempotency_key)`. An exact retry returns the original result; reuse with different variant, delta, reason, or expected version conflicts without a second effect.
- A reservation batch contains unique same-store active variants and positive quantities. Creation uses database time for a ten-minute deadline, locks stock rows in variant-ID order, and either creates the whole batch plus `inventory.reserved.v1` facts or commits nothing.
- Reservation retries for one `(store_id, order_id)` return the existing matching allocation; a changed allocation conflicts. Active reservation uniqueness is tenant scoped.
- Confirmation locks reservations then affected stock rows in stable order, rechecks database time/state, decrements `on_hand`, closes reservations, and writes matching negative ledger entries and outbox facts atomically. Release/expiry closes active reservations exactly once without changing `on_hand`.
- The current Inventory-only expiry processor never invents Order or Payment state. The future Orders/Payments slice must place these operations inside the documented global order-first transaction before checkout is exposed; no current public route can create a reservation.
- Domain and application packages do not import Gin, GORM, SQL, Kafka, HTTP, or foreign persistence types. Catalog and Inventory interact through explicit application interfaces rather than direct table access.
- Audit/outbox payloads exclude credentials and personal data; structured logs include request/correlation identifiers and counts, not sensitive payloads or idempotency keys.

## Technical solution

### Inventory domain and application

1. Model stock snapshots, ledger entries, reservation allocations, reservation states, adjustment commands, and lifecycle transitions with integer bounds and UTC timestamps.
2. Define consumer-owned Inventory interfaces with explicit `store_id`: admin stock service, Catalog batch availability resolver, reservation lifecycle service, and bounded expiry processor.
3. Treat missing stock as zero for reads. Validate active same-store variants through a narrow Catalog application interface before creating stock or reservations.
4. Make all retry outcomes explicit: exact replays succeed with the original result, conflicting reuse returns a stable conflict, and terminal reservation transitions are no-op only when the requested outcome already holds.

### Persistence, transactions, and events

1. Add `inventory_stock_items`, `inventory_reservations`, and `inventory_ledger_entries` with composite tenant keys/FKs, quantity/state/lifecycle checks, store-leading lookup and expiry indexes, optimistic versions, and idempotency uniqueness.
2. Use guarded GORM transactions and deterministic variant/reservation ordering. PostgreSQL database time is authoritative for expiry and boundary decisions.
3. Append Inventory events through the existing Eventing writer in the same transaction as their business effects. Manual adjustments also append the required Audit record atomically.
4. Implement bounded non-locking expiry discovery followed by per-order Inventory transactions. Because Orders/Payments are absent and no public reservation creator exists, record the required integration seam and replace the inner transition with the documented order-first coordinator before checkout becomes reachable.

### HTTP, Catalog, worker, and composition

1. Add strict Gin admin handlers with body/header limits, unknown-field rejection, identifier/cursor parsing, request timeouts, request IDs, stable problem mapping, and exact tenant/role enforcement.
2. Add batched Inventory availability resolution to Catalog public list/detail assembly so the existing bounded Catalog scan does not introduce per-variant queries.
3. Register Inventory routes only at schema version 5. At schema 4, the binary preserves existing routes and omits Inventory behavior.
4. Wire the schema-5 worker with bounded batch size and poll interval from validated configuration, context cancellation, per-cycle error isolation, and no Kafka dependency.
5. Update OpenAPI with admin stock/adjustment schemas and public `in_stock`, while keeping reservation lifecycle contracts internal.

## Compatibility and migration effects

- Migration 5 is additive except for widening the existing Audit action/subject constraint to admit `inventory.adjusted`. The migration changes that constraint transactionally and validates all tenant, quantity, state, and lifecycle checks.
- Existing schema-4 Catalog/Pricing data and APIs remain compatible. Variants without stock rows become publicly `in_stock: false`; this is the intended new Inventory gate and is an externally visible API behavior approved with this plan.
- Rollout order is compatible binary first, migration 5 second, then normal rolling restart/schema refresh. The binary omits Inventory routes and worker behavior while schema 4 is active.
- The new tables begin empty, so there is no data backfill. The brief Audit constraint metadata lock and new-table Catalog foreign keys are the principal migration locks; production-sized validation must be measured before deployment.
- Recovery is forward-fix after migration 5 while retaining the compatible binary. Older binaries remain safe for existing routes but do not expose Inventory and must not be used as a schema rollback mechanism.
- No new runtime dependency is expected.

## Ordered implementation plan

1. Add Inventory/reservation/ledger IDs, permissions, domain types, transitions, and unit tests for quantity, availability, state, expiry, and retry rules.
2. Add migration 5 with fresh/upgrade/repeat/schema tests covering tenant keys, Catalog FKs, checks, indexes, idempotency, audit compatibility, and populated schema-4 preservation.
3. Implement tenant-scoped GORM stock/ledger persistence and atomic idempotent adjustment with Audit/Eventing writers; add isolation, rollback, and contention tests.
4. Implement atomic batch reservation, confirmation, release, and expiry operations with database time, deterministic locks, immutable ledger/outbox effects, failure injection, and synchronized PostgreSQL concurrency tests.
5. Add the batch availability resolver to public Catalog reads and test zero-default, in-stock transitions, bounded query behavior, and cross-store non-enumeration.
6. Add Inventory admin services/handlers, role enforcement, strict input/idempotency behavior, exact quantity reads, keyset pagination, and API/OpenAPI tests.
7. Add schema-gated API and worker composition, validated expiry configuration, cancellation-safe processing, and worker tests.
8. Update development status/task navigation and run focused formatting, lint, unit, migration, PostgreSQL, API/OpenAPI, tenant-isolation, transaction, worker, and concurrency checks before independent review.

## Verification strategy

| Requirement or risk | Required evidence |
| --- | --- |
| Quantity and lifecycle rules | Unit/schema tests for integer bounds, zero/non-zero rules, computed availability, UTC/database-time expiry boundaries, and legal/idempotent transitions |
| Tenant isolation | API/PostgreSQL tests where Store A uses Store B variant, stock, reservation, order reference, cursor, ledger, event, or idempotency identity and cannot observe or affect it |
| Adjustment atomicity | PostgreSQL failure-injection tests proving stock, version, ledger, audit, and outbox commit once or all roll back; exact/mismatched retries are distinguished |
| No oversell | Synchronized PostgreSQL last-unit and multi-line tests for concurrent reservations, reductions, confirmations, releases, and expiries with non-negative availability |
| Reservation idempotency | Tests for exact reserve/confirm/release/expire retries, changed allocation conflicts, one terminal effect, and stable results |
| Immutable ledger | Schema/repository tests prevent update/delete through supported paths and reconcile every on-hand delta with one resulting balance |
| Expiry | Database-time boundary tests, bounded discovery, duplicate workers, cancellation, no busy loop, and release exactly once without changing `on_hand` |
| Catalog availability | Handler/API tests for implicit zero, false/true transitions, active variants only, no exact-count leakage, bounded batch lookup, and host isolation |
| Authorization and contract | Owner/admin success; staff/customer/guest/inactive/suspended/unauthenticated denial; strict headers/bodies; stable problems; OpenAPI coverage |
| Migration and rollout | Fresh schema, populated version-4 upgrade, repeat no-op, exact definitions, schema-4 gating, schema-5 enablement, and constraint-lock reasoning |
| Architecture boundaries | Import/static tests and inspection proving no foreign-table query, no persistence types in domain/application, and no public reservation API |

Development checks: `gofmt`/`make fmt-check`, `make lint`, focused `go test` commands for Inventory, Catalog, Stores authorization, API/worker composition, migrations, and OpenAPI, focused integration tests against real PostgreSQL, and `git diff --check`.

Final verification after implementation and independent reviews: `make verify`. Vulnerability scanning remains part of that command even though no dependency is planned.

## Acceptance criteria

- [x] Given an authorized owner or admin and an active same-store variant, when a valid manual delta is submitted, then stock, version, immutable ledger, audit, and one outbox fact commit atomically and the exact retry has one effect.
- [x] Given insufficient availability, a stale version, conflicting idempotency reuse, invalid quantity/reason, an archived/foreign variant, or injected persistence failure, when adjustment runs, then no partial state commits and the response does not leak another store's data.
- [x] Given one or more variants with sufficient availability, when a reservation batch is created, then all quantities are reserved atomically until exactly ten minutes after database time; exact retries reuse the allocation and changed retries conflict.
- [x] Given concurrent attempts for the last unit or overlapping multi-line batches, when PostgreSQL resolves them, then at most available stock is reserved, no availability becomes negative, and no workflow deadlocks.
- [x] Given an active unexpired reservation, when it is confirmed, then physical stock is decremented once and matching ledger/outbox effects commit; release or expiry instead restores availability once without changing `on_hand`.
- [x] Given concurrent confirmation, release, expiry, adjustment, or duplicate workers, when operations race, then one legal outcome wins, retries are idempotent, and stock/ledger/reservation/event state remains consistent.
- [x] Given Store A and Store B, when either uses the other's Inventory identifiers, relationships, cursor, order reference, event identity, or idempotency key, then it cannot read, mutate, reserve, deduplicate, or infer the other store's data.
- [x] Given a public Catalog response, when availability changes, then each visible active variant exposes the current `in_stock` boolean without exact counts or Inventory identifiers; missing stock is out of stock.
- [x] Given schema 4 or schema 5, when the compatible binaries start, then existing APIs remain available, Inventory API/worker behavior is gated correctly, and fresh/upgrade/repeat migration checks pass.
- [x] Given the completed implementation, when required focused checks, both independent reviews, and final verification run, then all applicable gates pass on the same artifacts.

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
- Last request, completed stage, pending gates, and next suggested command: `continue task`; automatic delivery completed through implementation, review fixes, independent re-review, and final verification; no Task 011 gates remain.
- Plan revision and approval: revision 1 prepared on 2026-09-11 and approved by the user on 2026-09-11.
- Plan: affected modules, technical solution, compatibility/migration behavior, and ordered steps are recorded above.
- Verification: acceptance criteria map to unit, handler/API/OpenAPI, worker, migration, real PostgreSQL tenant/transaction/concurrency, lint, and final `make verify` evidence as recorded above.
- Implementation evidence: added migration 5; Inventory domain/application, GORM transactions, Gin admin routes, Catalog availability projection, schema-gated API/worker composition, worker configuration, OpenAPI, audit/Eventing integration, and focused tests. Exact adjustment replays preserve their original result, and no public route creates or mutates reservations.
- Focused verification: `go test ./...` passed after fixes; migration 5 reset/up and populated version-4 upgrade/repeat tests passed; focused real-PostgreSQL Inventory and migration suites passed with first-adjustment, torn-read, writer rollback, tenant identity/cursor, multi-line contention, terminal/adjustment race, duplicate expiry worker, database-time boundary, and immutable-ledger coverage. The pre-review `make test-integration` and `make lint` passed; final `make verify` remains pending.
- Pre-existing working tree: before Task 011, documentation changes existed in `docs/architecture.md`, `docs/database-design.md`, `docs/development-status.md`, `docs/domain-model.md`, `docs/implementation-roadmap.md`, `docs/product-requirements.md`, `docs/testing-strategy.md`, and `tasks/README.md`; `tasks/archive/010-defer-coupons-until-after-mvp.md` was untracked. Task 011 must preserve them.
- Architect review: final pass on 2026-09-12 after concurrency/rollback evidence, exact-read consistency, materialization serialization, Catalog transaction-boundary, immutable-ledger, and repeatable test-cleanup corrections.
- Security review: final pass on 2026-09-12 after tenant/authorization/race evidence, lifecycle rollback injection, immutable-ledger bypass removal, and guarded disposable cleanup corrections.
- Final evidence: `make verify` passed on 2026-09-12 with formatting, zero-issue lint, all unit tests, all real integration tests at schema 5, and `govulncheck` reporting no vulnerabilities in called code. An earlier aggregate run reached the same tests but its first vulnerability-database fetch timed out; `make vulncheck` then passed and the complete `make verify` rerun passed.
