# System features

Last reviewed: 2026-10-24.

This is the current feature inventory for the e-commerce backend, including its storefront/admin APIs, workers, and operator tools. Frontend screens are maintained in the sibling `e-commerce-ui` repository and are not inventoried here. Features described as implemented exist in this checkout; that status does not certify production readiness or claim that tests were rerun for this inventory.

## Status and ownership

- **Implemented:** behavior exists in code and, where applicable, the API contract.
- **Partial:** a useful part exists, but a delivery path or user-facing surface is missing.
- **Validation pending:** implementation exists but requires live-provider or deployment evidence.
- **Planned:** remaining work explicitly tracked in [TASKS.md](TASKS.md).
- **Deferred:** outside the current MVP scope; no implementation commitment.

This file describes capabilities and limits. [TASKS.md](TASKS.md) owns unfinished work and risk decisions; [product requirements](docs/product-requirements.md) owns intended scope; [OpenAPI](api/openapi.json) owns exact request/response contracts. Required migrations and configuration must be present for a feature to be available.

## Stores, storefront configuration, and access

| Feature | Status | Current behavior and limits |
| --- | --- | --- |
| Independent stores | Implemented | Store-owned accounts, catalogs, carts, orders, and payments use explicit tenant scope; identifiers can be reused across stores. |
| SaaS host routing | Implemented | Immutable store subdomains select tenant scope before authentication; forwarded hosts and payload fields cannot select a tenant. |
| Customer-managed routing | Implemented | Exact configured single-store host mapping or multi-store subdomain routing. |
| Store provisioning | Implemented | Deployment platform token creates a store, initial owner credentials, membership, and audit atomically; no owner session is issued. |
| Store information | Implemented | Authenticated store read with Vietnamese regional defaults and VND currency. |
| Public storefront configuration | Implemented | Public display name, tagline, logo/hero HTTPS URLs, hero copy, announcement, and `home`, `fashion`, or `sport` preset; defaults when no appearance is saved. |
| Storefront administration | Implemented | Permissioned appearance reads and versioned updates with audit; URL references do not provide an upload service. |
| Password accounts | Implemented | Store-local registration, login, current-user lookup, and Argon2id password verification. |
| User sessions | Implemented | Opaque access/refresh tokens, rotation, refresh-reuse handling, and current-session logout. Deferred expiry/suspension risks remain in TASKS.md. |
| Guest sessions | Implemented | Create, inspect, refresh, and log out store-bound guest sessions. |
| Google and Facebook accounts | Validation pending | Configurable social login and linking, callback state, browser binding, and one-time code exchange; live interoperability remains unverified. |
| Workforce management | Implemented | List/add memberships and update role/status with fixed owner/admin/staff roles and protection of the last active owner. |
| Explicit permissions | Implemented | Deny-by-default store access plus explicit staff payment, reconciliation, cancellation, refund, and fulfillment grants. |
| Authentication protections | Implemented | Credential/client throttling, bounded password work, trusted-proxy client-address resolution, and exact-origin CORS configuration. |

Sources: [Stores](internal/stores/), [Identity](internal/identity/), [provisioning](internal/onboarding/), and [platform security](internal/platform/).

## Catalog, prices, and inventory

| Feature | Status | Current behavior and limits |
| --- | --- | --- |
| Product authoring | Implemented | Create, read, replace, list, publish, and archive physical products. |
| Variants and media references | Implemented | SKU/barcode, option values, weight/dimensions, and ordered product-media references managed with products; no media-upload endpoint. |
| Category management | Implemented | Create, list, replace, and archive categories with hierarchy and product assignments. |
| Public product browsing | Implemented | Published product list and slug detail with current variant prices, media, categories, and boolean stock availability; exact quantities stay private. |
| Catalog pagination | Implemented | Bounded cursor-based listing for catalog reads. |
| Price-list management | Implemented | Create, list, read, replace, and archive store price lists and their variant prices. |
| Authoritative VND pricing | Implemented | Integer minor units, deterministic current-price resolution, VAT-inclusive presentation, and price checks before publication/checkout. |
| Stock administration | Implemented | Permissioned exact stock list/detail and idempotent manual adjustments at one logical location per store. |
| Inventory ledger | Implemented | Auditable immutable stock changes and atomic guards against overselling. |
| Inventory reservations | Implemented | Ten-minute checkout reservations; payment consumes stock, cancellation/expiry releases availability, and repeated terminal actions are safe. |
| Expiry processing | Implemented | Bounded background order/reservation expiry coordinated with payment and stock state. |

