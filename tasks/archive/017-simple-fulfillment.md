# Task: Deliver tenant-safe simple fulfillment

## Context

This is roadmap slice 12 and builds on [checkout and orders](013-checkout-orders.md), [cancellation and full refunds](016-cancellation-full-refunds.md), and the shipping-rule foundation already delivered with checkout. The target behavior is defined by the [product requirements](../../docs/product-requirements.md), [architecture](../../docs/architecture.md), [domain model](../../docs/domain-model.md), [database design](../../docs/database-design.md), [event catalog](../../docs/event-catalog.md), and [testing strategy](../../docs/testing-strategy.md).

Task 015 remains separately active only for externally blocked live MoMo sandbox UAT. Its reviewed automated payment behavior and the accepted task-16 cancellation/refund behavior are the fulfillment baseline; task 17 does not claim or waive the missing live-provider evidence.

## Outcome

- Authorized workforce can find paid, processing, and shipped orders in a tenant-scoped fulfillment queue and inspect one order without using shopper credentials.
- Authorized workforce can create the order's single shipment, manually record and correct bounded carrier/tracking identifiers before dispatch, mark it shipped, and mark it completed.
- Shipment creation advances a paid order to processing; dispatch atomically advances the shipment and order to shipped; completion atomically advances both to completed.
- Registered customers and guests see the current shipment state, carrier, tracking number, and fulfillment timestamps through their existing protected order read after a shipment exists.
- Duplicate, concurrent, stale-version, permission-revocation, cancellation/refund, and cross-store operations produce one valid durable outcome without duplicate shipment, history, audit, or outbox effects.

## In scope

- Add a Shipping-owned one-shipment-per-order aggregate with `processing`, `shipped`, and `completed` lifecycle, carrier/tracking data, optimistic version, and UTC timestamps.
- Add `order.fulfill`. Owner/admin receive it by role; staff receive it only through an explicit owner-managed grant.
- Add tenant-scoped admin fulfillment queue/detail reads for paid, processing, and shipped orders with bounded keyset pagination and optional state filtering.
- Add idempotent shipment creation, pre-dispatch carrier/tracking correction, dispatch, and completion use cases and `/api/v1` admin routes.
- Extend protected shopper order reads with the current shipment projection when present.
- Coordinate Shipping-owned persistence with Orders-owned transitions, state history, Audit, and Eventing in one PostgreSQL transaction using explicit module interfaces.
- Add migration 11, OpenAPI updates, schema/domain/event/status documentation, and focused operational notes.

## Out of scope

- Partial or split fulfillment, multiple shipments per order, item-level packing, backorders, warehouse/location selection, transfers, returns, exchanges, or proof-of-delivery documents.
- Carrier rate shopping, label purchase/printing, carrier APIs, callback/webhook tracking, geolocation, delivery estimates, or automated delivery confirmation.
- Customer-selected shipping changes after checkout, editing the immutable delivery/shipping snapshots, customer shipment mutation, or public tracking-number lookup.
- Notifications or Kafka publication workers; this task writes transactional outbox facts only.
- Production migration execution, deployment, or live carrier integration.

## Invariants and security constraints

- Store scope comes only from validated host and authenticated context. Every order, shipment, command key, membership, permission, audit record, query, lock, and event includes `store_id`.
- Fulfillment reads and writes require a current active workforce membership with `order.fulfill`; mutation authorization and active-store state are revalidated inside the same transaction as the change.
- Owner/admin receive `order.fulfill` by fixed role. Staff are denied by default and receive only a versioned explicit grant managed by an owner through the existing membership-permissions surface.
- One order has at most one shipment. Shipment creation is allowed only for an order with a succeeded payment in `paid`; it atomically creates the shipment and transitions the order to `processing`.
- Carrier and tracking values are normalized, non-empty, bounded identifiers. Their values come from the command, but shipment/order/store identity, lifecycle, actor, and timestamps come from authenticated context and persisted state.
- Carrier/tracking correction is allowed only while the shipment/order are `processing`; dispatched or completed shipment identity is immutable.
- Dispatch requires matching `processing` order and shipment states and atomically changes both to `shipped`. Completion requires matching `shipped` states and atomically changes both to `completed` with database time.
- Fulfillment locks the order before the shipment and rechecks both states and expected versions. This preserves the existing order-first global lock policy so cancellation/refund and fulfillment races have one winner.
- Every mutation binds a store-scoped idempotency key to operation, normalized request fingerprint, shipment, and durable result. Exact replay returns the recorded result; changed input conflicts.
- Privileged mutations append required order history, audit records, and outbox facts in the same transaction. A subordinate write failure rolls back every effect.
- Shipment data never grants order access. Shopper reads still require the owning user session or exact store-bound guest grant; admin reads require workforce authorization. Delivery/contact snapshots do not enter shipment events or logs.
- Tracking numbers are not secrets, but they are excluded from general event payloads and unnecessary logs. Timestamps use database UTC and APIs expose RFC 3339.

