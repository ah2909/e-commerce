# Testing strategy

## Purpose

Tests establish tenant isolation, commerce correctness, retry safety, contract stability, and operability. A passing unit suite alone cannot establish PostgreSQL transaction, constraint, locking, migration, Redis atomicity, or provider-signature behavior.

## Test layers

| Layer | Establishes | Typical dependencies |
| --- | --- | --- |
| Domain unit | Value objects, invariants, state transitions, money calculations | None; fixed clock/IDs |
| Application | Use-case orchestration, permissions, failure classification, idempotent decisions | Controlled consumer-owned ports |
| Repository integration | PostgreSQL keys, constraints, transactions, locking, concurrency, migrations | Real isolated PostgreSQL |
| Redis integration | Atomic consumption, rate limits, expiry, store-scoped keys | Real isolated Redis |
| API/contract | Host selection, authentication, authorization, validation, problem responses, OpenAPI alignment | In-process API plus controlled dependencies |
| Provider contract | Signature/claim verification, request mapping, timeouts, error classification, idempotency | Recorded/local fixtures; provider sandbox where available |
| Event contract | Envelope/schema compatibility, outbox/inbox behavior, duplicate and ordering tolerance | PostgreSQL and Kafka-compatible fixtures |
| End to end | Critical admin and customer journeys | Disposable production-like stack |
| Operational | Restore, migration, load, failover, alert and runbook behavior | Isolated environment |

## General design rules

- Prefer table-driven tests for business-rule variations.
- Test observable behavior and invariants, not private implementation structure.
- Use fixed clocks and deterministic ID/token generators where time and identity matter.
- Preserve and inspect errors with `errors.Is` and `errors.As`; assert retryable/permanent classifications.
- Bound every external-call test and include cancellation/timeout behavior.
- Do not mock PostgreSQL behavior when making transaction, constraint, lock, index, or concurrency claims.
- Integration data is unique per test and cleaned safely; mutation is refused outside an explicitly identified test database.
- Concurrency tests synchronize the contested point rather than depending on sleep timing.
- Tests must not log or snapshot real credentials, tokens, provider secrets, or unnecessary personal data.

## Mandatory tenant-safety matrix

Every new tenant-owned resource tests at least:

- Store A cannot read Store B by ID, list cursor, relationship, search value, cache key, token, job payload, or event reference.
- Store A cannot create or update a relationship to Store B data; the composite foreign key rejects bypass attempts.
- Store A cannot update, archive, delete, confirm, cancel, refund, or reconcile Store B state.
- The same tenant-owned natural value can coexist across stores when required: email, social subject, slug, SKU, order number, idempotency key, and provider reference.
- Host, path, header, body, callback state, or forwarded-header mismatch fails closed.
- Guest credentials, user access/refresh tokens, and order grants are valid only for their issuing store.

Cross-store denial is an acceptance criterion, not optional security hardening.

## Commerce safety cases

### Money and pricing

- Integer VND calculations, deterministic list selection, VAT-inclusive presentation, discount caps, zero boundaries, and overflow rejection.
- Fixed and percentage coupon rounding and usage accounting under concurrent redemption.
- Checkout rejects stale/client-tampered price, currency mismatch, archived product, and ambiguous price.

### Inventory

- Concurrent buyers cannot reserve the last unit twice.
- Reservation creation and ledger/order rollback together on transaction failure.
- Confirm, release, and expire are idempotent.
- Confirmation racing expiration has one deterministic winner and preserves quantity.
- Expiry workers can run concurrently with safe bounded claims.
- Manual adjustment requires reason, permission, and audit; audit failure rolls back adjustment.

### Checkout and orders

- Repeating a checkout idempotency key returns one order; reusing it with a different request fingerprint is rejected.
- Cart, user/guest, product, price, shipping rule, and address cannot cross stores.
- Catalog, price, address, shipping, or customer changes never modify an existing order snapshot.
- Invalid order transitions are rejected under optimistic concurrency.
- Guest order number/email/phone without a valid order grant does not reveal the order.
- Revoked, expired, replayed, or wrong-store guest grants fail without resource enumeration.

### Payments and refunds

