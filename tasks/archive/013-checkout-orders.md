# Task: Deliver atomic checkout and immutable orders

## Context

This is roadmap slice 8 and builds on [Customers and carts](012-customers-and-carts.md), [VND pricing](009-vnd-pricing-catalog-publication.md), and [single-location Inventory](011-single-location-inventory.md). The target behavior is defined by [product requirements](../../docs/product-requirements.md), [architecture](../../docs/architecture.md), [domain model](../../docs/domain-model.md), [database design](../../docs/database-design.md), [event catalog](../../docs/event-catalog.md), and [testing strategy](../../docs/testing-strategy.md).

## Outcome

- A registered customer or guest can convert their active cart into one tenant-owned `pending_payment` order without overselling inventory.
- Checkout recalculates sellability, VND prices, shipping, and totals inside the authoritative transaction and persists immutable line, contact, address, tax-presentation, and shipping snapshots.
- A store-scoped idempotency key returns the original checkout result for an identical retry and rejects a changed request.
- A guest receives a one-time plaintext order grant while only its digest is stored; the order number alone never authorizes access.
- Pending orders expire after the ten-minute reservation window, with the order, minimal payment record, reservations, history, and outbox facts changed atomically.
- Kafka and payment providers are not contacted during checkout.

## In scope

- Add cohesive Orders domain, application, PostgreSQL, and Gin transport packages.
- Add the minimal Shipping quote rule needed by checkout: one current tenant-owned free or flat VND rule with owner/admin read and optimistic replacement endpoints.
- Add a minimal Payments transaction participant that records the selected `vietqr` or `momo` provider, payable amount, and `pending`/`expired` lifecycle; provider configuration and execution remain later tasks.
- Add `POST /api/v1/checkout`, registered-customer and guest order reads, and tenant-scoped order authorization.
- Add migration 7 for orders, snapshots, grants, checkout idempotency, state history, shipping rules, minimal payments, and per-store order-number allocation.
- Convert Cart, Catalog, Pricing, Customers, Inventory, Payments, Shipping, and Eventing participation into explicit transaction-aware infrastructure interfaces without allowing Orders to query their tables directly.
- Replace reservation-only expiry with order-first coordinated expiry and preserve bounded multi-worker behavior.
- Emit `order.created.v1`, `inventory.reserved.v1`, `order.expired.v1`, and `inventory.reservation_expired.v1` through the transactional outbox.
- Update OpenAPI, development status, schema/domain documentation where implementation details become concrete, API/worker composition, and focused operating notes.

## Out of scope

- VietQR QR generation or confirmation, MoMo API calls or webhooks, encrypted provider configuration, reconciliation, refunds, and external credentials.
- Customer, guest, or workforce cancellation; paid, processing, shipped, completed, cancellation-requested, refund-pending, and cancelled use cases beyond defining the central state machine.
- Shipment records, carrier/tracking entry, fulfillment transitions, email/Kafka publication workers, and consumer inboxes.
- Coupons, discounts, multiple shipping services, carrier quotes, tax calculation, multiple currencies, backorders, or partial orders.
- Deployment, production migration execution, or cleanup of historical opaque reservation references.

## Proposed HTTP contract

- `GET /api/v1/admin/shipping/rule` reads the current rule for an owner/admin.
- `PUT /api/v1/admin/shipping/rule` creates or replaces the current rule using `expected_version`, `mode` (`free` or `flat`), and a non-negative VND flat amount.
- `POST /api/v1/checkout` requires a user or guest session, an `Idempotency-Key` header, `cart_version`, and `payment_provider` (`vietqr` or `momo`). A registered user supplies an owned active `address_id`; a guest supplies validated contact and Vietnamese delivery-address fields inline.
- Checkout returns `201` with the order, selected pending payment, reservation expiry, and a plaintext `order_access_token` only on the first successful guest response. An identical replay returns `200` with the same order and no reissued grant; callers must retain the original guest grant.
- `GET /api/v1/orders/{order_id}` returns an owned registered-customer order under the user session or one guest order under its order-grant bearer credential. A guest session, public order number, contact detail, foreign-store credential, or foreign-user session is insufficient.
- Validation uses 400, authentication uses 401, owned-resource absence and cross-scope probes use the same 404, stale cart or idempotency conflicts use 409, unavailable sellability/price/stock/shipping uses 422, and dependency failures use 503.

