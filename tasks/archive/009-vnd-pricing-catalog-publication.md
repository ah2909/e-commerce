# Task: Add VND pricing and price-backed Catalog publication

## Context

This is the next work explicitly selected by [development status](../../docs/development-status.md) after the accepted [Catalog authoring foundation](008-catalog-authoring-foundation.md). Requirements come from the [product requirements](../../docs/product-requirements.md), [architecture](../../docs/architecture.md), [domain model](../../docs/domain-model.md), [database design](../../docs/database-design.md), [event catalog](../../docs/event-catalog.md), [testing strategy](../../docs/testing-strategy.md), and [implementation roadmap](../../docs/implementation-roadmap.md).

Task 004 remains inactive with two accepted Identity risks. This task uses the existing host-selected tenant and workforce authorization boundary without changing those risks.

## Outcome

- Authorized owners and admins can manage store-scoped VND storefront price lists and variant prices.
- An authorized owner or admin can publish a draft product only when at least one active variant has a deterministically selected current storefront price.
- Public callers can list published products and retrieve one by slug without authentication; responses expose only active variants that have a current VND price.
- Publication and its minimal `product.published.v1` outbox fact commit atomically without requiring Kafka.
- A published product that later has no current price remains historically published but disappears from public reads until pricing becomes current again.

## In scope

- Add a cohesive `internal/pricing` module with Money, price-list and price domain rules, application services, GORM persistence, and Gin administration handlers.
- Add migration `00004` for tenant-owned price lists and variant prices plus the minimal Eventing-owned transactional outbox schema required by publication.
- Add typed price-list and event identifiers using the existing opaque UUID convention.
- Support create, retrieve, keyset-list, atomically replace, and archive operations for price-list aggregates containing variant prices.
- Support the `storefront` channel, VND currency, optional UTC validity bounds, integer priority, optimistic versions, and deterministic current-price resolution.
- Add an authenticated Catalog publish transition guarded by `expected_version`, current price readiness, and one explicit cross-module PostgreSQL unit of work.
- Add unauthenticated, host-scoped public Catalog list and slug-detail routes with keyset pagination and resolved prices.
- Extend permissions, OpenAPI, migrations, composition, current-status documentation, and focused tests.

## Out of scope

- Coupons, coupon redemption or usage accounting, customer-specific pricing, wholesale channels, multiple currencies, or tax calculation beyond identifying returned VND prices as VAT-inclusive.
- Inventory quantities or availability, carts, checkout price snapshots, orders, search, analytics, or caching.
- Unpublishing, editing a published Catalog aggregate, product republishing, or emitting update/archive events.
- Kafka publication, outbox retry workers, consumer inboxes, dead-letter handling, or Eventing administration APIs.
- Media URL signing or binary/object-storage access; public responses retain validated media metadata and object keys only.
- Fixing Task 004 risks, production deployment, production migration execution, or external publishing.

## Proposed HTTP contract

Administrative routes use the resolved tenant host, user authentication, active workforce membership, and a new `pricing.manage` permission granted to owner/admin only. Catalog publication continues to require `catalog.manage`.

- `POST /api/v1/admin/pricing/price-lists` creates an active storefront VND price list with one or more prices.
- `GET /api/v1/admin/pricing/price-lists?limit=&cursor=` lists price lists by `updated_at DESC, price_list_id DESC`.
- `GET /api/v1/admin/pricing/price-lists/{price_list_id}` returns the complete aggregate.
- `PUT /api/v1/admin/pricing/price-lists/{price_list_id}` atomically replaces mutable metadata and prices using `expected_version`.
- `POST /api/v1/admin/pricing/price-lists/{price_list_id}/archive` archives the list using `expected_version`.
- `POST /api/v1/admin/catalog/products/{product_id}/publish` publishes a ready draft using `expected_version`.

Public routes require a valid active store host but no user or guest credential.

- `GET /api/v1/catalog/products?limit=&cursor=` lists storefront-visible products by `published_at DESC, product_id DESC`.
- `GET /api/v1/catalog/products/{slug}` returns one storefront-visible product by canonical slug.

