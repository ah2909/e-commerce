# Task: Build the tenant-safe Catalog authoring foundation

## Context

This begins roadmap slice 4 after the accepted Gin/GORM migration in [task 007](007-adopt-gin-gorm-project-wide.md). The target behavior comes from the [product requirements](../../docs/product-requirements.md), [architecture](../../docs/architecture.md), [domain model](../../docs/domain-model.md), [database design](../../docs/database-design.md), [testing strategy](../../docs/testing-strategy.md), and [implementation roadmap](../../docs/implementation-roadmap.md).

[Development status](../../docs/development-status.md) explicitly selects Catalog as the next work. Task 004 remains unaccepted with its two documented medium Identity risks knowingly skipped; this task neither fixes nor relies on those races beyond the existing authenticated tenant boundary.

## Outcome

- Authorized store workforce can create, edit, retrieve, keyset-list, and archive a store-owned physical-product catalog.
- A product is created atomically with at least one variant and may reference store-owned categories and ordered object-storage media metadata.
- Product slugs, category slugs, variant SKUs, and non-empty barcodes are normalized and unique within one store while the same values remain valid in another store.
- Categories form an acyclic store-local hierarchy, variants retain validated option values and physical measurements, and optimistic versions reject lost updates.
- Draft and archived catalog data is available only through authenticated workforce endpoints. Storefront publication and public catalog reads remain unavailable until Pricing can prove a current price.

## In scope

- Add a cohesive `internal/catalog` module with domain, application, PostgreSQL/GORM infrastructure, and Gin transport packages.
- Add forward migration `00003` for products, variants, categories, product-category assignments, and ordered product media references.
- Add typed IDs for products, variants, categories, and media using the existing opaque UUID convention.
- Support draft product creation, aggregate editing, authenticated detail/list reads, and irreversible archive transitions.
- Support category creation, editing, authenticated hierarchy/list reads, and archival when no active product assignment or child prevents it.
- Store variant option values as a validated bounded JSON object; store weight in grams and dimensions in integer millimetres.
- Store media metadata and object keys only; validate ordering, accessible alternative text, and store-local ownership without uploading binary content.
- Add owner/admin `catalog.manage` authorization. Staff remain denied until a later explicit staff-permission grant mechanism exists.
- Extend OpenAPI, migration tests, API composition, development status, and focused operating/design documentation for implemented behavior.

## Out of scope

- Product publication, public/storefront catalog endpoints, price lists, money, coupons, VAT calculation, or any workaround that allows an unpriced product to publish.
- Inventory stock items, availability, reservations, carts, checkout, orders, search indexing, analytics, or event publication.
- Object-storage upload, URL signing, image processing, binary metadata inspection, or deleting referenced objects.
- A generic staff-permission persistence/grant API, custom roles, or changing existing membership behavior.
- Product deletion, slug/SKU/barcode reuse after archive, category reparenting across stores, offset pagination, or arbitrary JSON attributes.
- Fixing the two accepted Task 004 risks, production deployment, migration execution against production, or external publishing.

## Proposed HTTP contract

All routes use the resolved tenant host, user authentication, current active workforce membership, and `catalog.manage`. Guests and users without an active authorized membership receive the existing non-enumerating authentication/authorization problems.

- `POST /api/v1/admin/catalog/categories` creates a draft-active category.
- `GET /api/v1/admin/catalog/categories` returns the bounded store-local hierarchy.
- `PUT /api/v1/admin/catalog/categories/{category_id}` replaces mutable category fields using `expected_version`.
- `POST /api/v1/admin/catalog/categories/{category_id}/archive` archives a category using `expected_version`.
- `POST /api/v1/admin/catalog/products` atomically creates a draft product with one or more variants plus optional category assignments and media.
- `GET /api/v1/admin/catalog/products?limit=&cursor=` lists draft and archived products by `updated_at DESC, product_id DESC` using an opaque validated cursor.
- `GET /api/v1/admin/catalog/products/{product_id}` returns the complete authoring aggregate.
- `PUT /api/v1/admin/catalog/products/{product_id}` atomically replaces mutable product fields, variants, category assignments, and media using `expected_version` while preserving supplied child IDs.
- `POST /api/v1/admin/catalog/products/{product_id}/archive` archives the product and its active variants using `expected_version`.

