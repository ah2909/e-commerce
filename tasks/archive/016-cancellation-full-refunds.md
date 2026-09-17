# Task: Deliver tenant-safe cancellation and full refunds

## Context

This is roadmap slice 11 and builds on [checkout and orders](013-checkout-orders.md), [VietQR payments](014-vietqr-payments.md), and [MoMo payments](../015-momo-payments.md). The target behavior is defined by the [product requirements](../../docs/product-requirements.md), [architecture](../../docs/architecture.md), [domain model](../../docs/domain-model.md), [database design](../../docs/database-design.md), [event catalog](../../docs/event-catalog.md), [testing strategy](../../docs/testing-strategy.md), and MoMo's official [refund contract](https://developers.momo.vn/v3/docs/payment/api/payment-api/refund/).

Task 015 remains separately active only for its externally blocked live-sandbox UAT. Its reviewed schema-9 implementation is the baseline for this task; task 16 does not claim or waive that missing provider acceptance evidence.

## Outcome

- A registered customer or guest can idempotently cancel an accessible unpaid order before payment, releasing active reservations once without changing physical stock.
- An owner, administrator, or explicitly permitted staff member can cancel an order before shipment. Unpaid cancellation completes atomically; paid cancellation creates one full-refund workflow and does not claim financial reversal before settlement.
- A MoMo full refund uses one bounded signed provider request with stable request identities, durable retry classification, and query-before-retry for ambiguous outcomes.
- An authorized actor can record one externally settled full VietQR refund with its external reference and settlement time.
- Successful refunds atomically mark the refund and payment refunded, restock the originally consumed quantities with immutable inventory ledgers, cancel the order, record audit/history, and append outbox facts exactly once.
- Duplicate, concurrent, cross-store, late-provider, and cancellation-versus-payment races produce one valid durable outcome without duplicate refunds, reservation releases, restocks, audit records, or events.

## In scope

- Add shopper unpaid-cancellation and workforce pre-shipment cancellation use cases and `/api/v1` routes.
- Add `order.cancel` and `payment.refund` permissions. Owner/admin receive both by role; staff receive only explicit owner-managed grants.
- Add the Payments-owned full-refund aggregate, idempotent request/execution state, MoMo provider references/results, and audited VietQR manual settlement.
- Add a bounded signed MoMo refund/query adapter using the existing encrypted credential snapshots and deployment-owned endpoint/timeout settings.
- Add Inventory-owned reservation release for unpaid cancellation and idempotent restock/ledger behavior after a successful paid refund.
- Coordinate Orders, Payments, Inventory, Audit, and Eventing through explicit application interfaces and the established global lock order.
- Add migration 10, OpenAPI and configuration documentation, development status, event/schema/domain updates, and focused operational notes.

## Out of scope

- Partial cancellation, partial refund, exchanges, store credit, return-merchandise workflows, cancellation after shipment, or automated refund scheduling workers.
- Reconciliation resolution, manual mutation of MoMo results, chargebacks, disputes, cash-on-delivery, or additional payment providers.
- Shipping/fulfillment implementation, automatic carrier integration, customer self-service cancellation of paid orders, or inventory-location selection.
- Production provider credentials, deployment, production migrations, or live MoMo refund execution.

## Invariants and security constraints