Sources: [Catalog](internal/catalog/), [Pricing](internal/pricing/), and [Inventory](internal/inventory/).

## Customers, carts, and orders

| Feature | Status | Current behavior and limits |
| --- | --- | --- |
| Customer profile | Implemented | Registered customers read/update their store-owned name, email, and phone. |
| Saved addresses | Implemented | Registered customers create, list, read, replace, and archive Vietnamese delivery addresses; district is optional. |
| User and guest carts | Implemented | Create/get the current cart, set/remove variant lines, abandon the cart, and expire inactive carts. |
| Cart estimates | Implemented | Current catalog, price, and availability estimates with versioned mutations; estimates do not guarantee checkout totals. |
| Shipping rules | Implemented | Permissioned configuration of one store's free/flat-rate VND shipping rule and checkout shipping snapshots. |
| Atomic checkout | Implemented | Revalidate ownership, address, sellable items, authoritative prices, shipping, and stock; persist order, reservation, pending payment, and outbox together. |
| Checkout retries | Implemented | Store-scoped idempotency returns the original order for the same accepted command. |
| Historical order snapshots | Implemented | Product, variant, price, contact/address, shipping, and totals remain independent of later source changes. |
| Shopper order access | Implemented | Registered-owner or opaque guest order-grant authorization; order numbers alone do not grant access. |
| Unpaid cancellation | Implemented | Owning user/guest can cancel an unpaid order with coordinated reservation release. |
| Workforce cancellation | Implemented | Permissioned cancellation before fulfillment; paid cancellation enters the full-refund workflow. |
| Order lifecycle | Implemented | Central transition checks and optimistic/idempotent commands protect order state. |

Sources: [Customers](internal/customers/), [Carts](internal/carts/), [Orders](internal/orders/), and [Shipping](internal/shipping/).

## Payments, refunds, and fulfillment

| Feature | Status | Current behavior and limits |
| --- | --- | --- |
| Tenant payment configuration | Implemented | Permissioned VietQR/MoMo configuration with encrypted credentials/snapshots and redacted reads. |
| VietQR instructions | Implemented | Local payment instruction/QR generation with expected amount and transfer reference; generating a QR does not confirm payment. |
| VietQR confirmation | Implemented | Permissioned audited manual confirmation atomically settles payment, order, and reserved stock with replay protection. |
| MoMo payment attempts | Validation pending | Signed bounded provider requests, idempotent attempts, and shopper payment initiation; live sandbox acceptance remains open. |
| MoMo signed callbacks | Validation pending | Raw-body signature checks, callback routing, deduplicated receipts, atomic settlement, and mismatch/replay handling; live signed sandbox callback remains unverified. |
| Payment reconciliation | Implemented | Permissioned queue for late, expired, mismatched, or otherwise unresolved payments; funds do not automatically revive expired orders. |
| Full refunds | Implemented | Permissioned refund reads and full-refund state transitions with atomic inventory restock when settled; partial refunds are unavailable. |
| MoMo refunds | Validation pending | Manual API execution invokes the provider refund/query adapter with idempotent result handling; live provider evidence remains required. |
| VietQR refunds | Implemented | Permissioned recording of externally completed settlement; the backend does not initiate a bank transfer. |
| Fulfillment administration | Implemented | Permissioned order queue/detail, one shipment per order, carrier/tracking preparation and correction, dispatch, and completion. |
| Shopper shipment information | Implemented | Authorized order reads include protected shipment/tracking projections. Automated carriers and partial shipments are unavailable. |

Sources: [Payments](internal/payments/), [Orders](internal/orders/), and [Shipping](internal/shipping/). Live acceptance and release prerequisites remain in [TASKS.md](TASKS.md).

## Workers and derived integrations

