# Implementation roadmap

## Delivery rule

Deliver vertical slices in dependency order. Each slice includes domain rules, schema, application use cases, `/api/v1` contract, authorization, audit where required, observability, migrations, tenant-denial tests, and focused operating documentation.

This file describes intended sequence, not completion. [Development status](development-status.md) records actual evidence.

## Slices

1. **Foundation**
   - Go service layout, configuration, structured logging, health checks, PostgreSQL/Redis/Kafka development dependencies, migrations, CI, test database guard, and verification commands.

2. **Tenant context**
   - Store aggregate, immutable subdomain, strict host parsing, authenticated tenant context, store-scoped repository patterns, Vietnam defaults, and cross-store test harness.

3. **Platform administration and Identity stabilization**
   - Replace public/self-service onboarding with platform-admin-authenticated provisioning.
   - Complete tenant-owned users, password/social login, rotating sessions, store-bound guests, fixed workforce roles, explicit permissions, mandatory audit, provider callback binding, and all unresolved review/concurrency evidence.
   - This correction precedes new commerce modules because store creation is a security boundary.

4. **Catalog basics**
   - Draft, publish, archive, and list physical products, variants, categories, options, media references, SKU/barcode, weight, and dimensions.
   - Include keyset pagination and overlapping slug/SKU cross-store tests.

5. **VND pricing and coupons**
   - Store price lists, integer-money value object, deterministic price selection, VAT-inclusive presentation, fixed/percentage coupons, validity and usage accounting.

6. **Single-location inventory**
   - Stock items, immutable ledger, manual adjustment audit, atomic ten-minute reservations, confirmation, release, expiry worker, and real PostgreSQL concurrency tests.

7. **Customers and carts**
   - Store-scoped profiles and Vietnamese delivery addresses, guest/user carts, line mutation, estimated totals, expiration, and archive behavior.

8. **Checkout and orders**
   - Authoritative pricing, flat/free shipping quote, atomic inventory reservation, immutable line/address/shipping snapshots, order state machine, checkout idempotency, guest order grants, and outbox writes.

9. **VietQR payments**
   - Encrypted store configuration, payment instruction/QR generation, unique transfer reference, audited owner/admin or explicitly permitted staff confirmation, expiry, duplicate-action safety, and late/mismatched reconciliation.

10. **MoMo payments**
    - Encrypted credentials, bounded provider adapter, idempotent attempts, signature-validated raw-body webhooks, automatic confirmation, replay/concurrency tests, and late-result reconciliation.

11. **Cancellation and full refunds**
    - Customer/guest unpaid cancellation, authorized pre-fulfillment cancellation, full-refund state machine, MoMo refund adapter, manual VietQR settlement record, audit, retries, and reconciliation.

12. **Simple fulfillment**
    - Free/flat shipping configuration, shipping snapshot, staff-entered carrier/tracking, shipped/completed transitions, and notifications. Automated carrier integration remains deferred.

13. **Event workers and derived integrations**
    - Concurrent outbox publisher, Kafka topics, consumer inbox/deduplication, dead-letter/reconciliation handling, email example, search projection, analytics, and backlog observability.

14. **Operational hardening**
    - Deployment configuration, secret/key rotation, dashboards, alerts, slow-query review, backup/PITR, restore exercise, dependency upgrades, security testing, capacity/load testing, runbooks, and explicit RPO/RTO/SLO approval.

## Release milestones

- **Internal admin foundation:** slices 1–3 pass independent architecture/security review and final verification.
- **Catalog preview:** slices 4–7 support store setup and cart testing without accepting money.
- **VietQR pilot:** slices 8–9 and the required event/operational subset support controlled manual-payment orders.
- **MoMo pilot:** slice 10 passes provider fixtures, webhook security, concurrency, and reconciliation testing.
- **MVP production:** slices 11–14 satisfy the documented product success criteria and an operator-approved launch checklist.

## Scale checkpoints

At each production milestone, capture store count, catalog skew, order volume, peak request rate, database size/growth, slow queries, pool wait time, reservation conflicts, queue lag, and recovery results. Apply the scale responses in [system architecture](architecture.md) and [database design](database-design.md) only when those measurements justify them.

## Extraction rule

Do not split a module into a microservice because it appears independent. Consider extraction only after evidence of separate scaling, ownership, release cadence, failure isolation, or regulatory needs outweighs network, consistency, and operating cost. Record the decision, data ownership, migration, rollback, and reliability design in an ADR.