Public product responses omit `store_id`, authoring versions, archived children, barcodes, and price-list metadata. Each visible variant includes its public option/physical fields and `price: {amount_minor, currency, vat_inclusive}`. Public list/detail return only published products with at least one currently priced active variant. Missing, cross-store, unpublished, or currently unpriced detail returns the same non-enumerating 404 problem.

Administrative validation uses 400, missing same-store resources use 404, uniqueness/version/window/readiness conflicts use 409, authentication uses 401, authorization uses 403, and dependency failures use 503. Responses retain the existing problem shape and RFC 3339 UTC timestamps.

## Invariants and security constraints

- Store scope comes only from the validated direct host and authenticated context where required; payloads, paths, cursors, slugs, and variant IDs never select a store.
- Every Pricing and outbox table, key, constraint, repository method, query, and mutation carries explicit `store_id`; Catalog and Pricing never query each other's tables directly.
- Money is a non-negative integer minor-unit amount paired with ISO currency. This slice accepts only `VND`, uses no floating point, and treats storefront values as VAT-inclusive presentation amounts.
- Price-list validity is a half-open UTC interval `[valid_from, valid_until)` with either bound optional. Database time is authoritative for current-price selection and publication readiness.
- Price selection filters active storefront VND lists whose validity contains database time, then orders by priority descending and `price_list_id` ascending. A write that creates an overlapping equal-priority price for the same variant is rejected as ambiguous.
- Price lists contain at least one price. Prices reference active same-store Catalog variants; foreign, archived, or unknown variants behave as unavailable and cannot leak existence.
- Aggregate replacement and archive use guarded store, identifier, and version predicates and commit no partial state on stale or invalid input.
- Publication requires a draft product with at least one active variant having a current price. The publication coordinator locks Pricing readiness and the Catalog product in one transaction, rechecks with database time, updates the Catalog version/status, and appends the outbox row atomically.
- All Pricing writes and publication use one documented tenant-scoped advisory-lock order before row locks so readiness cannot race an archive or replacement into an invalid publication commit.
- Public reads resolve Catalog content and Pricing through explicit application interfaces. They never expose draft/archived products, unpriced variants, authoring-only identifiers, another store's values, or client-selectable price context.
- Pagination cursors are opaque, validated, tenant-bound by query execution, and capped. Public listing may scan bounded Catalog batches to fill a page after Pricing filters unpriced products; it never returns an unbounded result or offset contract.
- The outbox fact contains only store, product, Catalog version, correlation/request identity, event metadata, and occurrence time; it contains no credentials, price internals, or unnecessary personal data.
- Domain and application packages do not import Gin, GORM, SQL, Kafka, or foreign persistence types.

## Technical solution

### Pricing domain and application

1. Add a shared immutable Money value with integer `AmountMinor` and validated three-letter currency; Pricing constrains authored values to VND.
2. Model `PriceList` as the aggregate owner of store, channel, currency, priority, validity, status, version, and variant-price entries. Normalize and bound display names while preserving prices as explicit typed entries.
3. Define consumer-owned Pricing repository and current-price resolver interfaces with explicit `store_id`. Resolve database time inside persistence operations rather than trusting the API process clock.
4. Serialize same-store price-list writes with a tenant advisory lock, validate referenced active variants through a Catalog application interface, and reject equal-priority overlapping windows for the same variant.

### Catalog publication and public reads

1. Add the `draft -> published` domain transition; keep published editing and `published -> archived` outside this task so existing authoring semantics remain explicit.
2. Implement an infrastructure publication coordinator that owns the exceptional cross-module GORM transaction and calls narrow module-owned Pricing readiness, Catalog publish, and Eventing append ports. No participant directly queries another module's tables.
3. Append `product.published.v1` with aggregate version in the same transaction. Implement only Eventing's transaction-aware append storage; leave delivery mechanics for the Event workers slice.
4. Add Catalog public query use cases that fetch published content in bounded keyset batches and call Pricing's batch resolver. Filter variants without a current price and products left with no visible variant.
5. Keep public DTOs separate from authoring aggregates so store IDs, optimistic versions, internal status, barcodes, and price-list selection details cannot leak accidentally.