| Feature | Status | Current behavior and limits |
| --- | --- | --- |
| Transactional outbox | Implemented | Facts commit with business state; leased concurrent workers publish to five fixed Kafka topics with bounded retries and terminal handling. Kafka does not gate checkout commit. |
| Consumer deduplication | Implemented | Validated tenant/event envelopes, transactional inbox/effect writes, and Kafka offset acknowledgement after durable processing. |
| Event recovery | Implemented | Redacted status/list commands, terminal outbox requeue, and validated dead-letter replay through `event-reconcile`. |
| Search projection | Partial | Rebuildable public-catalog documents exist; no public search API. |
| Analytics projection | Partial | Tenant/event/UTC-day counts exist; no analytics reporting API or business dashboard. |
| Email notifications | Partial | Durable purpose-limited email intents exist; no external sender or delivered-email guarantee. |

Sources: [Eventing](internal/eventing/), [Search](internal/search/), [Analytics](internal/analytics/), [Notifications](internal/notifications/), and [event operations](docs/event-operations.md).

## Operations and platform capabilities

| Feature | Status | Current behavior and limits |
| --- | --- | --- |
| Audit trail | Implemented | Transactional privileged/financial action records; no general audit-browser API. |
| Health checks | Implemented | Liveness/readiness endpoints, container probe command, and graceful process shutdown. |
| Telemetry | Implemented | Internal metrics listener, bounded labels, trace propagation/export, SQL-pool/worker signals, and example dashboards/alerts. |
| Production configuration checks | Implemented | Fail-closed validation of routing, secrets, transport security, dependency timeouts, and connection budgets; `config-check` operator command. |
| Database migrations | Implemented | Forward-only Goose migrations through 00015; guarded integration-test database tooling. |
| Payment-key rotation | Implemented | Key-reference inventory, bounded re-encryption, and fenced retirement verification via `payment-key-rotate`. |
| Event retention | Implemented | Report/cleanup commands, bounded eligible deletion, retention floors, and incident hold; financial/audit records remain excluded. |
| Local runtime and checks | Implemented | Docker Compose API/worker/dependencies, containerized checks, formatting/lint/unit/integration/vulnerability targets, and CI. |
| Release security tooling | Implemented | Container build, SBOM, vulnerability/secret scans, and disposable ZAP baseline targets; available commands do not imply passing release evidence. |
| Backup/restore tooling | Validation pending | Encrypted backup and guarded restore-drill scripts exist; selected-environment PITR, invariant/decryption/readiness checks, and measured RPO/RTO remain required. |
| Production deployment | Validation pending | Image/deployment examples and runbooks exist; ingress, network policy, broker security, paging, rollout rehearsal, and launch approval remain open. |
| Capacity certification | Planned | No checked-in versioned load scenario or measured production envelope; fixtures, scenario, hardware/traffic assumptions, and evidence remain required. |

Sources: [Audit](internal/audit/), [platform](internal/platform/), [commands](cmd/), [operator scripts](ops/), [deployment](deploy/), [Makefile](Makefile), and [operations runbook](docs/operations-runbook.md).

## Planned and deferred capabilities

| Feature | Status | Scope |
| --- | --- | --- |
| Coupons and discount redemption | Planned | After the MVP production gate: fixed/percentage discounts, eligibility, integer rounding, usage limits, idempotent redemption, and order discount snapshots. |
| Account recovery and stronger sign-in | Deferred | Password reset/change, email verification, MFA, and forced initial-password change. |
| Platform administration workflows | Deferred | Individual platform accounts, invitations, tenant activation/suspension management, self-service provisioning, and SaaS subscription billing. |
| Advanced commerce | Deferred | Multi-warehouse/backorders, partial payment/refund/fulfillment, automated carriers, multi-country tax/VAT invoice issuance, and advanced promotions. |
| Broader product scope | Deferred | Marketplaces/payouts, digital goods, subscriptions, B2B, custom tenant domains, and cross-store account linking. |

## Keeping this inventory current

Update the relevant rows in the same change that adds, changes, deprecates, or removes a feature, including API, worker, integration, operator-tool, permission, and configuration behavior. Keep entries grouped by user capability; do not append a chronological change log or duplicate endpoint schemas here.

Check the implementation and OpenAPI before changing a status. Record external/live validation gaps explicitly and link unfinished actions to TASKS.md. Update the review date when the inventory is reviewed; never infer production readiness from an implemented status or a newly written test. If a code change affects no listed capability, no inventory edit is required.
