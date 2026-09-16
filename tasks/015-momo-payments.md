# Task: Deliver tenant-safe MoMo payments

## Context

This is roadmap slice 10 and builds on [VietQR payments](archive/014-vietqr-payments.md). The target behavior is defined by the [product requirements](../docs/product-requirements.md), [architecture](../docs/architecture.md), [domain model](../docs/domain-model.md), [database design](../docs/database-design.md), [event catalog](../docs/event-catalog.md), [testing strategy](../docs/testing-strategy.md), and MoMo's official [integration process](https://developers.momo.vn/v3/docs/payment/onboarding/integration-process/), [sandbox test instructions](https://developers.momo.vn/v3/docs/payment/onboarding/test-instructions/), [one-time payment](https://developers.momo.vn/v3/docs/payment/api/wallet/onetime/), [payment notification](https://developers.momo.vn/v3/docs/payment/api/result-handling/notification/), [idempotency](https://developers.momo.vn/v3/docs/payment/api/result-handling/idempotency/), and [result-code](https://developers.momo.vn/v3/docs/payment/api/result-handling/resultcode/) contracts.

## Outcome

- A store owner or administrator can configure one enabled MoMo merchant account while access and secret keys remain encrypted at rest and redacted from reads.
- An authorized shopper can idempotently start the pending MoMo payment created at checkout and receive the provider action URLs from one bounded, signed `captureWallet` request.
- A valid MoMo IPN confirms funds exactly once and atomically pays the order, succeeds the payment, consumes reserved stock, appends inventory ledgers, records the webhook receipt, and emits outbox facts.
- Invalid signatures have no durable business effect; duplicate notifications acknowledge the recorded result; failed, late, expired, mismatched, or ambiguous results are classified without reviving closed commerce state.
- Provider timeouts and non-final results remain durably retryable with the same MoMo request identity, while final failures cannot be replayed into success without a new permitted attempt.
- Before completion, an opt-in live test proves initiation and signed-IPN settlement against MoMo's sandbox with credentials supplied outside the repository and an HTTPS callback reachable by MoMo.

## In scope

- Expand Payments with encrypted MoMo configuration, a bounded provider adapter, signed request creation, durable attempt execution, raw-body IPN verification, receipt deduplication, failure classification, and reconciliation.
- Add tenant-hosted configuration and shopper attempt APIs plus one public provider-callback route under `/api/v1`.
- Reuse the accepted Payments-coordinated PostgreSQL settlement transaction and global lock order across Orders and Inventory.
- Add migration 9, runtime provider/end-point settings, OpenAPI and environment documentation, development status, and focused operational notes.
- Support MoMo one-time wallet payments using `captureWallet`; persist and return only the documented action URLs required by the shopper.
- Add a secret-safe sandbox UAT command and runbook that exercise the real create-payment endpoint and a completed test-wallet payment through the public IPN route.

## Out of scope

- MoMo refunds, recurring/tokenized payments, subscriptions, cards, disbursement, transaction inquiry workers, or automatic retry workers.
- Customer/guest or workforce cancellation, reconciliation resolution, partial payment/refund, and fulfillment.
- Treating browser redirects as payment proof, storing raw credentials or full callback bodies, exposing provider secrets, or accepting tenant-selected provider/API/IPN hosts.
- Production merchant onboarding, production credentials, deployment, permanent DNS/TLS changes, or production migration execution.

## Invariants and security constraints