### Persistence and migration

1. Add `pricing_price_lists` and `pricing_prices` with composite tenant primary/foreign keys, VND/channel/status/window/amount/version checks, store-leading current-resolution indexes, and retained archived rows.
2. Add Eventing-owned `outbox_events` with `(store_id,event_id)` identity, tenant aggregate/version uniqueness, bounded JSON payload, publication-attempt metadata, and the future publisher's unpublished-claim index.
3. Add/adjust the published Catalog index only if query-plan evidence requires the slug/detail or keyset shape beyond migration 3's partial index.
4. Use GORM row mappings and explicit transactions over the existing pool; Goose SQL remains schema authority and `AutoMigrate` remains prohibited.
5. Use an expand-compatible rollout: deploy the task-009 binary while schema 3 is active with Pricing/publication/public routes disabled; apply migration 4; restart/roll the same binary to enable them. After migration 4, recover with a forward fix while retaining the compatible binary rather than rolling back to an older exact-schema binary.

### HTTP, authorization, and composition

1. Add `pricing.manage` to the fixed owner/admin policy; staff, customers, and guests deny by default.
2. Register admin Pricing and publish routes after host resolution, authentication, current membership, and permission enforcement. Register public Catalog routes after host validation and active-store resolution only.
3. Preserve strict JSON decoding, body/collection limits, identifier/cursor parsing, request timeouts, request IDs, structured error logging, and non-enumerating problem mapping.
4. Update OpenAPI with exact administrative/public schemas, bounds, security requirements, keyset cursors, VAT-inclusive money, and the absence of coupons/inventory availability.

## Compatibility and migration effects

- Existing schema-3 Catalog authoring data and routes remain compatible. Migration 4 is additive except for any evidence-backed Catalog index addition.
- The compatible binary accepts schema versions 3 and 4; at schema 3 it omits all task-009 routes and behavior, while existing Catalog authoring remains available.
- The proposed `/api/v1/admin/pricing`, Catalog publish, and `/api/v1/catalog/products` contracts are new public API decisions. Plan approval approves these namespaces, aggregate replacement semantics, public slug lookup, half-open validity, deterministic price selection, and the published-but-temporarily-hidden behavior.
- Outbox rows will accumulate until the future publisher exists. This is bounded by business writes rather than request volume, must be visible in development status, and prevents event loss without putting Kafka on the request path.
- No new runtime dependency is expected.

## Ordered implementation plan

1. Add typed IDs and Money/Pricing domain aggregates with normalization, currency, interval, amount, lifecycle, and deterministic-selection unit tests.
2. Add migration 4 and fresh/upgrade/repeat/schema tests for Pricing and outbox tenant keys, constraints, indexes, cross-store foreign keys, and populated schema-3 compatibility.
3. Implement tenant-scoped Pricing GORM repositories, ambiguity guards, optimistic aggregate writes, current-price resolution, and real PostgreSQL isolation/concurrency tests.
4. Implement the Catalog publish transition and cross-module transaction coordinator with readiness, lock-order, rollback/failure-injection, and outbox atomicity tests.
5. Add Pricing authorization/application services and strict Gin administration handlers with role, tenant-denial, window, and conflict tests.
6. Add public Catalog DTO/query services and Gin list/detail handlers with price filtering, keyset stability, non-enumeration, host isolation, and bounded-scan tests.
7. Update OpenAPI, API composition/schema gating, development status, database/event documentation where implementation detail becomes concrete, and task navigation.
8. Run focused formatting, lint, unit, migration, PostgreSQL, API/OpenAPI, tenant-isolation, transaction, and concurrency checks before the independent review stage.

## Verification strategy