- Store scope comes only from validated host and authenticated context. Every order, payment, refund, settlement, reservation, stock, ledger, permission, audit, idempotency key, query, lock, and event includes `store_id`.
- Shopper cancellation requires the order-owning user session or the exact store-bound guest order grant and is limited to `pending_payment`. Workforce cancellation requires a current active membership and `order.cancel`; refund execution or settlement additionally requires `payment.refund`.
- Permission and store activity are revalidated in the same transaction as every privileged cancellation/refund mutation so suspension or permission revocation cannot race admission.
- Cancellation reason is normalized and bounded. Actor, tenant, order/payment identity, refund amount, provider, and financial state come from authenticated context and persisted records, never request-selected scope or amount.
- Only full refunds are created. One order/payment cannot have more than one active or successful full refund, and successful refunds cannot exceed the captured VND amount.
- Unpaid cancellation uses the established lock order of order, payment, reservations by reservation ID, then stock by variant ID; it releases active reservations once, expires the pending payment/attempts, cancels the order, and writes history/audit/outbox facts atomically without changing `on_hand`.
- Paid cancellation is allowed only before shipment. It atomically moves the order into the refund workflow, creates one refund with stable idempotency/provider identities, records audit/history, and appends request facts before any provider call.
- External MoMo calls occur without an open database transaction, use the existing 35-second bounded client and deployment-owned base URL, enforce response-size/JSON bounds, and exclude credentials, signatures, descriptions, and provider responses from logs, events, and problem details.
- MoMo refund signatures use the exact documented HMAC-SHA256 field order. The original successful provider transaction ID, full amount, stored credential snapshot, stable refund `orderId`, and stable `requestId` are persisted and bound before execution.
- An ambiguous MoMo timeout or response leaves the refund durably retryable with the same provider identities. Resume queries the refund result first and only reissues the same idempotent request when the query establishes that doing so is safe.
- VietQR settlement requires `payment.refund`, an idempotency key, a bounded external reference, and an RFC 3339 settlement time. Replays return the recorded outcome; changed observations conflict.
- Successful refund application locks the order and payment before affected stock rows ordered by variant ID, rechecks all states, increments `on_hand` by the immutable order quantities, appends equal refund-restock ledgers, and commits refund success, payment refunded, order cancelled, audit/history, and outbox facts together.
- Permanent or retryable provider failures never mark the payment refunded, cancel the order terminally, or restock inventory. Duplicate or concurrent shopper/workforce cancellation, payment confirmation, refund execution, VietQR settlement, and inventory adjustment have one valid outcome.

## Proposed HTTP contract

- `POST /api/v1/orders/{order_id}/cancellations` requires the order-owning user session or guest order grant plus `Idempotency-Key`; it accepts a bounded reason and cancels only an unpaid order.
- `POST /api/v1/admin/orders/{order_id}/cancellations` requires `order.cancel` plus `Idempotency-Key`; it accepts a bounded reason, completes unpaid cancellation, or creates/resumes a full-refund workflow for a paid or processing order before shipment.
- `GET /api/v1/admin/refunds/{refund_id}` requires `payment.refund` and returns tenant-scoped refund state with no credentials, signatures, or raw provider data.
- `POST /api/v1/admin/refunds/{refund_id}/momo-executions` requires `payment.refund` plus `Idempotency-Key`; it creates or resumes the bounded MoMo refund/query flow using the persisted full amount and provider identities.
- `POST /api/v1/admin/refunds/{refund_id}/vietqr-settlements` requires `payment.refund` plus `Idempotency-Key`; it records the externally observed full settlement reference and RFC 3339 settlement time.
- Validation uses 400, authentication 401, authorization 403, scoped absence 404, idempotency/version conflict 409, invalid lifecycle 422, and retryable or ambiguous provider dependency 503. Responses containing financial workflow details use `Cache-Control: no-store`.

## Technical solution

1. Extend Orders' central state policy with actor-specific cancellation eligibility while preserving `pending_payment -> cancelled`, paid/processing refund workflow, and terminal shipment boundaries.
2. Add `order.cancel` and `payment.refund` to fixed-role authorization and explicit staff grants, including transactional workforce revalidation for privileged writes.
3. Add Payments refund domain/application models with full-amount validation, states `pending`, `processing`, `succeeded`, `failed`, and `reconciliation_required`, stable request fingerprints, and provider-neutral results.
4. Add migration 10 for tenant-owned refunds and manual-settlement receipts, captured MoMo transaction identity, provider request/result metadata, uniqueness, state/amount checks, query indexes, new payment states, permissions, audit actions, and event-safe foreign keys.
5. Implement an Orders-led cancellation coordinator. It uses module-owned transaction participants to lock and recheck eligibility, close pending payment/attempts, release unpaid reservations, or create a full refund and order history/outbox facts atomically.
6. Extend the Inventory participant with refund restock based on immutable order-line variant/quantity snapshots, ordered stock locks, guarded updates, immutable ledger entries, and rollback-safe events.
7. Extend the MoMo adapter with official full-refund and refund-query HMAC contracts. Execute after commit, persist classifications in short transactions, query ambiguous outcomes before safe retry, and apply success through the atomic refund coordinator.
8. Add audited idempotent VietQR manual settlement and shared successful-refund application so both providers produce the same payment/order/inventory invariants.
9. Add Gin routes and problem mapping using existing user, guest-grant, workforce, tenant, timeout, request-ID, and no-store patterns; update OpenAPI and project documentation.