## Invariants and security constraints

- Store scope comes only from validated host and authenticated context; every aggregate, lookup, lock, constraint, event, and idempotency key includes `store_id`.
- Checkout owns one PostgreSQL transaction. No provider, Kafka, Redis, or other external call occurs before commit.
- The cart row is locked and rechecked for owner, active state, version, non-empty lines, and deadline. Successful conversion records its order relationship exactly once.
- Catalog snapshots come only from currently published products and active variants; Pricing resolves current authoritative VND prices; checked integer arithmetic rejects overflow.
- Shipping uses the current active rule and snapshots its mode, amount, and version. Later rule changes cannot affect an order.
- Inventory locks variants in stable order and creates ten-minute reservations in the checkout transaction. A shortage rolls back the order, payment, cart conversion, history, grant, idempotency, and outbox rows.
- Order totals satisfy `grand_total = subtotal + shipping_total`, all components are non-negative integer VND minor units, and the persisted snapshot is immutable.
- A checkout key is bounded, stored only with its request fingerprint and result, unique by store, and cannot replay across tenants or with a changed cart/provider/address request.
- Guest grants contain at least 256 random bits, are returned only at initial creation, are stored as SHA-256 digests, are bound to store and order, expire after 30 days, and are never logged.
- Order numbers are communication references allocated monotonically per store and formatted `ORD-` plus a zero-padded decimal sequence; uniqueness is `(store_id, order_number)` and no authorization decision uses the number.
- Expiry claims the order first, then the payment, reservations ordered by reservation ID, and stock items ordered by variant ID, rechecking database time and state. Expiry releases reservations without changing `on_hand` and is idempotent under duplicate workers.
- State changes go through the Orders state machine and optimistic version checks; state history and required outbox failure roll back the transition.

## Technical solution

### Domain and application boundaries

1. Add Orders money-safe snapshots, owner references, lifecycle transitions, order views, checkout commands, request fingerprinting, and guest-grant generation/digestion.
2. Make Orders the checkout and expiry coordinator. Define consumer-side ports for cart claim/conversion, catalog snapshots, price resolution, customer address/profile snapshots, shipping quote, inventory reservations, minimal payment creation/expiry, and outbox append.
3. Keep GORM transaction handles at infrastructure composition boundaries. Each participating module implements a narrow transaction-aware writer/reader for its own tables; Orders never imports another module's row models or SQL.
4. Add purpose-limited user address/profile resolution and guest inline validation, returning the same not-found outcome for archived, foreign-store, and foreign-user records.

### Persistence and concurrency

1. Migration 7 creates tenant-composite keys, checks, immutable snapshot tables, owner/history indexes, one-current-shipping-rule enforcement, per-store order-number counters, minimal payments, and checkout idempotency.
2. Keep Inventory reservation order references opaque at the schema boundary in this slice. PostgreSQL enforces `NOT VALID` foreign keys on new rows, so adding one would break the existing standalone Inventory contract; the Orders coordinator establishes the required relationship atomically and a later controlled contract migration can add the cross-module key after all callers require persisted orders.
3. Serialize one cart checkout and one store/key replay, then lock catalog/price/shipping/inventory inputs in stable order. Persist the idempotency row only in the same transaction as the completed result.
4. Update Inventory reservation creation/expiry to accept the caller transaction and keep its events module-owned. Replace the worker's reservation-only expiry loop with the Orders coordinator.
5. Use database time for reservation/order deadlines, state changes, and order-number allocation; use guarded constraints plus the domain rules for total and lifecycle consistency.

