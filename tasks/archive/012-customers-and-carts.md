# Task: Build Customers and carts

## Context

This is the next work explicitly selected by [development status](../../docs/development-status.md) after the accepted [single-location Inventory slice](011-single-location-inventory.md). Requirements come from the [product requirements](../../docs/product-requirements.md), [architecture](../../docs/architecture.md), [domain model](../../docs/domain-model.md), [database design](../../docs/database-design.md), [testing strategy](../../docs/testing-strategy.md), and [implementation roadmap](../../docs/implementation-roadmap.md).

Task 004 remains inactive with two accepted Identity risks, and task 005 remains inactive after the user skipped its final verification. The working tree is otherwise clean and those records are outside this task.

## Outcome

- An authenticated registered shopper can create and maintain one store-scoped customer profile and saved Vietnamese delivery addresses without exposing personal data across users or stores.
- An authenticated registered user or guest can create one active VND cart, read it, add or replace variant quantities, remove lines, and abandon it.
- Cart responses contain current estimated VND prices and availability from the owning Catalog, Pricing, and Inventory modules; carts never reserve stock and checkout will recalculate everything authoritatively.
- Inactive carts expire after 30 days using PostgreSQL database time. Archived profiles/addresses and terminal carts remain available only where future historical workflows explicitly require them.

## In scope

- Add cohesive `internal/customers` and `internal/carts` modules with domain rules, application interfaces, GORM persistence, and Gin shopper handlers.
- Add migration `00006` for tenant-owned customers, customer addresses, carts, and cart lines with composite tenant keys, ownership checks, optimistic versions, expiry indexes, and store-scoped uniqueness.
- Support one customer profile per store user, profile replacement, saved-address create/list/get/replace/archive, and purpose-limited self-service reads.
- Support one active cart per store principal and VND currency, atomic create-or-return, current-cart read, line upsert/removal, abandonment, and bounded overdue expiry.
- Resolve current sellable variant details, prices, and inventory availability through explicit application interfaces owned by Catalog, Pricing, and Inventory.
- Add schema-gated API and worker composition, typed identifiers, OpenAPI coverage, task/status navigation, and focused unit, HTTP, migration, PostgreSQL, isolation, and concurrency tests.

## Out of scope

- Checkout, Orders, inventory reservations, shipping quotes/configuration, payment selection, guest order grants, cancellation, refunds, or fulfillment.
- Cart merging or ownership transfer when a guest registers or signs in; wish lists, saved-for-later lines, coupon fields, discounts, tax invoices, and multi-currency carts.
- Administrative customer search/export, marketing consent, customer notes, address verification/geocoding, or authoritative Vietnamese administrative-division datasets.
- Kafka publication, cart/customer domain events, notification delivery, per-membership permission changes, Task 004 fixes, deployment, or production migration execution.

## Proposed HTTP contract

All routes use the resolved tenant host and an authenticated store-bound bearer principal. Customer-profile routes accept users only; cart routes accept users or guests.

- `GET /api/v1/customer/profile` returns the current user's active profile or 404.
- `PUT /api/v1/customer/profile` creates with `expected_version: 0` or replaces with the current positive version and fields `full_name`, `email`, and `phone`.
- `GET /api/v1/customer/addresses` lists the current user's active saved addresses with a bounded keyset cursor.
- `POST /api/v1/customer/addresses` creates a saved address.
- `GET /api/v1/customer/addresses/{address_id}` returns one active owned address.
- `PUT /api/v1/customer/addresses/{address_id}` replaces one address using `expected_version`.
- `DELETE /api/v1/customer/addresses/{address_id}` archives one address using `expected_version`.
- `POST /api/v1/cart` atomically creates or returns the caller's one active VND cart.
- `GET /api/v1/cart` returns the active cart and its current estimates or 404.
- `PUT /api/v1/cart/lines/{variant_id}` sets a positive integer `quantity` using cart `expected_version`; concurrent or stale mutation conflicts.
- `DELETE /api/v1/cart/lines/{variant_id}` removes a line using cart `expected_version`.
- `DELETE /api/v1/cart` marks the cart abandoned using `expected_version`; a later `POST` creates a new active cart.