## Migration and compatibility

- Deploy migration 10 before binaries that serve cancellation/refund routes. The migration is additive apart from expanding bounded payment/permission/audit state constraints and adding a captured MoMo transaction identifier needed for refunds.
- Backfill the MoMo transaction identifier for existing successful schema-9 payments from their successful webhook receipt inside the migration, reject ambiguous/missing successful MoMo rows during validation, then enforce the provider-shaped refund binding. Existing VietQR rows need no rewrite or decryption.
- Existing pending, expired, and reconciled orders/payments remain unchanged. Existing successful payments become refundable only when their provider settlement identity is complete and the order is still before shipment.
- Refund tables start empty; adding nullable identity/state columns precedes backfill and constraint validation. Test empty and populated schema-9 upgrades, repeat application, and lock-sensitive constraint validation.
- If migration application fails, keep schema-9 binaries running and forward-fix/retry migration 10 before deploying the new code. Do not down-migrate retained refund, financial, inventory-ledger, audit, or outbox records in production.
- No new runtime dependency is planned; the standard library and existing keyring, HTTP client, HMAC, GORM, Gin, Audit, and Eventing adapters cover this slice.

## Ordered implementation plan

1. Add permissions, cancellation/refund domain rules, IDs, models, and unit tests for actors, states, amounts, reasons, and idempotency.
2. Add migration 10 and PostgreSQL models/repositories for refunds, manual settlements, MoMo capture identity, expanded financial states, permissions, audit actions, and tenant constraints; cover empty/populated upgrade and repeat.
3. Add Orders/Payments/Inventory transaction participants and implement atomic unpaid cancellation, paid refund creation, successful settlement/restock, rollback injection, and global lock ordering.
4. Add the MoMo refund/query adapter and short-transaction orchestration with exact signatures, stable identities, timeout/ambiguous/final classification, and query-before-retry.
5. Add VietQR settlement and refund-read use cases with authorization, deduplication, changed-request conflicts, and audit/outbox behavior.
6. Add shopper and workforce Gin routes, OpenAPI schemas, authentication/authorization/problem handling, no-store headers, and cross-store denial tests.
7. Add synchronized cancellation-versus-confirmation, duplicate cancellation/refund, refund-versus-adjustment, permission-revocation, and provider retry/query integration tests plus VietQR/MoMo regressions.
8. Update schema/domain/event/status/configuration and operating documentation, then run formatting, lint, focused unit/API tests, real PostgreSQL migration/transaction/isolation/concurrency tests, vulnerability scanning, and `make verify`.

## Acceptance criteria

