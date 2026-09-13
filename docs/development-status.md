# Development status

## Snapshot

- Assessed: 2026-09-13
- Implemented baseline: commit `5bfd212a8c39e24ed976aebc47f4321aec94255c`, plus accepted tasks 007, 008, 009, 010, 011, 012, and 013
- Target baseline: [product requirements](product-requirements.md), [system architecture](architecture.md), [domain model](domain-model.md), and [database design](database-design.md)
- Current release verdict: **not production-ready**

This is the authoritative current-state summary. Detailed historical evidence remains in [task records](../tasks/README.md). A feature is complete only when its approved acceptance criteria, independent architecture/security reviews, and final verification pass on the same relevant artifacts.

Task 007 migrated production API routing and transport middleware to Gin and production repositories and cross-module transactions to GORM without changing Goose schema version 2. Independent architecture and security reviews passed after compatibility and hardening corrections, and final verification passed on 2026-09-10.

Task 008 completed tenant-scoped draft Catalog authoring. Both independent reviews and final verification passed on 2026-09-10.

Task 009 completed VND storefront price lists, deterministic current-price resolution, price-gated Catalog publication with an atomic outbox fact, and public priced Catalog reads. Both independent reviews and final verification passed on 2026-09-11.

Task 010 moved coupons, discount snapshots, and redemption accounting after the MVP production milestone. Inventory is now the next required slice; both independent documentation reviews and validation passed on 2026-09-11.

Task 011 completed single-location stock, immutable quantity ledgers, idempotent audited adjustments, ten-minute reservations and terminal transitions, public in-stock projection, and the bounded expiry worker. Both independent reviews and final verification passed on 2026-09-12.

Accepted [task 012](../tasks/archive/012-customers-and-carts.md) implements store/user-scoped customer profiles and Vietnamese saved addresses plus user/guest-owned VND carts, current batched estimates, optimistic mutation, and database-time expiry. Independent architecture/security reviews and final verification passed.

Accepted [task 013](../tasks/archive/013-checkout-orders.md) implements atomic user/guest checkout, authoritative VND price and stock validation, immutable order snapshots, store-scoped idempotency, guest order grants, flat/free shipping rules, minimal pending payments, transactional outbox facts, and coordinated order expiry. Both independent reviews and final verification passed.

## Status meanings

| Status | Meaning |
| --- | --- |
| Implemented and accepted | Required behavior exists and its task records passing review and final verification |
| Implemented, awaiting review | Required behavior and focused checks exist, but independent review and final verification are pending |
| Implemented, changes required | Substantial code exists, but correctness, security, changed requirements, review, or verification blocks acceptance |
| Partially implemented | Only part of the target capability exists |
| Not started | No meaningful domain implementation exists; infrastructure placeholders do not count |

## Roadmap status