An address contains `label`, `recipient_name`, `phone`, `line1`, optional `line2`, `ward`, `district`, `province`, optional `postal_code`, and fixed `country_code: "VN"`. Administrative names are normalized bounded text rather than unstable externally managed codes. Responses use RFC 3339 UTC timestamps and never return normalized lookup columns.

Cart lines return a purpose-limited current variant snapshot, requested quantity, current unit price/line estimate when priced, and `sellable`/`in_stock` indicators. If a retained line becomes unpublished, unpriced, or unavailable, the cart remains readable, that line is marked unavailable without leaking foreign data, the cart is not checkout-ready, and no misleading grand estimate is returned. Validation uses 400, unauthenticated or wrong principal type uses 401, owned-resource absence and cross-store/cross-user probes use the same 404, stale versions use 409, and dependency failures use 503.

## Invariants and security constraints

- Store scope comes only from validated host and authenticated context. Every table, key, constraint, repository operation, job candidate, and cross-module call receives explicit `store_id`.
- Customer access is both store- and user-scoped. Email/phone normalization supports only same-store owned workflows and never serves as authentication or an unscoped lookup key.
- Customer and address records are archived, not physically deleted. Archived addresses cannot be edited or selected for future checkout, and future orders must use immutable snapshots rather than these rows.
- Customer and cart APIs minimize personal data, do not log bodies/contact values/tokens, and return non-enumerating errors for another user or store.
- A cart has exactly one immutable owner: `user_id` or `guest_id`. Database checks and tenant-aware foreign keys prevent mixed, missing, or cross-store ownership.
- At most one active cart exists for `(store_id, owner_kind, owner_id, currency)`. Concurrent create-or-return attempts converge on the same cart.
- Cart lines contain unique same-store variant IDs and positive bounded integer quantities. Cart mutations are atomic and guarded by optimistic version.
- Cart rows persist only shopping intent, not authoritative price, stock, product, address, or order snapshots. Cart reads batch through consumer-owned Catalog, Pricing, and Inventory interfaces and never query another module's tables directly.
- Estimates use integer VND money and checked arithmetic. Any missing/currently invalid product or price makes the cart not checkout-ready. Availability is advisory and does not reserve stock.
- Cart creation and each successful mutation use PostgreSQL database time to set or refresh `expires_at` to 30 days. Expiry rechecks state and database time under lock; duplicate workers have one terminal effect.
- Domain and application packages do not import Gin, GORM, SQL, Kafka, HTTP, or another module's persistence types.

## Technical solution

### Customers

1. Model customer profiles and addresses as separate optimistic aggregates with bounded normalized contact and Vietnam address value objects.
2. Key one customer to one same-store user. Keep profile/address authorization in customer application services by requiring both `store_id` and `user_id` on every operation.
3. Use archive transitions rather than deletes, active-only self-service queries, stable keyset address pagination, and tenant/user predicates on every update.

### Carts

1. Model an immutable user-or-guest owner, VND currency, `active`, `converted`, `expired`, and `abandoned` states, optimistic version, database-time expiry, and positive unique variant quantities.
2. Implement atomic create-or-return with a tenant-scoped partial unique index and conflict-safe retry. Mutations lock the cart, recheck ownership/state/version/deadline, update the line set, increment version once, and refresh expiry in one transaction.
3. Keep cart persistence limited to owner, lifecycle, and requested variant quantities. Assemble current estimates after loading the cart through batched Catalog sellability, Pricing price, and Inventory availability interfaces.
4. Discover overdue carts in bounded non-locking batches, then claim and recheck each cart under a short transaction. The worker records counts and errors without personal data and does not require Kafka.

### Authentication, HTTP, composition, and migration