- [x] Given an accessible pending order, when its owning user or guest cancels with an idempotency key, then the order and pending payment close, active reservations release once, availability returns, physical stock is unchanged, and replay returns the same outcome.
- [x] Given a current workforce actor with `order.cancel`, when they cancel an unpaid pre-shipment order, then cancellation, reservation release, audit/history, and outbox facts commit atomically; inactive, revoked, unauthorized, or foreign-store actors are denied.
- [x] Given a paid or processing pre-shipment order, when authorized cancellation is requested, then one full-amount refund and refund workflow are created without claiming settlement, restocking inventory, or terminally cancelling the order.
- [x] Given a MoMo refund, when execution succeeds, retries, times out ambiguously, or permanently fails, then stable signed provider identities, query-before-retry, and durable classification produce at most one financial effect without holding database locks during network I/O.
- [x] Given an externally settled VietQR refund, when an authorized actor records the exact full settlement, then one audited result is applied; identical replay returns it and changed reference/time conflicts.
- [x] Given a durable successful refund, when it is applied, then refund success, payment refunded, order cancellation, exact inventory restock, immutable ledgers, audit/history, and outbox events commit together or all roll back.
- [x] Given duplicate or concurrent cancellation, payment confirmation, refund, settlement, expiry, and stock adjustment operations, when they race, then the global lock order yields one valid outcome without negative/duplicated stock or duplicate financial/event effects.
- [x] Given any foreign-store order, payment, refund, idempotency key, guest grant, membership, settlement reference, or provider identity, when it probes a cancellation/refund surface, then it cannot resolve or mutate another store's data.
- [x] OpenAPI, configuration, schema/domain/event/status docs, both independent reviews, and applicable final verification pass.

## Verification strategy

| Requirement or risk | Required evidence |
| --- | --- |
| Domain and authorization | Unit/application tests for shopper/workforce eligibility, lifecycle boundaries, full-only money rules, permission defaults/grants/revocation, and idempotency conflicts |
| Tenant isolation | API and PostgreSQL cross-store tests for orders, refunds, settlement references, idempotency keys, guest grants, memberships, and provider identities |
| Atomic cancellation/refund | Real PostgreSQL failure injection proving order/payment/refund/reservation/stock/ledger/history/audit/outbox commit or roll back together |
| Concurrency | Synchronized cancellation-versus-confirmation, duplicate refund, refund-versus-adjustment, expiry, and permission-revocation tests with one effect and the documented lock order |
| MoMo compatibility | Official refund/query HMAC fixtures, exact field/bounds tests, response classification, controlled timeout/disconnect/HTTP/result-code tests, and proof no transaction spans the network call |
| VietQR manual settlement | Authorization, exact-full-amount binding, replay/change conflict, external-reference uniqueness, audit, and cross-store tests |
| Migration and compatibility | Empty and populated schema-9 upgrade, repeat migration, constraint/backfill checks, and existing checkout/VietQR/MoMo regression suites |
| Final checks | `gofmt` on changed Go files, `make lint`, focused `go test` commands, `make test-integration`, `make vulncheck`, and `make verify` |

## Required completion report

- Behavior and design summary
- Files/modules changed
- Commands executed with results
- Risks, assumptions, and follow-ups

## Delivery record

- Stage: complete
- Active task: no
- Last command, completed stage, pending gates, and next suggested command: both independent re-reviews approved the corrected implementation and `make verify` passed on 2026-09-17; no task-16 gates remain.
- Plan revision and approval: revision 1 was approved by the user on 2026-09-16 (`approve`).
- Plan: add tenant-safe shopper/workforce cancellation, explicit cancellation/refund permissions, one full-refund aggregate, post-commit MoMo refund/query execution, audited VietQR settlement, atomic inventory restock and financial/order completion, migration 10, APIs, contracts, and documentation as detailed above.
- Verification: acceptance criteria map to focused domain/API tests, official provider fixtures, cross-store denial, real PostgreSQL migration/rollback/concurrency tests, existing payment regressions, lint, vulnerability scanning, and full verification.
- Architect review: approved after corrections and focused Gin boundary evidence; no remaining findings.
- Security review: approved after financial binding, provider classification, timestamp, tenant, concurrency, and rollback corrections; no remaining findings.
- Final evidence: `make verify` passed on 2026-09-17 with formatting, zero-issue lint, all unit tests, all real integration tests at schema 10, and vulnerability scanning reporting no vulnerabilities in called or imported packages. Focused migration tests covered successful, missing, and ambiguous MoMo identity backfill; focused transaction tests covered tenant denial, permission revocation, duplicate replay, cancellation-versus-confirmation, refund-versus-adjustment, rollback, and durable MoMo query-before-retry.