Successful writes return the resulting aggregate. Validation uses 400, missing same-store resources use 404, uniqueness/version/state conflicts use 409, authentication uses 401, authorization uses 403, and dependency failures use 503. All problems retain `code`, `message`, `details`, and `request_id`; responses expose UTC RFC 3339 timestamps.

## Invariants and security constraints

- Store scope comes only from authenticated tenant context; request bodies, paths, option JSON, cursors, and media metadata cannot select or override a store.
- Every Catalog table, key, foreign key, unique constraint, index, repository method, query, update, and archive operation carries explicit `store_id`.
- Product, variant, category, and media identifiers from another store behave as absent and cannot alter relationships or leak existence.
- Products are created in `draft`, contain at least one variant, and move only from `draft` to `archived` in this task. The schema reserves `published`, but no application transition can enter it yet.
- Archived products, variants, categories, slugs, SKUs, and barcodes remain retained and cannot be reused.
- Category parents belong to the same store; self-parenting, cycles, excessive depth, and archiving a category with active children or product assignments are rejected.
- SKU and slug canonicalization is deterministic. User-facing Unicode text is normalized, length-bounded, and never used as tenant authority.
- Option values are a bounded JSON object of normalized string keys and string values with no duplicate canonical keys; JSON cannot carry identifiers, prices, stock, status, or dimensions.
- Weight grams and length/width/height millimetres are non-negative bounded integers. A physical variant must provide a complete dimension triple when any dimension is supplied.
- Media positions are unique and contiguous per product; object keys are relative, bounded, and cannot contain traversal segments, control characters, schemes, query strings, or fragments.
- Aggregate replacement and archive use guarded `store_id`, identifier, and version predicates. A stale command makes no partial changes.
- Catalog domain and application packages do not import Gin, GORM, SQL, object-storage SDKs, or another module's persistence types.
- No Catalog integration event is emitted until the outbox exists; later publication must write its event through the transactional outbox rather than requiring Kafka in the request transaction.

## Technical solution

### Domain and application

1. Model `Product`, `Variant`, `Category`, and `MediaReference` as Catalog-owned types with constructors/restorers that enforce normalization, bounds, lifecycle, relationship, and version invariants.
2. Treat product editing as one aggregate replacement command so the product, variants, assignments, and media validate before one transaction commits. Existing child IDs are updated, new child IDs are generated server-side, and omitted active children are archived rather than deleted.
3. Keep categories as separate aggregates. Resolve and lock the affected ancestry during reparenting; reject cycles, cross-store parents, excessive hierarchy depth, and unsafe archive.
4. Define consumer-owned application repository interfaces whose every method takes `store_id`. Keep opaque cursor encoding/decoding at the transport/application boundary and cap page size.
5. Reserve a future Pricing availability interface at the Catalog application boundary only when publication is implemented; do not add a placeholder dependency or a production path that can publish now.

### Persistence and migration

1. Add migration `00003_catalog_authoring.sql` with composite tenant primary/foreign keys, normalized scoped unique constraints, checks for statuses and measurement bounds, version columns, UTC timestamps, and query indexes matching the admin list and hierarchy paths.
2. Use module-local GORM row types over the existing bounded shared pool. Do not use `AutoMigrate`, associations, cross-module table access, or an exported raw database handle.
3. Persist a product aggregate in one short GORM transaction. Use explicit guarded SQL/GORM predicates and row counts for versioned updates and archive operations.
4. Retain rows on archive. Use restrictive tenant composite foreign keys for relationships and prove the database rejects crafted cross-store assignments even if application validation is bypassed.
5. Use an expand-compatible release sequence: deploy this binary while schema 2 is still active, where it accepts schema 2 but does not register Catalog routes; apply migration 3; then restart/roll the same binary so Catalog routes register. After migration 3, do not roll back to a pre-Task-008 binary whose exact schema check rejects version 3; keep the compatible binary deployed and use a corrected forward migration/release for recovery.