| Requirement or risk | Required evidence |
| --- | --- |
| Money and price rules | Unit/schema tests for integer-only non-negative VND, overflow/bounds, half-open intervals, UTC normalization, and no floating-point contract |
| Deterministic selection | Unit/PostgreSQL tests for validity boundaries, priority ordering, stable tie-break, equal-priority overlap rejection, archive, and database-clock behavior |
| Tenant isolation | API and PostgreSQL tests where Store A uses Store B price-list, variant, product, slug, cursor, or event identifiers and observes no data/state leakage |
| Scoped uniqueness | PostgreSQL tests proving same-store conflicts and identical IDs/business values where allowed across stores |
| Pricing aggregate atomicity | Failure-injection and PostgreSQL tests proving invalid/stale entries, references, overlap, or archive errors roll back the complete write |
| Publication readiness | Domain/application tests for draft-only transition and at least one active currently priced variant; unpriced and archived variants cannot satisfy readiness |
| Cross-module concurrency | Synchronized PostgreSQL tests for publish versus price replace/archive, two publishers, stale product version, fixed lock order, no deadlock, one coherent winner, and unchanged state on failure |
| Outbox atomicity | PostgreSQL tests proving Catalog publication and one tenant-scoped fact commit together or both roll back, with aggregate/version deduplication |
| Public visibility | Handler/API tests for published priced content only, unpriced-variant filtering, zero-current-price product suppression, later price restoration, no authoring-field leakage, and non-enumerating 404s |
| Authorization | Owner/admin success plus staff/customer/guest/inactive/suspended/unauthenticated denial on admin routes; public routes require only a valid active tenant host |
| Pagination | Stable published keysets across price filtering and intervening writes, bounded scanning, malformed/tampered cursors, limit bounds, and no offset contract |
| Migration and rollout | Fresh schema, populated version-3 upgrade, repeat no-op, exact schema definitions, schema-3 route gating, schema-4 enablement, and compatibility reasoning |
| Boundaries and contract | OpenAPI coverage and import/static tests proving domain/application isolation and no direct cross-module table access |

Development checks: `gofmt`/`make fmt-check`, `make lint`, focused `go test` commands for shared money, Pricing, Catalog, Stores authorization, API composition, and OpenAPI, focused integration tests against real PostgreSQL, and `git diff --check`.

Final verification after implementation and independent reviews: `make verify`. Vulnerability scanning remains part of that command even though no dependency is planned.

## Acceptance criteria