| Slice | Status | Evidence and gap |
| --- | --- | --- |
| Foundation | Implemented and accepted | API/worker entry points, configuration, logging, health checks, Compose dependencies, migration runner, CI, Make verification targets, and guarded integration database are recorded complete in [task 001](../tasks/archive/001-bootstrap.md). Native Gin transport and GORM persistence adapters are accepted in [task 007](../tasks/archive/007-adopt-gin-gorm-project-wide.md). |
| Tenant context | Implemented, changes required | Store persistence, tenant middleware, and the task 003 isolation baseline exist. Task 004 changed host and session behavior; its two remaining medium findings are explicitly skipped and retained as accepted risks below. Current routing implements shared-base-domain subdomains, not the target customer-managed exact-host single-store mode. |
| Platform administration | Implemented, changes required | The protected base-domain route requires an explicitly supplied deployment platform token and atomically creates a sessionless owner account, membership, and audit record. Consolidated checks passed; the store-suspension/session race remains an explicitly accepted risk. |
| Identity and workforce authorization | Implemented, changes required | Store-owned users, passwords, Google/Facebook adapters, opaque sessions, guests, memberships, roles, permission middleware, bound Redis state, trusted-proxy-aware rate limits, audit, host-scoped routes, migration 2, and OpenAPI exist. Consolidated verification passed; database-authoritative credential expiry and atomic suspension/session admission are explicitly accepted unresolved risks. |
| Catalog | Implemented and accepted | Accepted [task 008](../tasks/archive/008-catalog-authoring-foundation.md) provides authoring. Accepted [task 009](../tasks/archive/009-vnd-pricing-catalog-publication.md) adds draft-to-published transition, price readiness, public priced list/detail reads, and transactional publication intent. |
| Pricing | Implemented and accepted | Task 009 implements integer VND Money, storefront price-list aggregates, deterministic current-price selection, admin APIs, and tenant-scoped persistence. Coupons and redemption accounting are intentionally deferred until after MVP. |
| Inventory | Implemented and accepted | Accepted [task 011](../tasks/archive/011-single-location-inventory.md) adds migration 5, tenant-scoped stock/reservation/ledger persistence, admin adjustment APIs, public boolean availability, atomic reservation lifecycle operations, outbox/audit effects, expiry processing, and contention/isolation/rollback evidence. |
| Customers and carts | Implemented and accepted | Accepted [task 012](../tasks/archive/012-customers-and-carts.md) adds migration 6, user-owned profiles and saved addresses, user/guest carts, current Catalog/Pricing/Inventory projections, schema-gated APIs, and bounded expiry. |
| Checkout and orders | Implemented and accepted | Accepted task 013 adds migration 7, the atomic checkout coordinator, immutable snapshots, central lifecycle policy, tenant idempotency, guest grants, order reads, and order-first expiry. |
| VietQR payments | Not started | No store provider configuration, QR instruction, audited payment confirmation, or reconciliation implementation exists. |
| MoMo payments | Not started | No provider adapter, signed webhook, attempt, receipt, or automatic confirmation implementation exists. |
| Cancellation and refunds | Not started | No order cancellation or full-refund workflow exists. |
| Shipping and fulfillment | Partially implemented | Task 013 adds versioned flat/free VND shipping rules and checkout snapshots. Shipment, carrier, tracking, and fulfillment transitions remain unimplemented. |
| Event workers | Partially implemented | Task 009 adds Eventing-owned tenant-scoped outbox persistence and atomically appends `product.published.v1`. Kafka publishing, retries, inboxes, consumers, and dead-letter handling remain unimplemented. |
| Operational hardening | Partially implemented | Structured logging, health checks, bounded shutdown/dependency configuration, CI, and local dependency controls exist. Backup/restore, dashboards, alerts, secret rotation, load/security tests, launch SLO/RPO/RTO, and production runbooks are not implemented or proven. |

## Implemented database schema

### Migration 00001

- stores with immutable unique subdomain, active/suspended status, regional defaults, version, and UTC timestamps
- store memberships keyed by store and user

### Migration 00002

- store-owned users, password credentials, social identities, user sessions, and access/refresh token digests
- store-bound guests and rotating guest token/session records
- membership roles
- limited Store/membership audit records with typed platform or tenant actors

### Migration 00003

- store-owned Catalog products, variants, category hierarchy, product-category assignments, and ordered media references
- scoped uniqueness for product/category slugs, variant SKUs, and optional barcodes
- optimistic versions, retained archive state, physical measurements in grams/millimetres, and keyset query indexes

### Migration 00004

- tenant-owned storefront VND price lists and variant prices with composite Catalog foreign keys
- validity, priority, lifecycle, optimistic-version, amount, and current-price query constraints/indexes
- Eventing-owned tenant-scoped outbox facts with aggregate-version deduplication and a future publisher claim index

### Migration 00005

- tenant-owned variant stock, ten-minute order reservations, and immutable quantity ledger entries
- non-negative physical/available quantity constraints, tenant Catalog foreign keys, optimistic versions, idempotent adjustment keys, and active-reservation indexes
- audit action/subject support for attributed manual inventory adjustments

### Migration 00006

- tenant-owned customer profiles and archived Vietnamese saved addresses keyed to store users
- user-or-guest VND carts with exactly-one-owner checks, one-active-cart uniqueness, optimistic versions, 30-day expiry, and terminal retention
- positive tenant Catalog cart lines with composite cart and variant foreign keys

### Migration 00007

- tenant-owned orders, immutable line/delivery/shipping/contact snapshots, per-store order numbers, checkout idempotency, state history, and digested guest grants
- one current versioned free/flat VND shipping rule, minimal provider-selected pending payments, cart conversion links, and pending-order expiry indexes
- Inventory reservation order references remain opaque for standalone compatibility; the Orders coordinator establishes their relationship in the checkout transaction