### HTTP, authorization, and composition

1. Add `catalog.manage` to the fixed role policy for owner and admin only; staff, customers, and guests deny by default in this task.
2. Register the admin Catalog routes after direct-host store resolution, user authentication, current membership resolution, and authorization, preserving existing timeout, request ID, logging, recovery, and problem behavior.
3. Decode strict JSON with body/collection limits, reject unknown fields, parse identifiers/cursors before use-case invocation, and never echo dependency or constraint details.
4. Update OpenAPI with exact schemas, limits, conflict responses, security requirements, opaque cursors, and the explicit absence of public Catalog routes.

## Compatibility and migration effects

- Existing API behavior and schema-version-2 data remain compatible. Migration 3 adds only Catalog-owned objects.
- The Task 008 binary accepts schema versions 2 and 3. At version 2 it deliberately omits Catalog routes; at version 3 it registers them. Other versions fail startup.
- `/api/v1/admin/catalog` is a new public contract namespace for authenticated workforce administration. Approving this plan approves that namespace and the aggregate-replacement semantics.
- The schema reserves `published` for forward compatibility, but Task 008 provides no route or repository command that can set it. Pricing must later add the successful publication transition and public read contract atomically with its price-readiness rule.
- Grams and millimetres are the proposed durable base units. Changing them after deployment would require a forward data migration, so plan approval is the decision gate for these units.
- Deployment order is Task 008 binary on schema 2, migration 3, then a rolling restart of the Task 008 binary. A pre-Task-008 binary is not a valid rollback after migration 3; recovery is a forward fix while retaining this compatible binary.
- No new runtime dependency is expected.

## Ordered implementation plan

1. Add Catalog IDs and domain aggregates, normalization, lifecycle, hierarchy, measurement, option, and media validation with focused unit tests.
2. Add migration 3 and schema inspection/fresh/upgrade/repeat tests for every tenant key, constraint, index, and cross-store relationship.
3. Implement tenant-scoped GORM repositories, aggregate transactions, optimistic guards, keyset listing, and real PostgreSQL isolation/concurrency tests.
4. Implement Catalog application use cases and owner/admin permission mapping with denial tests for staff, customers, guests, stale memberships, and suspended stores.
5. Add strict Gin admin handlers, route composition, problem mapping, cursor behavior, and API-level cross-store tests.
6. Update OpenAPI and current development documentation to distinguish authoring complete from publication/pricing pending.
7. Run focused formatting, lint, unit, migration, PostgreSQL, API-contract, tenant-isolation, and concurrency checks; then stop for independent reviews in the automatic workflow.

## Verification strategy

| Requirement or risk | Required evidence |
| --- | --- |
| Domain lifecycle | Unit tests for draft creation, valid edits, archive, invalid transition, retained identifiers, and inability to enter `published` |
| Tenant isolation | API and real-PostgreSQL tests where Store A uses Store B product, variant, category, media, slug, SKU, barcode, cursor, or relationship identifiers and observes no data/state leakage |
| Scoped uniqueness | PostgreSQL tests proving same-store collisions fail, archived values remain reserved, and identical slug/SKU/barcode values coexist across two stores |
| Aggregate atomicity | Failure-injection and PostgreSQL tests proving invalid/stale child, relationship, media, or version changes roll back the complete product edit |
| Optimistic concurrency | Synchronized PostgreSQL tests proving two edits/archive races have one winner and no mixed child state |
| Category hierarchy | Unit/PostgreSQL tests for same-store ancestry, cycle/depth rejection, concurrent reparent/archive, and active child/assignment protection |
| Typed physical data | Boundary/domain/schema tests for JSON shape and bounds, normalization collisions, integer overflow, negative/incomplete measurements, and media object-key traversal |
| Authorization | Handler/API tests for owner/admin success and staff/customer/guest/cross-store/inactive/suspended denial with correct middleware order |
| Pagination | Tests for stable descending keysets, page boundaries, inserts between pages, invalid/tampered cursors, limit bounds, and no offset contract |
| Migration safety | Fresh schema, populated version-2 upgrade, schema inspection, repeat no-op, cross-store FK rejection, and previous-binary compatibility reasoning |
| Contract and boundaries | OpenAPI coverage plus static/import tests proving Catalog domain/application contain no Gin/GORM/SQL or foreign persistence imports |

