# Task: Deliver tenant-safe VietQR payments

## Context

This is roadmap slice 9 and builds on [checkout and orders](013-checkout-orders.md). The target behavior is defined by [product requirements](../../docs/product-requirements.md), [architecture](../../docs/architecture.md), [domain model](../../docs/domain-model.md), [database design](../../docs/database-design.md), [event catalog](../../docs/event-catalog.md), and [testing strategy](../../docs/testing-strategy.md).

## Outcome

- A store owner or administrator can configure one enabled VietQR receiving account while sensitive account data remains encrypted at rest and redacted from reads.
- An authorized shopper can create or replay one payment instruction for a pending VietQR order and receive a standards-compatible QR payload and image containing the exact VND amount and a unique transfer reference.
- An owner, administrator, or explicitly permitted staff member can record a bank-verified transfer once; an eligible transfer atomically pays the order, succeeds the payment, consumes reserved stock, appends inventory ledgers and audit/history, and emits outbox facts.
- Late, expired, cancelled, wrong-reference, and wrong-amount transfers become visible reconciliation items without reviving the order or consuming inventory.
- Duplicate and concurrent instruction or confirmation requests have one durable business effect and return the recorded outcome.

## In scope

- Expand the Payments module with encrypted VietQR provider configuration, payment attempts/instructions, manual confirmations, and reconciliation items.
- Add tenant-hosted configuration, shopper instruction, privileged confirmation, and reconciliation-read APIs under `/api/v1`.
- Add `payment.confirm` and `payment.reconcile.view` permissions: owner/admin receive them by role; staff receive only explicit membership grants managed by an owner.
- Generate the VietQR transfer payload locally from a configured bank BIN/account, exact order amount, and bounded unique transfer reference; render a QR image without a network call.
- Coordinate payment confirmation across Payments, Orders, Inventory, Audit, and Eventing through one PostgreSQL transaction and the established global lock order.
- Add migration 8, runtime keyring configuration, OpenAPI and environment documentation, development status, and focused operational notes.

## Out of scope

- Automatic bank transaction feeds, VietQR merchant callbacks, account ownership verification, or any claim that a generated QR proves payment.
- MoMo attempts, callbacks, webhooks, credentials, or refunds.
- Customer/guest or workforce cancellation, reconciliation resolution, externally settled refunds, partial payments/refunds, and fulfillment.
- Provider-hosted QR generation, production bank credentials, deployment, or production migration execution.

## Invariants and security constraints

- Store scope comes only from validated host/authenticated context; every config, payment, attempt, confirmation, reconciliation item, permission grant, audit record, query, lock, key, and event includes `store_id`.
- VietQR account configuration is encrypted with AES-256-GCM using a deployment keyring; PostgreSQL stores ciphertext, nonce, and key version but no plaintext account number or encryption key.
- Configuration reads expose bank identity, account-holder display name, enablement, version, and only a masked account number; logs, errors, audit, events, and metrics contain no account number, plaintext config, key, or QR payload.
- Encryption uses a fresh random nonce on every write, authenticated associated data binds store/provider/config version, and unknown/retired key versions fail closed.
- QR generation is local and records an instruction only. It never changes payment, order, or inventory to paid/confirmed states.
- The transfer reference is bounded for VietQR additional data, cryptographically unpredictable, unique within a store, and durably bound to store, order, payment, attempt, amount, currency, and provider-config version.
- The payment amount comes only from the persisted payment/order; clients cannot override it. All money is integer VND minor units.
- Instruction creation is store-scoped and idempotent. A changed request under the same idempotency key conflicts; concurrent identical requests create one attempt/reference.
- Confirmation requires a current active workforce membership and permission. Actor/store/payment/reference/amount fields are never inferred from caller-controlled tenant scope.
- Confirmation and expiry share the lock order: order, payment, reservations by reservation ID, then stock by variant ID; all states and the database deadline are rechecked after locks are held.
- A payable matching transfer decrements `on_hand`, confirms each reservation, appends equal immutable inventory ledger deltas, succeeds the payment, transitions the order to paid through the central state machine, records confirmation/audit/history/idempotency, and appends outbox facts in one transaction.
- If expiry/cancellation already won, the database deadline passed, or reference/amount/provider/config binding mismatches, the transaction records one reconciliation item and emits only `payment.reconciliation_required.v1`; it never revives an order or consumes inventory.
- Repeated or concurrent confirmations return the original recorded outcome and never duplicate ledger, audit, history, reconciliation, or outbox effects.

## Proposed HTTP contract