## Proposed HTTP contract

- `GET /api/v1/admin/orders?state=paid|processing|shipped&limit=&cursor=` requires `order.fulfill` and returns a keyset-paginated tenant fulfillment queue. Omitting `state` returns only those three actionable states.
- `GET /api/v1/admin/orders/{order_id}` requires `order.fulfill` and returns tenant-scoped order detail plus its shipment when present.
- `POST /api/v1/admin/orders/{order_id}/shipments` requires `order.fulfill` plus `Idempotency-Key`; it accepts carrier and tracking number, creates the single `processing` shipment, and transitions a paid order to processing.
- `PUT /api/v1/admin/shipments/{shipment_id}` requires `order.fulfill` plus `Idempotency-Key`; it accepts expected version, carrier, and tracking number and replaces those identifiers only before dispatch.
- `POST /api/v1/admin/shipments/{shipment_id}/dispatches` requires `order.fulfill` plus `Idempotency-Key` and expected version; it atomically marks shipment and order shipped.
- `POST /api/v1/admin/shipments/{shipment_id}/completions` requires `order.fulfill` plus `Idempotency-Key` and expected version; it atomically marks shipment and order completed.
- `GET /api/v1/orders/{order_id}` preserves its current user/guest authorization and response fields and adds nullable `shipment` detail.
- Validation uses 400, authentication 401, authorization 403, scoped absence 404, replay/version conflict 409, invalid lifecycle 422, and retryable dependency failure 503. Admin and shopper responses containing order/shipment detail use `Cache-Control: no-store`.

## Technical solution

1. Add a Shipping shipment aggregate with normalized carrier/tracking value rules, lifecycle policy, optimistic version, and database-time transition timestamps. Keep Orders' central policy authoritative for order transitions.
2. Add `order.fulfill` to fixed-role authorization and explicit staff grants, including transaction-time workforce and active-store revalidation for every mutation.
3. Add migration 11 for tenant-owned `shipments` and `shipment_commands`, one-shipment-per-order and tenant foreign keys, lifecycle/timestamp/version checks, store-scoped carrier/tracking and command uniqueness, queue/lookup indexes, and expanded permission/audit constraints.
4. Define explicit Orders application interfaces for tenant-scoped fulfillment queue/detail reads and locked `paid -> processing -> shipped -> completed` transitions. Shipping coordinates its own shipment rows with Orders history, Audit, and Eventing without querying or owning Orders rules outside those interfaces.
5. Implement idempotent create, correction, dispatch, and completion transactions. Lock the order before shipment, use database time, revalidate authorization and lifecycle, persist the command result, and append audit/history/outbox facts atomically.
6. Add bounded keyset admin reads and attach the current Shipping projection to admin and existing shopper order responses through application interfaces rather than cross-module table access.
7. Add Gin routes and problem mapping using existing tenant host, workforce/shopper authentication, request timeout, request ID, idempotency, no-store, and deny-by-default patterns; update OpenAPI.
8. Update schema/domain/event/status documentation for shipment ownership, transitions, permission, API behavior, and events `order.shipped.v1` and `order.completed.v1`.

## Migration and compatibility

- Deploy migration 11 before binaries that expose fulfillment routes or shipment response fields. Gate those routes on schema version 11; schema-10 binaries continue serving existing checkout/payment/refund behavior during rollout.
- The migration is additive for shipment tables and indexes, while permission and audit check constraints expand through add-not-valid, validate, swap steps to reduce lock duration. No existing order, payment, refund, inventory, or shipping snapshot data is rewritten.
- Existing `paid` and `processing` orders remain valid. A paid order can receive its first shipment after deployment; a legacy processing order without a shipment is not guessed or backfilled and remains unavailable for mutation until inspected and forward-fixed explicitly.
- Measure validation/index work against a production-sized copy. `shipments` and `shipment_commands` begin empty, so their initial indexes require no data backfill; expanded checks still receive lock-sensitive migration coverage.
- If migration application fails, keep schema-10 binaries running and forward-fix/retry migration 11 before deploying fulfillment code. Retained order, shipment, history, audit, command, and outbox records are not down-migrated in production.
- No new runtime dependency is planned; existing Gin, GORM, PostgreSQL, Audit, Eventing, tenant, authorization, and keyset patterns cover this slice.

## Ordered implementation plan