Development checks: `gofmt`/`make fmt-check`, `make lint`, focused `go test` commands for Catalog, Stores authorization, composition, and OpenAPI, focused integration tests with real PostgreSQL, and `git diff --check`.

Final verification after implementation and independent reviews: `make verify`. Vulnerability scanning remains part of that aggregate command even though no new dependency is planned.

## Acceptance criteria

- [x] Given an authorized owner or admin, when a valid draft product with variants, categories, and media is created or replaced, then the complete tenant-owned aggregate commits atomically and returns normalized data with an incremented version.
- [x] Given a stale version or invalid child/relationship, when an aggregate edit or archive is attempted, then a stable conflict/validation problem is returned and no partial Catalog state changes.
- [x] Given Store A and Store B, when they use overlapping product/category slugs, SKUs, or barcodes, then both succeed independently; same-store duplicates including archived rows fail.
- [x] Given any Store B Catalog identifier or cursor, when used through Store A host and credentials, then it behaves as absent/invalid and cannot leak or mutate Store B data.
- [x] Given a category hierarchy, when a caller creates a cycle, crosses stores, exceeds the depth bound, or archives a category still in use, then the operation fails without partial state.
- [x] Given concurrent edits or edit/archive races, when executed against real PostgreSQL, then optimistic guards produce one coherent winner and one conflict.
- [x] Given staff, customer, guest, inactive membership, suspended store, or unauthenticated access, when an admin Catalog route is requested, then access is denied before Catalog mutation.
- [x] Given a valid keyset cursor, when products are paged, then results are stable and bounded without duplicates from the existing snapshot order; malformed cursors return 400.
- [x] Given Task 008 artifacts, when inspected or exercised, then no route or application command can publish a product and no public Catalog read exists before Pricing enforces current-price readiness.
- [x] Given migration 3 and the updated API contract, when required verification runs, then fresh/upgrade/repeat migrations, focused tests, OpenAPI checks, lint, and final `make verify` pass.

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
- Last request, completed stage, pending gates, and next suggested command: `continue`; Task 008 completed with no pending gate; define the next roadmap task when ready.
- Plan revision and approval: revision 1 prepared and approved on 2026-09-10. Revision 2 changes deployment from the inaccurate migration-first/previous-binary rollback claim to the expand-compatible binary-first, migration, restart, forward-fix strategy described above; revision 2 approved by the user on 2026-09-10.
- Plan: Catalog authoring foundation as specified above, including migration 3, admin APIs, owner/admin authorization, aggregate transactions, and delayed publication until Pricing exists.
- Verification: acceptance criteria map to unit, API, OpenAPI, migration, tenant-isolation, atomicity, and synchronized PostgreSQL concurrency evidence above; final command is `make verify`.
- Architect review: final revision-2 re-review passed with no findings after rollout approval, complete schema-definition evidence, and database-lock-synchronized concurrency tests.
- Security review: final re-review passed with no findings after independent foreign-child denial and unchanged-state assertions; future media dereferencing must use server-derived tenant namespaces and cross-store object-access tests.
- Final evidence: on 2026-09-10, focused Catalog/unit/API/OpenAPI tests passed; guarded PostgreSQL tests passed for tenant isolation, atomicity, hierarchy, migration 2-to-3 upgrade/repeat/shape, identifier retention, and lock-synchronized concurrency, including three consecutive migration/repository runs; `make fmt-check`, `make lint` with zero issues, and `git diff --check` passed. After both independent reviews passed, final `make verify` passed formatting, zero-issue lint, all unit and integration tests at schema version 3, and vulnerability scanning with zero vulnerabilities in called code or imported packages. Initial Git status was unavailable without a per-command safe-directory override because the sandbox user differs from the repository owner; the overridden read-only status reported no file entries before this task record was created.