- Store scope comes from validated tenant context for shopper/admin routes. IPNs resolve a candidate store only through an opaque server-issued callback route bound to a Payments-owned configuration; payload fields never establish tenant scope.
- MoMo partner code, access key, and secret key are encrypted with the existing AES-256-GCM keyring. PostgreSQL stores a bounded redacted merchant identifier, ciphertext, nonce, key version, and a hash of the callback-route token, but no plaintext keys or route token.
- The MoMo API base URL, IPN origin, redirect target, request type, language, and HTTP timeout are deployment-owned configuration. Tenant input cannot select an outbound host, preventing SSRF and signature confusion.
- Outbound calls use a shared `http.Client` with a 35-second request timeout, request context, response-size limit, strict JSON decoding, and a result-code classifier that distinguishes final customer/merchant failures, retryable provider failures, and ambiguous transport outcomes. The timeout exceeds MoMo's documented 30-second minimum while remaining bounded.
- Attempt creation commits before the external call. Every retry under the same store/idempotency key reuses the persisted MoMo `orderId` and `requestId`; changed input conflicts, and no database transaction remains open during network I/O.
- Request and IPN signatures use HMAC-SHA256 over the exact ordered MoMo fields with constant-time comparison. The raw body is bounded and retained only long enough to verify, hash, and strictly decode it; secrets, signatures, full bodies, and action URLs are excluded from logs, events, errors, and audit data.
- IPN validation binds callback route, partner code, MoMo order/request/transaction identities, expected VND amount, stored attempt, and supported result code before any business transition. Browser redirects never confirm payment.
- Webhook receipts and attempt/provider identities are tenant-aware and unique. Identical duplicates return the prior acknowledgement; an identity reused with a different body digest is rejected and surfaced for operations without replaying a business effect.
- Valid success uses the established lock order: order, payment, reservations by reservation ID, then stock by variant ID. States and the database deadline are rechecked after locks are held.
- A payable success decrements `on_hand`, confirms reservations, appends matching immutable ledgers, succeeds payment/attempt, pays the order, records the receipt, and writes outbox facts atomically.
- If expiry/cancellation already won, the database deadline passed, a reservation is unavailable, or signed provider facts mismatch the stored attempt, the transaction records reconciliation when receipt authenticity and payment identity are established; it never revives the order or consumes inventory.
- Failed and pending provider results never consume stock or mark an order paid. Duplicate or concurrent initiation, IPN, expiry, adjustment, and VietQR activity produce one valid outcome.
- Live sandbox credentials are provided only through process environment or an ignored local secret file. The sandbox command refuses the production MoMo host, redacts all credential values and signatures, uses uniquely generated test identities, and records no secret or full callback payload in artifacts.

## Proposed HTTP contract

- `GET /api/v1/admin/payment-providers/momo` returns redacted configuration to owner/admin.
- `PUT /api/v1/admin/payment-providers/momo` uses `expected_version` and accepts `enabled`, bounded `partner_code`, `access_key`, and `secret_key`; it returns redacted configuration and writes an audit record atomically.
- `POST /api/v1/orders/{order_id}/payments/momo/attempts` requires the order owner session or guest order grant plus `Idempotency-Key`; it creates or resumes one provider request and returns attempt state and bounded `pay_url`, `deeplink`, and `qr_code_url` values with `Cache-Control: no-store`.
- `POST /api/v1/payment-webhooks/momo/{callback_token}` accepts a bounded raw JSON body, performs no tenant middleware or shopper authentication, resolves configuration from the high-entropy server-issued route token, verifies the MoMo signature and stored attempt binding, and returns the provider-required acknowledgement only after the receipt outcome is durable.
- Validation uses 400, authentication 401, authorization 403, scoped absence 404, optimistic/idempotency conflict 409, invalid payment eligibility 422, and dependency/ambiguous provider failure 503. Invalid callback routes or signatures fail closed without revealing whether a store, payment, or merchant exists.

## Technical solution

1. Generalize the Payments domain and repository boundary from VietQR-only configuration/attempts to provider-specific records while preserving the current VietQR API and behavior.
2. Add a MoMo configuration envelope and a random callback token whose digest is used only by a narrowly scoped callback resolver; rotate the callback token whenever merchant credentials are replaced.
3. Add a standard-library MoMo adapter that builds the documented `captureWallet` request, signs exact canonical fields, uses deterministic stored provider identities, applies strict response bounds, and classifies HTTP/transport/result-code outcomes.
4. Split initiation into short transactions around the external call: claim/create the attempt, call MoMo with no database locks held, then conditionally record action-required/final-failure/retryable outcome. Concurrent callers reuse or observe the same durable attempt.
5. Add a bounded raw-body IPN handler and verifier. Claim the tenant-aware receipt by callback route and provider identities, reject signature/binding conflicts, and make exact duplicates return the recorded acknowledgement.
6. Reuse the existing Orders and Inventory transaction participants to apply signed success in one transaction; extend reconciliation and outbox facts for authenticated late/mismatched MoMo funds and persist final failed results without commerce-state changes.
7. Add migration 9 to make provider configuration and attempt constraints provider-shaped without rewriting encrypted VietQR data, add MoMo attempt response fields and callback routing, and create webhook receipts with composite tenant foreign keys and uniqueness.
8. Wire deployment-owned MoMo URLs/timeouts, Gin routes, OpenAPI schemas, environment examples, README/status/schema/domain/event documentation, secret-safe structured diagnostics, and an opt-in `make test-momo-sandbox` UAT flow.