1. Add one Identity shopper-authentication boundary that accepts either a valid user or guest access token for the resolved store and emits the existing typed principal. Customer routes reject guest principals before invoking the module.
2. Add strict Gin handlers with bounded bodies, unknown-field rejection, identifier/cursor parsing, request timeouts, request IDs, stable problem responses, and no caller-supplied owner or tenant fields.
3. Add migration 6 with composite tenant keys/FKs, exactly-one-owner checks, tenant/user uniqueness, active-row and expiry indexes, positive quantity and fixed-VND checks, UTC timestamps, and optimistic versions.
4. Register Customer and Cart routes and cart expiry work only at schema version 6. Existing schema-5 behavior remains available while the migration is pending.
5. Update OpenAPI and development status without changing existing Inventory, Catalog, Pricing, or Identity public contracts.

## Compatibility and migration effects

- Migration 6 is additive: it creates four initially empty tables and indexes and does not rewrite existing rows. Composite foreign keys reference existing tenant-owned users, guests, and Catalog variants without transferring ownership of those tables.
- The principal deployment locks are catalog/user/guest metadata locks while adding foreign keys and index creation on empty new tables. Production rollout is compatible binary first, migration 6 second, then normal restart/schema refresh.
- At schema 5, the compatible binary omits Customer/Cart routes and expiry behavior. Recovery after applying schema 6 is forward-fix; older binaries ignore the additive tables but are not a schema rollback mechanism.
- This task adds no runtime dependency and emits no new event contract.

## Ordered implementation plan

1. Add Customer/Address/Cart identifiers, domain value objects, lifecycle and version rules, owner types, bounded quantities, VND estimate arithmetic, and focused unit tests.
2. Add migration 6 and fresh/populated-upgrade/repeat/schema tests for composite tenant ownership, active uniqueness, exactly-one-owner, positive quantities, archive/expiry indexes, and existing schema-5 preservation.
3. Implement tenant- and user-scoped Customer GORM persistence and application services with archive semantics, optimistic concurrency, pagination, and cross-store/cross-user tests.
4. Implement Cart GORM persistence for create-or-return, line mutations, abandonment, and expiry with database time, deterministic locking, synchronized concurrent creation/mutation tests, and rollback evidence.
5. Add batched Catalog/Pricing/Inventory projection interfaces and Cart estimate assembly with unavailable-line, overflow, bounded-query, and cross-store tests.
6. Add user-or-guest shopper authentication, strict Customer/Cart handlers, ownership/non-enumeration enforcement, API/OpenAPI tests, and schema-5/schema-6 composition tests.
7. Add bounded cart expiry processing to the worker with configuration, cancellation, duplicate-worker, and failure-isolation tests.
8. Update task navigation and development status, run focused development checks, then proceed automatically through independent architecture/security review, fixes, and final verification.

## Verification strategy

| Requirement or risk | Required evidence |
| --- | --- |
| Customer privacy and tenant isolation | API/PostgreSQL tests where another user, guest, or Store B cannot read or mutate Store A's profile/address or infer it from errors |
| Address/profile rules | Unit/schema tests for normalized bounded fields, fixed `VN`, optimistic versions, archive behavior, and duplicate user-profile prevention |
| Cart ownership | Schema/application tests for exactly one immutable user/guest owner, store-matching foreign keys, principal-kind denial, and non-enumerating foreign probes |
| Active cart uniqueness | Synchronized PostgreSQL tests proving concurrent create-or-return yields one active cart and terminal carts permit a later new cart |
| Cart mutation atomicity | PostgreSQL tests for stale versions, duplicate/positive quantities, line upsert/removal, expiry boundary, injected rollback, and one version increment per mutation |
| Estimates and module boundaries | Tests for integer VND arithmetic, current batched Catalog/Pricing/Inventory data, no persisted authoritative snapshot, unavailable lines, no N+1 queries, and no foreign-table access |
| Expiry | Database-time boundary, bounded discovery, duplicate workers, cancellation, no busy loop, and one active-to-expired transition |
| HTTP/OpenAPI | User profile/address success, user/guest cart success, guest customer denial, strict input/body limits, stable problems, request IDs, and contract coverage |
| Migration and rollout | Fresh schema, populated version-5 upgrade, repeat no-op, exact constraints/indexes/FKs, schema-5 gating, and schema-6 enablement |