The schema intentionally does not include platform operators. It also does not include post-MVP coupons, provider configurations/attempts/webhooks, Refunds, shipments, or inbox tables.

## Current accepted risks and release blockers

Task 004 revision 5 implements the corrections requested by the prior reviews and the latest finding fixes: explicit platform-token injection, disabled-user refresh-family revocation, current-state social session admission, non-enumerating malformed membership handling, and bounded unauthenticated base64url parsing.

Independent architecture and security re-review completed with changes required. The two remaining medium findings are API-clock-based credential expiry under clock skew and non-atomic store suspension versus session issuance. On 2026-09-10, the user explicitly reaffirmed that both are skipped with no later Task 004 fix gate scheduled. They remain unresolved, keep Task 004 unaccepted under the workflow, and are knowingly accepted for continued roadmap work. Residual release validation also remains for live Google/Facebook interoperability, real ingress proxy configuration, customer-managed exact-host routing, production-size migration locking, race-enabled CI, and production secret rotation.

## Alignment that is already present

- Host-selected store context and immutable tenant subdomains are represented in current code and migration 1.
- User, credential, session, token, guest, membership, and audit persistence uses store-aware keys in migration 2.
- Same email and social provider subject are designed to coexist across stores.
- Access/refresh tokens are opaque and stored as digests; session rotation and reuse handling have focused tests.
- User and guest principals are distinct, and permissions deny by default.
- Membership targeting uses tenant-local user ID rather than unverified email.
- PostgreSQL, Redis, configuration, provider fixtures, API contract, and concurrency tests exist for substantial Identity behavior.
- Browser frontends can use the configured exact-origin CORS allowlist; cross-origin access is disabled when the allowlist is empty.
- No downstream commerce module falsely appears implemented.

These positives do not waive the blockers above.

## Public API status

[api/openapi.json](../api/openapi.json) describes the current Identity, guest, store, membership, Catalog, Pricing, Inventory, Customer, Cart, Shipping-rule, Checkout, Order, health, and onboarding endpoints. It remains an incremental implementation contract, not the complete MVP API.

`POST /api/v1/onboarding/stores` is base-domain-only, requires `platformAdminBearer`, and returns store and owner identifiers without issuing an owner session. Task 008 adds authenticated owner/admin authoring routes under `/api/v1/admin/catalog`.

Task 009 adds owner/admin price-list administration and authenticated Catalog publication. Task 011 adds owner/admin exact stock reads and idempotent manual adjustments under `/api/v1/admin/inventory`, while unauthenticated Catalog list/detail responses expose a boolean `in_stock` value. Task 012 adds registered-customer profile/address routes and one current Cart surface for either a user or guest. Task 013 adds owner/admin shipping-rule configuration, shopper checkout, and user-or-order-grant authorized order reads. Exact inventory quantities, reservations, ledgers, normalized contacts, and foreign customer/cart/order data are not public. Post-MVP coupons and provider execution, refunds, fulfillment, and event-administration endpoints remain absent.

## Verification evidence

Task 005 ran current checks on 2026-09-08 against Go 1.26.6 on Windows:

| Command/check | Result |
| --- | --- |
| `make fmt-check` | Passed; golangci-lint emitted a sandbox working-directory warning but returned success |
| `make lint` | Failed with 12 findings in unfinished Identity/platform code |
| `make test` | Passed for all packages; native Windows Make configuration does not enable the race detector |
| `make test-integration` | Passed after starting disposable PostgreSQL, Redis, and Kafka; migrations were already at version 2 |
| `make vulncheck` | Passed outside the restricted sandbox; no called-code vulnerabilities found |
| Local Markdown link validation | Passed |
| ADR identifier uniqueness | Passed with ADRs 001–004 |
| `git diff --check` | Passed |
| GitHub Actions YAML parse | Unavailable because no YAML parser or actionlint is installed; only a message path changed |

Task 004 revision 5 addresses the recorded lint findings. The task 005 table above remains historical system-wide evidence and is not presented as the current result.