- `GET /api/v1/admin/payment-providers/vietqr` returns redacted configuration to owner/admin.
- `PUT /api/v1/admin/payment-providers/vietqr` uses `expected_version` and accepts `enabled`, six-digit `bank_bin`, bounded numeric `account_number`, and bounded normalized `account_name`; it returns the redacted configuration and writes an audit record atomically.
- `PATCH /api/v1/memberships/{user_id}/permissions` lets an owner replace a staff member's bounded grants from the supported payment-permission set using `expected_membership_version`.
- `POST /api/v1/orders/{order_id}/payments/vietqr/instructions` requires the order owner session or guest order grant plus `Idempotency-Key`; it returns attempt/reference, exact amount/currency, expiry, VietQR payload, and base64-encoded PNG, with `Cache-Control: no-store`.
- `POST /api/v1/admin/payments/{payment_id}/vietqr-confirmations` requires `Idempotency-Key`, `payment.confirm`, and observed `transfer_reference`, `amount_minor`, and RFC 3339 `received_at`; it returns either the paid result or a reconciliation-required result.
- `GET /api/v1/admin/payment-reconciliation-items` requires `payment.reconcile.view` and returns a bounded cursor page of unresolved items without a resolution mutation in this slice.
- Validation uses 400, authentication 401, authorization 403, scoped absence 404, optimistic/idempotency conflict 409, invalid payment eligibility 422 where no reconciliation record is appropriate, and dependency/key failures 503.

## Technical solution

1. Extend fixed-role authorization with the two payment permissions and a tenant-owned membership-permission table. Keep owner-only grant mutation, deny non-staff grants, and prevent arbitrary permission strings.
2. Add a Payments application service and domain states for encrypted provider configuration, VietQR attempts/instructions, confirmation outcomes, and reconciliation reasons.
3. Add a deployment keyring configuration with an active numeric version and one or more unpadded base64url 32-byte keys. Use standard-library AES-GCM behind a narrow crypto interface so persistence never handles plaintext beyond the adapter boundary.
4. Implement NAPAS/VietQR EMV-compatible TLV generation, CRC validation, bounded ASCII transfer content, deterministic test vectors, and PNG rendering. Add one pinned pure-Go QR encoder dependency because the standard library has no QR matrix/image encoder; no provider SDK or runtime network dependency is introduced.
5. Add migration 8 for encrypted provider configs, payment attempts, manual confirmation deduplication/receipts, reconciliation items, and explicit membership permission grants, with composite tenant keys, bounded columns, state checks, and uniqueness scoped by store.
6. Make Payments the confirmation coordinator with narrow transaction-aware Orders and Inventory participant ports. Orders owns locking/history/state transition; Inventory owns reservation/stock/ledger mutation; Audit and Eventing own their writes.
7. Add Gin handlers that reuse host selection, shopper/order-grant authentication, workforce authorization, request IDs, problem responses, timeouts, and no-store response handling.
8. Update OpenAPI, environment examples, README, schema/domain/event/status docs, and the task record to match the shipped behavior.

## Migration and compatibility

- Deploy migration 8 before code that serves VietQR routes. The migration is additive to schema 7 and does not rewrite existing orders, payments, reservations, or inventory ledgers.
- Existing pending VietQR payments remain eligible for instruction creation after a store enables configuration; existing MoMo payments remain unchanged and cannot enter VietQR endpoints.
- Membership permission grants default to none, so existing staff gain no privilege. Owner/admin behavior expands only to the documented payment operations.
- If migration application fails, keep schema-7 binaries running and forward-fix/retry migration 8 before deploying the new code; do not down-migrate production financial records.
- Key rotation is deploy-new-key, set active version, rewrite configs through the versioned API, verify, then retire the old key. Removing a referenced key before re-encryption makes affected configuration unavailable by design.

## Ordered implementation plan

1. Add payment permissions, explicit staff grants, authorization tests, and owner-only permission mutation.
2. Add Payments domain/application models for config, instruction, confirmation, and reconciliation with validation and state-transition tests.
3. Add versioned AES-GCM keyring configuration/encryption and VietQR payload/image generation with redaction and conformance tests.
4. Add migration 8 and PostgreSQL repositories for configs, attempts, confirmation deduplication, and reconciliation; cover empty/populated upgrade and tenant constraints.
5. Add Orders and Inventory transaction participants, then implement atomic manual confirmation with global lock order, rollback injection, expiry races, and duplicate/concurrent confirmation tests.
6. Add config, instruction, confirmation, permission-grant, and reconciliation-read HTTP routes with owner/guest/workforce and cross-store denial tests.
7. Add audit and outbox facts, update OpenAPI/configuration/docs/status, and validate contracts and secret redaction.
8. Run formatting, lint, focused unit/API tests, real PostgreSQL migration/transaction/isolation/concurrency tests, vulnerability scanning, and full verification because this is a security-sensitive cross-module financial change.

## Acceptance criteria