### HTTP and compatibility

1. Reuse host selection and shopper authentication. The checkout handler derives user/guest ownership from the authenticated principal and never accepts owner or store identifiers in the body.
2. Add explicit order-grant authentication for guest reads. Hash before lookup, scope by resolved store and requested order, compare without exposing grant existence, and set `Cache-Control: no-store` on responses containing a new grant.
3. Extend the OpenAPI contract with strict request/response schemas, the idempotency header, order-grant security, standard problems, and RFC 3339 UTC timestamps.
4. Existing cart/catalog/pricing/inventory/customer routes remain compatible. The worker changes from reservation-only expiry to the coordinated order workflow; no external provider behavior is introduced.

## Migration and recovery

- Deploy migration 7 before the revised API and worker. The migration is additive for existing tenant data and does not rewrite carts, prices, customers, or stock.
- New order tables are initially empty. Migration 7 takes an `ACCESS EXCLUSIVE` metadata lock to add nullable `carts.converted_order_id`, then adds and validates the composite cart-to-order foreign key, which scans the populated carts table while holding PostgreSQL's foreign-key DDL locks. The conversion consistency check is added `NOT VALID`, so it enforces new writes without scanning historical carts; the migration test asserts these validation states. Deploy with a bounded `lock_timeout` during a low-traffic window after estimating the carts scan, and forward-fix or retry before deploying schema-7 code if lock acquisition or validation times out.
- If migration application fails, keep the old API/worker on schema 6 and forward-fix the failed migration before deploying code. Do not down-migrate production data.
- Before production readiness, audit reservation callers and legacy references, then decide whether a cross-module foreign key can be added through a separate forward migration without breaking the Inventory contract.

## Ordered implementation plan

1. Add IDs, Orders/Shipping/minimal Payments domain models, validation, transitions, and focused unit tests.
2. Add migration 7 and migration tests for empty/populated schema, tenant keys, checks, indexes, immutable snapshots, clean-schema foreign-key validation, and legacy-reference compatibility.
3. Add transaction-aware participant adapters for Carts, Catalog, Pricing, Customers, Shipping, Inventory, Payments, and Eventing.
4. Implement atomic checkout, request fingerprinting/replay, per-store order-number allocation, guest grants, cart conversion, snapshots, reservations, payment record, history, and outbox writes.
5. Implement owned order reads and guest order-grant authorization with same-store success and cross-store/cross-owner denial.
6. Implement order-first pending-order expiry and replace worker composition, with duplicate-worker, confirmation-race-ready lock ordering, and rollback tests.
7. Add Gin routes, problem mappings, OpenAPI schemas/security, configuration where needed, development-status updates, and operational logging/metrics without personal data or credentials.
8. Run formatting, linting, focused unit/API tests, real PostgreSQL migration/transaction/isolation/concurrency tests, vulnerability scanning if security-sensitive grant handling warrants it, and full verification because checkout is a broad cross-module change.

## Verification strategy

| Requirement or risk | Required evidence |
| --- | --- |
| Authoritative checkout | Unit and PostgreSQL tests proving current catalog, price, shipping, and stock win over cart estimates and all totals use checked integer VND arithmetic |
| Atomicity | Failure injection at each participant proving no partial order, reservation, payment, conversion, history, grant, idempotency, or outbox state commits |
| Idempotency | Identical sequential/concurrent retry returns one order; changed payload conflicts; the same key and order number can coexist in another store |
| Inventory contention | Real PostgreSQL synchronized last-unit checkout test proving one winner and no negative availability |
| Snapshot history | Tests changing product, price, address, and shipping after checkout while the order response remains unchanged |
| Tenant and owner isolation | Same-store reads plus cross-store, cross-user, guest-session-only, wrong-order-grant, and public-order-number denial |
| Guest grant security | Randomness/digest-only persistence, one-time return, replay behavior, expiry, revocation-ready state, no-store response, and redacted logs |
| Expiry | Database-time boundary, duplicate-worker, idempotent release, order-first locking, rollback, outbox, and no `on_hand` mutation tests |
| Schema and contracts | Migration upgrade/repeat and constraint inspection; OpenAPI parse and route/response conformance |
| Final checks | `gofmt` on changed Go files, `make lint`, focused `go test` commands, `make test-integration`, `make vulncheck`, and `make verify` |