Task 004 build-stage evidence on 2026-09-09: `make test` passed, `make lint` reported zero issues, `make fmt-check` passed, and `git diff --check` passed. A clean guarded test reset applied migrations 00001 and revised 00002, the full `make test-integration` suite passed, repeat migration was a no-op at version 2, and focused post-change tests passed for populated version-1 upgrade, sessionless onboarding/audit rollback, Store authorization, API isolation, and atomic Redis social consumption. This is development evidence, not the independent-review or final-verification gate.

Previous task 004 focused evidence also reports fresh disposable migrations, concurrency/provider fixtures, and repeated integration runs. Its independent reviewers returned changes required. Neither previous nor current passing checks replace final checks after task 004 is fixed, independently re-reviewed, and verified.

Task 004 fix-stage evidence on 2026-09-09: focused unit packages passed; migration status was already version 2; focused real PostgreSQL tests passed for active refresh concurrency, disabled-user concurrent refresh/revocation/reactivation, and stale social-session denial after user disablement or store suspension. Independent re-review confirmed the prior fixes but identified the two deferred medium findings above. This is not the final verification gate.

Task 004 consolidated MVP verification on 2026-09-09: full unit and integration suites passed before a tooling-only Makefile change and logger-only test cleanup; afterward, formatting, zero-issue lint, vulnerability scanning with no called-code vulnerabilities, and the affected Stores HTTP package test passed. Both reviewers approved the later changes, and the user approved reusing the unaffected full-suite evidence instead of repeating integration tests. Verification is accepted for continued MVP development, while the two deferred findings still prevent full workflow acceptance.

Task 007 final verification on 2026-09-10: `make verify` passed formatting, zero-issue lint, all unit tests, all integration tests against Goose schema version 2, and vulnerability scanning. The initial final scan exposed reachable `GO-2026-5676` in Gin's transitive QUIC dependency; `quic-go v0.59.1` and `qpack v0.6.0` cleared it, and both independent reviewers approved the final module graph. The clean scan reported zero vulnerabilities in called code or imported packages.

Task 008 final verification on 2026-09-10: both independent reviews passed with no findings, then `make verify` passed formatting, zero-issue lint, all unit tests, all integration tests against Goose schema version 3, and vulnerability scanning. The scan reported zero vulnerabilities in called code or imported packages. Focused PostgreSQL evidence also covered exact migration definitions, populated version-2 upgrade and repeat, tenant isolation, aggregate atomicity, hierarchy locking, and synchronized edit/archive races.

Task 009 final verification on 2026-09-11: both independent reviews passed after database-time, concurrency evidence, conflict mapping, and public-query amplification corrections. The complete rerun of `make verify` passed formatting, zero-issue lint, all unit tests, all integration tests against Goose schema version 4, and vulnerability scanning. The scan reported zero vulnerabilities in called code or imported packages.

Task 011 final verification on 2026-09-12: both independent reviews passed after materialization serialization, consistent exact reads, Catalog transaction-boundary cleanup, immutable-ledger hardening, duplicate-worker accounting, and expanded tenant/race/failure-injection coverage. The complete rerun of `make verify` passed formatting, zero-issue lint, all unit tests, all integration tests against Goose schema version 5, and vulnerability scanning with no vulnerabilities in called code.

Task 012 final verification on 2026-09-12: both independent reviews passed after snapshot-consistent cart responses, strict contact validation, and expanded persistence, migration, concurrency, isolation, authentication, and HTTP evidence. The complete `make verify` run passed formatting, zero-issue lint, all unit tests, all real integration tests at schema 6, and vulnerability scanning with no vulnerabilities in called or imported code.

Task 013 final verification on 2026-09-13: both independent reviews passed after consistent order/payment reads, all-or-nothing coordinated expiry, owner-complete events, retryable Shipping error mapping, migration-operability documentation, and expanded rollback, contention, grant, tenant, and schema evidence. The complete `make verify` run passed formatting, zero-issue lint, all unit tests, all real integration tests at schema 7, and vulnerability scanning with no vulnerabilities in called code.

## Required next work

1. Implement VietQR and MoMo provider execution and signed confirmation workflows.

Six roadmap slices remain after Checkout and orders before MVP production if each slice is delivered as one task. Coupons and redemption accounting follow the MVP production gate.

No production deployment should proceed until at least the required MVP slices and operational hardening pass their gates.