Development checks: `gofmt`/`make fmt-check`, `make lint`, focused `go test` commands for Customers, Carts, Identity shopper authentication, API/worker composition, migrations, OpenAPI, and focused integration tests against real PostgreSQL, followed by `git diff --check`.

Final verification after implementation and independent reviews: `make verify`. This broad cross-module and authentication-sensitive slice requires the full command, including vulnerability scanning.

## Acceptance criteria

- [x] Given an authenticated registered user, when the user creates or replaces a valid profile, then exactly one store/user-scoped profile is persisted with normalized contact fields and optimistic conflict detection.
- [x] Given an owned active customer profile, when the user creates, reads, replaces, lists, or archives a Vietnamese address, then only that user in that store can observe or mutate it and archived data is excluded from active use.
- [x] Given another user, a guest, another store, a malformed identifier, or an archived resource, when profile/address access is attempted, then access fails closed without existence or personal-data leakage.
- [x] Given an authenticated user or guest, when the caller creates a cart concurrently or retries, then one active VND cart with one immutable principal owner is returned.
- [x] Given an active owned cart and same-store published priced variant, when a positive quantity is set or a line is removed with the current version, then the mutation is atomic, increments the cart version once, and refreshes the 30-day database-time expiry.
- [x] Given stale version, invalid/overflowing quantity, foreign or unavailable variant, terminal/expired cart, or injected persistence failure, when a mutation runs, then no partial state commits and no foreign resource is disclosed.
- [x] Given an active cart, when it is read, then current variant, VND price, and availability are resolved in bounded batches; totals use integer arithmetic, no stock is reserved, and any unavailable line makes checkout readiness false without a misleading total.
- [x] Given an overdue active cart or concurrent expiry workers, when expiry runs, then the cart transitions to expired exactly once after a locked database-time recheck and cannot be mutated afterward.
- [x] Given abandonment, when the owner closes an active cart, then it becomes terminal and a later create call may establish one new active cart without deleting history.
- [x] Cross-store and cross-user denial tests cover every new tenant-owned resource and all owner, cursor, variant, and foreign-key probes.
- [x] Migration 6, schema gating, API/OpenAPI contracts, architecture boundaries, focused checks, independent reviews, and final `make verify` pass on the final artifacts.

## Required completion report

- Behavior and design summary
- Files/modules changed
- Commands executed with results
- Risks, assumptions, and follow-ups

## Delivery record

Follow [the project workflow](../../docs/development-workflow.md). Keep this record concise and update it as work progresses.

- Stage: complete
- Active task: no
- Last command, completed stage, pending gates, and next suggested command: automatic delivery completed through implementation, review fixes, independent re-reviews, and final verification; no task 012 gates remain.
- Plan revision and approval: revision 1 prepared and explicitly approved by the user on 2026-09-12.
- Plan: add separate Customers and Carts modules, migration 6, user profile/address APIs, user-or-guest cart APIs, current batched estimates, database-time expiry, schema gating, OpenAPI/status updates, and risk-matched tests without implementing checkout.
- Verification: map acceptance criteria to domain, API, PostgreSQL migration/isolation/concurrency/rollback, cross-module boundary, worker, and OpenAPI tests; run focused development checks and final `make verify` after review fixes.
- Architect review: passed final re-review after snapshot-consistent existing-cart reads, a race-free successful-create response, strict contact validation, and expanded persistence, migration, concurrency, isolation, and HTTP evidence.
- Security review: passed final re-review after broader mutation/FK/owner/authentication isolation evidence; unbounded authenticated address/cart-history growth remains a low advisory for a later abuse-control policy.
- Final evidence: `make verify` passed on 2026-09-12 with formatting, zero-issue lint, all unit tests, all real integration tests at schema 6, and `govulncheck` reporting no vulnerabilities in called or imported code. Both independent reviewers passed the same final artifact set.