- [x] Given an authorized owner or admin, when a valid VND storefront price list is created, replaced, listed, retrieved, or archived, then the tenant-owned aggregate behaves atomically with optimistic versioning and deterministic current-price selection.
- [x] Given equal-priority overlapping current windows for the same store and variant, when the conflicting price is authored, then the request fails without partial state; different stores remain independent.
- [x] Given Store A and Store B, when either uses the other's price-list, variant, product, slug, cursor, or event identifier, then it cannot read, mutate, publish, deduplicate, or infer the other store's data.
- [x] Given a draft product with at least one active currently priced variant, when an authorized caller publishes with the current version, then Catalog status/version and one `product.published.v1` outbox fact commit atomically.
- [x] Given stale versions, no current price, archived/foreign variants, concurrent price archive/replacement, concurrent publication, or injected persistence failure, when publication runs, then no invalid or partial publication/outbox state commits and no workflow deadlocks.
- [x] Given a valid active store host, when public list/detail routes are called without credentials, then only published products and currently priced active variants are returned with VAT-inclusive integer VND prices and no authoring-only fields.
- [x] Given a published product loses every current price, when public reads run, then the product is unavailable without changing its historical published state; a later current price restores visibility.
- [x] Given malformed input/cursors, unsupported currency/channel, invalid host, suspended/unknown store, or unauthorized admin principal, when routes are called, then stable non-enumerating problems are returned at the correct boundary.
- [x] Given migration 4 and the updated contract, when required verification runs, then fresh/upgrade/repeat migrations, focused unit/API/OpenAPI tests, real PostgreSQL tenant/transaction/concurrency tests, lint, and final `make verify` pass.

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
- Last request, completed stage, pending gates, and next suggested command: `continue`; implementation, fixes, both independent review passes, final verification, and archival are complete; start the coupons and redemption-accounting task next.
- Plan revision and approval: revision 1 prepared and approved by the user on 2026-09-10.
- Plan: affected modules, technical solution, compatibility/migration behavior, and ordered steps are recorded above.
- Verification: acceptance criteria map to unit, API/OpenAPI, migration, tenant-isolation, atomicity, outbox, and synchronized PostgreSQL concurrency evidence above; final command is `make verify`.
- Implementation evidence: added shared integer Money and Pricing/Event IDs; tenant-owned Pricing domain/application/GORM/Gin layers; migration 00004 price-list, price, and outbox tables; Catalog publication/public projections; transaction-aware publication coordinator; permissions, schema gating, OpenAPI, status, and fixture cleanup updates.
- Focused verification: `make fmt-check` passed; `make lint` passed with zero issues; `make test` passed; OpenAPI JSON parsing/reference/operation/schema tests passed; `git diff --check` passed with only a line-ending warning. A guarded `test-reset` followed by `make test-integration` applied migrations 00001 through 00004; the first full integration run exposed two Task-009 compatibility updates (Catalog lock-observation expectations and fixture cleanup), which were corrected. Focused reruns passed `./cmd/migrate`, `./internal/catalog/infrastructure/postgres`, and `./internal/pricing/infrastructure/postgres`, including concurrent publication and outbox rollback.
- Architect review: changes required on tracked diff `69279d4f6400fdb86bf3b98ade78e9520f47948c`. Three high evidence gaps covered publish-versus-price concurrency, deterministic price/public pagination behavior, and exact migration definitions; one medium defect mapped archived-list replacement to 400 instead of 409. Module ownership, tenant predicates, Money, transaction/outbox atomicity, lock ordering, public DTOs, schema gating, and OpenAPI structure were otherwise sound.
- Security review: changes required on the same artifacts. Medium findings covered transaction-start `CURRENT_TIMESTAMP` allowing post-wait expired publication, excessive unauthenticated public-list query fan-out, and missing tenant/concurrency/schema evidence. Middleware order, tenant predicates/FKs, bound SQL, input bounds, DTO redaction, and outbox privacy/atomicity were otherwise sound.
- Fix disposition: publication now reads `clock_timestamp()` only after acquiring the shared tenant advisory lock and passes that exact timestamp through price readiness and event creation. Public listing scans at most three 50-product batches, returns a continuation cursor when the work budget is exhausted, and hydrates every repository batch with four fixed queries. Archived price-list replacement now returns `ErrConflict`. Tests now cover validity boundaries and priority selection, cross-store get/replace/archive/cursors/relationships, same price-list IDs across stores, restored visibility, cross-store public cursors, publish-versus-archive/replace serialization, post-lock expiry, outbox rollback/concurrent publication, same event IDs across stores, bounded public scanning, fixed-query public hydration, and complete migration constraint/index definitions.
- First re-review: both reviewers closed the timestamp, concurrency, deterministic selection, tenant, migration, and conflict-mapping findings, but found the bulk loader had been wired only to the admin list while `ListPublished` retained per-product hydration. The public path now uses the bulk loader, and its repository integration test asserts four queries for a three-product batch.
- Post-fix evidence: `go test ./...` passed; non-cached focused PostgreSQL tests passed for `./cmd/migrate`, Pricing repository, and Catalog repository, including the final fixed-query regression; `make fmt-check`, zero-issue `make lint`, and `git diff --check` passed. Latest tracked diff hash: `dd8a059536d8c876d1aa9d7c3a79701eaf0b6949`; untracked task files remain in reviewer scope.
- Final reviews: architecture and security reviewers passed tracked diff `dd8a059536d8c876d1aa9d7c3a79701eaf0b6949` plus all untracked Task-009 artifacts with no blocking or actionable findings. The architecture reviewer requested one non-blocking status correction removing Pricing and outbox from the list of absent schema areas; it is applied before final verification.
- Final evidence: after the first `make verify` attempt passed formatting, zero-issue lint, and unit tests, it stopped because the disposable PostgreSQL dependency was not running. `make dev-deps` started healthy PostgreSQL, Redis, and Kafka containers. The complete rerun of `make verify` then passed formatting, zero-issue lint, all unit tests, all schema-4 PostgreSQL integration tests, and vulnerability scanning with zero vulnerabilities in called code or imported packages.