## Acceptance criteria

- [x] Given a valid user or guest cart, when checkout succeeds, then one `pending_payment` order, payment, reservation set, cart conversion, state history, idempotency result, and required outbox events commit together.
- [x] Given stale, unavailable, unpriced, overflowed, foreign-store, or insufficient-stock input, when checkout runs, then it fails closed without any partial effect.
- [x] Given the same store/key/fingerprint, when checkout is retried sequentially or concurrently, then the original order is returned and no business effect is duplicated; a changed fingerprint conflicts.
- [x] Given two stores with overlapping identifiers, keys, or order-number sequences, when they checkout and read orders, then their data and credentials never resolve across stores.
- [x] Given a guest checkout, when the initial response is returned, then a high-entropy grant is exposed once, only its digest is stored, and only that grant can read that guest order.
- [x] Given later catalog, price, address, or shipping changes, when the order is read, then every persisted snapshot and total remains unchanged.
- [x] Given concurrent last-unit checkouts, when both contend, then at most one succeeds and available stock never becomes negative.
- [x] Given an overdue pending order, when one or more workers process it, then order/payment/reservations/history/outbox transition once, `on_hand` is unchanged, and a retry is a no-op.
- [x] OpenAPI, development status, migration evidence, focused tests, both independent reviews, and applicable final verification pass.

## Required completion report

- Behavior and design summary
- Files/modules changed
- Commands executed with results
- Risks, assumptions, and follow-ups

## Delivery record

- Stage: complete
- Active task: no
- Last command, completed stage, pending gates, and next suggested command: implementation, review fixes, both independent re-reviews, and final verification completed on 2026-09-13. No delivery gate remains; the next roadmap slice is provider payment execution.
- Plan revision and approval: revision 1 recorded on 2026-09-12 and approved by the user on 2026-09-13 (`approve`).
- Plan: add atomic Orders coordination, immutable snapshots, tenant idempotency, minimal shipping/payment participants, guest grants, order-first expiry, migration 7, API/contracts, and risk-matched evidence as detailed above.
- Verification: acceptance criteria map to domain/API tests, real PostgreSQL migration/transaction/isolation/concurrency tests, OpenAPI parsing, lint, vulnerability scanning, and full verification.
- Implementation evidence: schema 7 empty/populated upgrades, all unit tests, full PostgreSQL integration suite, focused checkout/Shipping transaction tests, OpenAPI tests, and lint passed before review.
- Architect review: changes required on 2026-09-13. Blocking findings covered consistent order/payment reads, all-or-nothing expiry, and missing coordinator-level concurrency/rollback/schema evidence; medium/low findings covered owner event references, Shipping timeout mapping, migration locking notes, and worker terminology. Fixes add shared read locks, strict payment/reservation expiry preconditions, central transition validation, synchronized PostgreSQL and rollback coverage, owner references, 503 mapping, accurate migration operations, and coordinated-expiry logs. Independent architect re-review passed with every finding closed and no new findings.
- Security review: changes required on 2026-09-13. The shared blocker was partial expiry commit plus missing transaction, contention, grant, tenant-key, and migration evidence. Fixes and focused regression tests completed; security re-review passed.
- Final evidence: `make verify` passed on 2026-09-13; lint, unit tests, full integration tests, and vulnerability scan all passed. Architect and security re-reviews passed.