## Migration and compatibility

- Deploy migration 9 before code that serves MoMo routes. It is additive except for replacing VietQR-only check constraints with provider-shaped checks; it does not decrypt, backfill, or rewrite existing VietQR ciphertext, orders, payments, reservations, or ledgers.
- Existing VietQR rows remain valid and their API/confirmation behavior remains unchanged. Existing pending `momo` payments created by checkout become eligible after a store enables MoMo configuration.
- New nullable/provider-specific columns are added first, populated only for new MoMo rows, and protected by shape constraints after creation. Existing indexes and VietQR uniqueness remain intact.
- The webhook table and callback-routing index are created empty, so migration locking is limited to brief metadata/check-constraint work on the current pre-production payment tables. Verify empty and populated schema-8 upgrades and repeat application.
- If migration application fails, keep schema-8 binaries running and forward-fix/retry migration 9 before deploying MoMo-enabled code; do not down-migrate provider receipts or financial attempts in production.
- Credential rotation writes a new encrypted config version and callback route for future attempts. Existing attempts retain an encrypted credential/config snapshot needed to validate their bounded completion window; referenced key versions cannot be retired until those attempts are terminal and the documented retention window has elapsed.

## Ordered implementation plan

1. Add provider-neutral Payments models plus MoMo config validation, signature fixtures, redaction, callback-token generation, and result classification tests.
2. Add migration 9 and PostgreSQL repositories for MoMo configuration, attempt claiming/finalization, callback resolution, and webhook receipts; cover populated upgrade, constraints, and cross-store routing denial.
3. Implement the bounded MoMo `captureWallet` adapter and initiation orchestration with deterministic provider identities, no-transaction network calls, timeout/ambiguous retry behavior, and concurrent idempotency tests.
4. Implement raw-body IPN verification and durable duplicate/conflict handling, then connect valid success to the accepted atomic Payments/Orders/Inventory settlement path.
5. Add config, shopper initiation, and public IPN routes with owner/user/guest authorization, no-store responses, strict limits, problem mapping, signature denial, and cross-store tests.
6. Add MoMo failure/reconciliation/outbox behavior and synchronized IPN-versus-IPN, expiry, adjustment, configuration rotation, and VietQR regression tests.
7. Update OpenAPI, configuration/examples, schema/domain/event/status docs, and operational guidance; ensure no secret, signature, raw body, or action URL reaches logs/events/problems.
8. Run automated formatting, lint, focused unit/API tests, real PostgreSQL migration/transaction/isolation/concurrency tests, vulnerability scanning, and full verification because this is a security-sensitive external financial integration.
9. With user-supplied test credentials and a temporary or deployed HTTPS callback reachable by MoMo, run `make test-momo-sandbox`: initiate against the exact sandbox host, complete payment in a MoMo test wallet, receive and validate the signed IPN, and verify one paid order, one successful payment/attempt/receipt, one inventory consumption, and duplicate-IPN idempotency. Do not enable or test production.

## Acceptance criteria

- [x] Given valid store MoMo credentials, when an owner/admin writes or reads them, then secrets are encrypted with a key version, reads are redacted, the callback route is opaque, and cross-store access is denied.
- [x] Given a pending owned MoMo order, when its user or guest starts payment, then one signed bounded provider request uses the persisted exact VND amount and returns only safe action URLs without marking anything paid.
- [x] Given an identical store/idempotency key, when initiation races, retries, or times out ambiguously, then one attempt and one stable MoMo `orderId`/`requestId` are reused; changed input conflicts and another store remains independent.
- [x] Given a valid signed success IPN before the database deadline, when it is processed, then receipt, payment, order, reservations, stock, ledgers, and outbox facts commit atomically exactly once.
- [x] Given invalid signature, callback route, merchant identity, amount, order/request/transaction reference, malformed/oversized body, or conflicting duplicate, when an IPN is received, then it fails closed without cross-store disclosure or a commerce-state mutation.
- [x] Given duplicate/out-of-order IPNs and initiation responses, when they race with expiry or adjustment, then the recorded provider result is deterministic, stock never goes negative, and no business or outbox effect is duplicated.
- [x] Given authenticated late, expired, closed, reservation-unavailable, or mismatched funds, when the IPN is processed, then one tenant-scoped reconciliation item is visible and order/inventory are not revived or consumed.
- [x] Given provider timeout, transport failure, HTTP failure, and final/non-final result codes, when initiation executes, then retryable, permanent, and ambiguous failures are distinguished and the stored attempt can be safely resumed only where allowed.
- [ ] Given sandbox credentials and a MoMo-reachable HTTPS callback, when the live sandbox UAT runs, then a real `captureWallet` request returns an action URL, test-wallet completion delivers a valid IPN, settlement occurs exactly once, and replay remains idempotent without exposing credentials.
- [x] OpenAPI, configuration, schema/domain/event/status docs, both independent reviews, and applicable final verification pass.