- [x] Given valid store VietQR settings, when an owner/admin writes or reads them, then plaintext is accepted only at the boundary, ciphertext is persisted with a key version, reads are redacted, and cross-store access is denied.
- [x] Given a pending owned VietQR order, when its owner or guest grant requests an instruction, then the QR encodes the configured destination, exact VND amount, and one durable unique reference without marking anything paid.
- [x] Given an identical store/idempotency key, when instruction requests race or retry, then one attempt/reference is returned; a changed request conflicts and another store remains independent.
- [x] Given a matching bank-observed transfer before the database deadline, when an authorized actor confirms it, then payment success, order paid, reservation consumption, stock decrement, ledger entries, history, audit, deduplication, and outbox events commit atomically.
- [x] Given duplicate or concurrent confirmation, expiry, or adjustment activity, when operations race, then lock ordering and guarded updates produce one valid outcome without negative stock or duplicated effects.
- [x] Given a late, expired, cancelled, wrong-reference, or wrong-amount transfer, when it is recorded, then one tenant-scoped reconciliation item is visible and the order/inventory are not revived or consumed.
- [x] Given owner/admin/staff roles, when payment operations are authorized, then owner/admin receive documented defaults, staff require an explicit owner-granted permission, and guests never satisfy workforce authorization.
- [x] Given any foreign-store ID, reference, idempotency key, membership grant, guest grant, or workforce session, when it probes VietQR resources, then it cannot resolve or mutate another store's data.
- [x] OpenAPI, configuration, schema/domain/event/status docs, both independent reviews, and applicable final verification pass.

## Verification strategy

| Requirement or risk | Required evidence |
| --- | --- |
| Encryption and redaction | Keyring/config unit tests, AES-GCM round trip/tamper/AAD/wrong-version tests, database ciphertext inspection, and log/problem/OpenAPI redaction checks |
| VietQR compatibility | Official-format TLV/CRC fixtures, field-boundary tests, decoded PNG content comparison, and exact amount/reference tests |
| Tenant and owner isolation | API and PostgreSQL cross-store tests for config, attempts, confirmations, reconciliation, permissions, user order ownership, and guest grants |
| Atomic confirmation | PostgreSQL failure injection for every participant plus assertions that payment/order/reservation/stock/ledger/history/audit/idempotency/outbox commit or roll back together |
| Concurrency and duplicate safety | Synchronized confirmation-versus-confirmation, confirmation-versus-expiry, and confirmation-versus-adjustment tests with one business effect |
| Reconciliation | Late, expired, wrong-reference, wrong-amount, already-closed, and duplicate observations produce one visible item and no paid/stock events |
| Authorization | Fixed-role and explicit-staff permission tests, inactive-membership denial, owner-only grant mutation, and guest denial |
| Final checks | `gofmt` on changed Go files, `make lint`, focused `go test` commands, `make test-integration`, `make vulncheck`, and `make verify` |

## Required completion report

- Behavior and design summary
- Files/modules changed
- Commands executed with results
- Risks, assumptions, and follow-ups

## Delivery record

- Stage: complete
- Active task: no
- Last command, completed stage, pending gates, and next suggested command: implementation, review fixes, both independent re-reviews, and final verification completed on 2026-09-14. No delivery gate remains; the next roadmap slice is MoMo execution and signed webhook confirmation.
- Plan revision and approval: revision 1 prepared and approved by the user on 2026-09-13 (`approve`).
- Plan: add tenant-safe encrypted VietQR configuration, standards-compatible local instructions, explicit staff permissions, atomic audited confirmation, and visible reconciliation as detailed above.
- Verification: acceptance criteria map to domain/API tests, encryption/redaction checks, VietQR conformance fixtures, real PostgreSQL migration/transaction/isolation/concurrency tests, OpenAPI parsing, lint, vulnerability scanning, and full verification.
- Implementation evidence: focused unit/API packages, the full unit suite, migration reset/up through schema 8, focused real PostgreSQL VietQR/permission isolation and concurrency tests, and lint passed.
- Architect review: passed final re-review after transactional, state, event identity, dependency, reservation, concurrency, and HTTP boundary corrections.
- Security review: passed final re-review after authorization, replay, event identity, key safety, and dependency classification corrections.
- Fix evidence: privileged mutations now lock active store/membership authority in the financial transaction; instruction identity is one-per-payment; changed reference observations conflict; succeeded payments remain succeeded while extra observations create reconciliation aggregates; key/crypto failures map to 503; production rejects the development key. Regression tests cover permission-revocation serialization, post-success and repeated reconciliation, changed observations, final-event rollback, populated migration-8 upgrade/repeat, production key rejection, response classification, and decoded PNG payload equality.
- Final evidence: `make verify` passed on 2026-09-14; formatting, zero-issue lint, all unit tests, all integration tests at schema 8, and vulnerability scanning passed with no vulnerabilities in called or imported packages. Architect and security re-reviews passed.