- VietQR generation never confirms payment.
- Authorized confirmation records actor, reason/reference, timestamp, and audit atomically.
- Duplicate or concurrent manual confirmation has one payment/order/inventory effect.
- MoMo fixtures cover valid signature, invalid signature, wrong merchant, store, amount, currency, reference, duplicate event, malformed body, provider 4xx/5xx, timeout, and cancellation.
- Webhook raw bytes are verified before parsing-dependent mutation.
- A late success, wrong amount, or wrong reference creates one reconciliation item and does not restore an expired order.
- Repeated webhook/provider response processing has one business effect and returns the documented acknowledgement.
- Full refund requests are authorized, audited, amount-bounded, and idempotent.
- MoMo refund retry and callback duplication have one result; manual VietQR settlement requires independent recorded confirmation.
- Payment success racing cancellation/refund preserves the state machine and financial/inventory totals.

### Events

- Aggregate change and outbox insert commit or roll back together.
- A crash after Kafka publish but before marking the outbox row may republish safely.
- Consumer inbox deduplicates concurrent and sequential delivery in the same transaction as its effect.
- Unknown additive fields are tolerated; unsupported versions and malformed envelopes enter an observable terminal path.
- Store scope, correlation, aggregate identity/version, and personal-data restrictions are contract-tested.

## Identity and authorization cases

- Platform administrators and tenant memberships are distinct; owner/admin credentials cannot provision a store.
- Store provisioning is unavailable anonymously and rolls back store/user/owner/audit together on failure.
- Same email/social subject can register independently across stores, but every session operation denies cross-host reuse.
- Password hashing admission is bounded and cancellable; changing an email cannot bypass peer-level rate policy.
- Refresh rotation, reuse detection, logout, account disablement, guest lifecycle, and store suspension are tested sequentially and concurrently.
- OAuth state and exchange codes bind browser, provider, return path, and store and are consumed only by a matching attempt.
- Current memberships are resolved for each privileged request and permissions deny by default.
- The final active owner cannot be removed or demoted under concurrent commands.

## Migration tests

- Apply every migration from an empty database and verify schema version.
- Test forward migration against representative populated data and tenant skew.
- Prove ambiguous tenant backfills refuse to guess and report actionable records.
- Exercise unique/check/foreign-key enforcement and expected query indexes.
- Measure or reason explicitly about locks for large-table changes; test concurrent compatibility during expand/contract phases.
- Verify recovery uses a forward fix and never depends on an untested destructive down migration.

## API and schema checks

- Every operation has request/response schemas, security requirements, expected error responses, and stable operation IDs in OpenAPI.
- Problem responses contain `code`, `message`, `details`, and `request_id` without raw dependency errors.
- Boundary limits cover body size, string/list lengths, pagination maximums, MIME/media metadata, and unknown fields.
- Tests resolve every local OpenAPI reference and compare security-sensitive route responses with handler behavior.

## Performance and scale testing

Benchmarks are comparisons, not portable service-level guarantees. Before production, load tests use realistic tenant skew and cover catalog browsing, authenticated admin work, checkout contention, reservation expiry, webhook bursts, outbox backlog, and database pool saturation.

Record latency distributions, errors, throughput, pool wait, locks, query plans/buffers, CPU, memory, database growth, and backlog age. A scale change such as a new index, replica, partition, placement tier, or service extraction requires evidence from these measurements plus regression and recovery tests.

## Operational verification

- Restore encrypted backups into an isolated environment and verify schema, tenant isolation, counts/checksums, order/payment/ledger consistency, outbox continuity, and active secret-key versions.
- Exercise dependency loss: PostgreSQL, Redis, Kafka, object storage, MoMo, email, and DNS/TLS where applicable.
- Confirm readiness, retry, reconciliation, dead-letter, alert, and runbook behavior.
- Validate secret rotation without exposing plaintext or requiring simultaneous downtime.
- Set and measure operator-approved RPO/RTO/SLO before production launch.

## Quality gates

For backend changes, run:

```bash
make fmt-check
make lint
make test
make test-integration
make vulncheck
```

`make verify` may be used when it runs all five and propagates failures. On Windows, record when race-enabled tests are unavailable and rely on supported CI/WSL for that evidence.

Documentation/workflow-only changes require diff inspection, local-link validation, terminology/decision consistency, and format-specific parsing for changed configuration. A development-status claim additionally requires the code checks that claim cites. Never report a skipped, unavailable, or failed command as passing.