## Verification strategy

| Requirement or risk | Required evidence |
| --- | --- |
| Encryption and routing secrecy | Keyring/config round-trip and tamper tests, ciphertext inspection, callback-token digest/rotation tests, and log/problem/OpenAPI redaction checks |
| MoMo wire compatibility | Official request/IPN HMAC fixtures, exact canonical-field tests, strict JSON/bounds tests, controlled HTTP responses, and result-code classification |
| Tenant and shopper isolation | API and PostgreSQL cross-store tests for config, attempts, callback routing, receipts, reconciliation, user order ownership, and guest grants |
| Network/idempotency safety | Controlled timeout/disconnect/5xx/4xx/final/non-final fixtures plus synchronized duplicate initiation proving stable request identities and no open database transaction during the call |
| Atomic confirmation | PostgreSQL failure injection for every participant with receipt/payment/order/reservation/stock/ledger/outbox commit-or-rollback assertions |
| Concurrency and reconciliation | Synchronized IPN-versus-IPN, expiry, adjustment, credential rotation, late/mismatched result, and conflicting-body tests with one durable outcome |
| Compatibility | Existing VietQR unit/API/integration suites plus schema-8 populated migration upgrade and migration-9 repeat tests |
| Live provider compatibility | `make test-momo-sandbox` against `https://test-payment.momo.vn` with externally supplied test credentials, a reachable HTTPS IPN endpoint, completed test-wallet payment, database/API state assertions, and duplicate-IPN replay |
| Final checks | `gofmt` on changed Go files, `make lint`, focused `go test` commands, `make test-integration`, `make vulncheck`, and `make verify` |

## Required completion report

- Behavior and design summary
- Files/modules changed
- Commands executed with results
- Risks, assumptions, and follow-ups

## Delivery record

- Stage: live sandbox UAT pending
- Active task: yes
- Last command, completed stage, pending gates, and next suggested command: `make verify` passed on 2026-09-15 after both final re-reviews passed; `make test-momo-sandbox` is blocked because all required `MOMO_SANDBOX_*` environment inputs and a MoMo-reachable HTTPS callback are unavailable.
- Plan revision and approval: revision 2 approved by the user on 2026-09-14 (`approve`).
- Plan: Payments adds encrypted MoMo configuration, deterministic idempotent `captureWallet` execution, opaque callback routing, raw-body signed IPN receipt handling, and atomic settlement/reconciliation through migration 9 and the existing Orders/Inventory participants; an opt-in secret-safe sandbox UAT is required before completion; no refund, cancellation, fulfillment, or background inquiry work.
- Verification: acceptance criteria map to official HMAC fixtures, adapter failure tests, API/tenant tests, real PostgreSQL migration/transaction/isolation/concurrency evidence, VietQR regressions, lint, vulnerability scanning, full verification, and a real sandbox initiation/payment/IPN/replay flow.
- Architect review: passed final re-review on 2026-09-15; initiation events, terminal retries, stale-result locking, rollback, expiry, and adjustment evidence were accepted.
- Security review: passed final re-review on 2026-09-15; signature/routing isolation, token log redaction, sandbox/origin controls, transaction identity, and retry classification were accepted.
- Final evidence: `make verify` passed on 2026-09-15: formatting, zero-issue lint, all unit tests, all integration tests against schema version 9, and vulnerability scanning with no vulnerabilities in called or imported packages. Live sandbox UAT remains the sole incomplete acceptance gate.
- Live payment execution: not tested against the MoMo sandbox yet; no sandbox payment or real signed sandbox callback is claimed as passing.