1. Add shipment IDs, domain models, normalization, lifecycle rules, `order.fulfill`, and focused unit tests for states, bounds, role defaults, and explicit staff grants.
2. Add migration 11 and PostgreSQL models/repositories for shipments and command idempotency, expanded permission/audit constraints, tenant relationships, uniqueness, and query indexes; cover empty/populated schema-10 upgrade and repeat application.
3. Extend Orders with fulfillment queue/detail projections and locked transition interfaces while preserving one central state machine and the order-first global lock policy.
4. Implement Shipping create/correct/dispatch/complete transactions with authorization revalidation, request fingerprints, optimistic versions, database timestamps, history, audit, events, and failure-injection rollback tests.
5. Add admin queue/detail and shipment mutation Gin routes, extend shopper order responses, and update OpenAPI schemas, problems, headers, and schema-version wiring.
6. Add cross-store denial, permission revocation, duplicate/replay conflict, stale version, cancellation-versus-dispatch, duplicate dispatch/completion, and rollback integration tests against real PostgreSQL.
7. Update architecture, domain, database, event, development-status, and operating documentation, then run formatting, lint, focused unit/API tests, migration and transaction integration tests, and `make verify` because this is a cross-module release slice.

## Acceptance criteria

- [x] Given a paid tenant order, when authorized workforce creates a shipment with an idempotency key, then one processing shipment is created and the order transitions to processing with matching history, audit, and outbox facts.
- [x] Given a processing shipment, when authorized workforce corrects carrier/tracking before dispatch, then normalized identifiers and version update atomically; stale versions, post-dispatch edits, and changed idempotent replays fail without mutation.
- [x] Given matching processing order/shipment state, when authorized workforce dispatches it, then both become shipped at one database timestamp and `order.shipped.v1`, history, and audit commit exactly once.
- [x] Given matching shipped order/shipment state, when authorized workforce completes it, then both become completed with terminal timestamps and `order.completed.v1`, history, and audit commit exactly once.
- [x] Given a paid, processing, or shipped order, when authorized workforce lists or reads fulfillment work, then only the validated store's bounded keyset results are returned; inactive, revoked, unauthorized, and foreign-store actors are denied.
- [x] Given an owning user or exact guest grant, when the protected order is read after shipment creation, then the shipment projection is visible; another user, guest, store, public order number, or tracking number cannot resolve it.
- [x] Given duplicate, concurrent, stale, or cancellation/refund-versus-fulfillment commands, when they race, then the order-first lock policy yields one valid lifecycle with no duplicate shipment, history, audit, command result, or event.
- [x] Given a required shipment, order, history, audit, idempotency, or outbox write failure, when a mutation runs, then all state rolls back.
- [x] Migration 11, OpenAPI, schema/domain/event/status docs, both independent reviews, and applicable final verification pass.

## Verification strategy

| Requirement or risk | Required evidence |
| --- | --- |
| Shipment and order lifecycle | Unit/application tests for normalization, allowed transitions, one-shipment policy, expected versions, terminal behavior, and immutable checkout snapshots |
| Authorization and tenant isolation | Role/grant/revocation tests plus API and PostgreSQL cross-store denial for orders, shipments, command keys, carrier/tracking identifiers, histories, audits, and events |
| Idempotency | Exact replay, changed-request conflict, duplicate creation, correction, dispatch, and completion tests with one durable result |
| Atomicity and lock order | Real PostgreSQL failure injection and synchronized cancellation/refund-versus-create/dispatch tests proving order, shipment, history, audit, command, and outbox commit or roll back together |
| Reads and privacy | Admin keyset/state filtering, shopper user/guest ownership, nullable shipment projection, no-store headers, and absence of delivery/contact data from events/logs |
| Migration and compatibility | Empty and populated schema-10 upgrade, repeat migration, constraint/index checks, schema gate tests, and existing checkout/payment/refund regressions |
| Final checks | `gofmt` on changed Go files, `make lint`, focused `go test` commands, `make test-integration`, and `make verify` |

## Required completion report

- Behavior and design summary
- Files/modules changed
- Commands executed with results
- Risks, assumptions, and follow-ups

## Delivery record

- Stage: complete
- Active task: no
- Last command, completed stage, pending gates, and next suggested command: `make verify` passed on 2026-09-20 after both independent reviews passed; no Task 17 gates remain.
- Plan revision and approval: revision 1 was approved by the user on 2026-09-19 (`approve`).
- Plan: add a tenant-safe one-shipment fulfillment aggregate, explicit workforce permission, admin fulfillment queue/detail, shopper shipment projection, atomic order/shipment transitions, migration 11, contracts, and documentation as detailed above.
- Verification: acceptance criteria map to focused domain/API tests, cross-store and authorization denial, idempotency and rollback evidence, real PostgreSQL migration/concurrency tests, regression coverage, lint, and broad verification.
- Architect review: final re-review passed with no findings after actionable-state read locking, keyset pagination, synchronized read/write, and command-result rollback evidence were added.
- Security review: final re-review passed with no exploitable security or tenant-isolation findings after inspecting the read-locking delta and rollback evidence.
- Final evidence: focused package/API and real-PostgreSQL fulfillment/migration tests passed; `make test-integration` passed after the final fixes; `make verify` passed zero-issue lint, all unit tests, all real integration tests at schema 11, and vulnerability scanning with no vulnerabilities in called or imported code.
