# Product requirements

## Purpose

Provide a reusable commerce backend for independent Vietnamese retailers. The same application supports a shared SaaS deployment, where stores are isolated by subdomain, and customer-managed deployments operated by a customer or implementation partner.

This document defines intended behavior. [Remaining work](../TASKS.md) tracks outstanding implementation and release gaps.

## Product principles

- A store is one independent business and never shares tenant-owned data with another store.
- Correctness of money, inventory, orders, and payments takes priority over feature breadth.
- Storefronts and admin applications consume stable APIs; the backend does not own a particular UI.
- Start with operationally simple components and add partitioning, tenant placement, or service extraction only from measured need.
- Historical commerce and audit records are retained; mutable source records are archived rather than physically deleted.

## Actors

- Platform administrator: provisions stores in a SaaS or customer-managed deployment.
- Store owner: configures one store and controls its workforce.
- Store administrator: manages permitted store operations, including payment confirmation and refunds.
- Store staff: manages explicitly permitted catalog, inventory, order, and payment operations.
- Registered customer: shops within one store using a store-owned account.
- Guest: shops without registering and accesses an order through a store-bound opaque credential.
- Integration: MoMo, email, analytics, search, or a future shipping provider.

## Tenant and deployment model

- In SaaS mode, one validated subdomain selects one store before authentication.
- Only an authenticated platform administrator can create a store. Store onboarding is not public or self-service.
- Store owners and staff cannot create tenants.
- Accounts, customers, staff memberships, catalogs, inventories, carts, orders, payments, provider configuration, and audit records belong to exactly one store.
- The same person may independently register the same email or social identity at different stores; no account or data is linked across stores.
- A customer-managed deployment uses the same binaries and modules. In single-store mode, one exact configured tenant host maps server-side to one configured `store_id`; in multi-store mode, immutable subdomains resolve beneath a configured base domain. Platform/authentication hosts remain distinct, and caller fields never select a store.
- Custom storefront domains and SaaS subscription billing are not part of the MVP.

## MVP capabilities

### Store and access management

- Platform-admin store provisioning with an initial owner account; activation and suspension workflows are deferred.
- Immutable SaaS subdomain, store name, VND currency, `vi-VN` locale, and `Asia/Ho_Chi_Minh` timezone defaults.
- Store-owned registered accounts and store-bound guest sessions.
- Fixed `owner`, `admin`, and `staff` workforce roles with deny-by-default permissions.
- Owner and admin payment-confirmation and refund permissions by default; staff receive them only through an explicit permission.
- Audit records for tenant provisioning, membership, provider configuration, price override, inventory adjustment, payment confirmation, cancellation, and refund actions.

### Catalog and pricing

- Physical products with categories, descriptions, multiple media references, and archive/publish status.
- Variants with SKU, barcode, option values such as size and color, weight, dimensions, and independent availability.
- Store-scoped price lists and VND prices represented as integer minor units.
- VAT-inclusive storefront prices for the first Vietnam-focused release. VAT invoice issuance is deferred.

### Inventory

- One logical stock location per store.
- Variant-level on-hand stock, immutable adjustment ledger, and atomic reservations.
- No backorders: available stock may not become negative.
- Checkout reserves inventory for ten minutes.
- Payment confirmation consumes the reserved physical stock and closes the reservation atomically; expiration or cancellation releases the reservation idempotently without changing `on_hand`.

### Customer, cart, and checkout

- Registered and guest checkout.
- Store-scoped customer profiles, Vietnamese delivery addresses, email, and phone number.
- Mutable carts with estimated prices; checkout always recalculates authoritative prices and availability.
- Flat-rate or free store-configured shipping for the MVP.
- Order snapshots for product name, SKU, option values, price, address, shipping charge, and applicable tax presentation.
- A guest receives a high-entropy, store-bound order-access credential. Public order numbers are never sufficient for access.

### Orders and fulfillment

- Controlled order, payment, reservation, refund, and fulfillment state transitions.
- Customers and guests may cancel before payment.
- Authorized staff may cancel before fulfillment.
- Cancelling a paid order creates a full-refund workflow rather than silently reversing financial state.
- Store staff record the carrier and tracking number manually.
- Partial fulfillment, partial cancellation, partial refunds, and automated carrier integration are deferred.

### Payments

- Each store may enable both VietQR and MoMo and let the shopper choose.
- Store payment settings and credentials are tenant-scoped, encrypted at rest, redacted from reads, and accessible only to authorized configuration and adapter paths.
- VietQR generates a QR code and never treats generation as payment. Authorized staff verify the bank account and record an audited approval.
- MoMo confirms payment only from a valid, idempotently processed provider response or signed webhook.
- A payment received after reservation expiry, with a wrong amount, or with a wrong reference enters reconciliation. It does not automatically restore an expired order.
- Full refunds are supported. VietQR refunds are externally settled and then recorded by authorized staff; MoMo refunds use its adapter and reconcile provider results idempotently.

### Asynchronous processing

- Transactional outbox publication for email, search, analytics, and integration events.
- At-least-once delivery with idempotent consumers and consumer inbox records where business effects require them.
- Kafka and external provider availability are not required to commit checkout.

## Explicitly out of scope for MVP

- Multi-vendor marketplace and seller payouts
- Digital products, services, subscriptions, and B2B/wholesale rules
- Multiple warehouses, transfers, fulfillment optimization, and backorders
- Advanced promotion-rule language
- Partial payments, partial refunds, and partial fulfillment
- Automated shipping-provider integration
- Coupons, promotional discounts, and redemption accounting
- VAT invoice issuance and general multi-country tax calculation
- Custom tenant domains and cross-store account linking
- Self-service tenant creation and SaaS subscription billing
- Recommendations, event sourcing, or microservice deployment

## Quality attributes

- Isolation: cross-store access fails at routing, authorization, repository, constraint, cache, job, and event boundaries.
- Correctness: transactions and constraints preserve money, inventory, order, payment, and refund invariants under concurrency and retries.
- Reliability: external calls use deadlines; callbacks and asynchronous work tolerate duplicate, delayed, and out-of-order delivery.
- Security: credentials and personal data are minimized, protected, and excluded from logs and events.
- Auditability: privileged and financial changes can be attributed and reconstructed.
- Maintainability: one module owns each rule and table, and module interactions use explicit contracts.
- Operability: structured logs, metrics, traces, health checks, backups, and recovery procedures support diagnosis.
- Scalability: stateless processes scale horizontally, database access is query- and tenant-oriented, and advanced data placement follows measured thresholds.

## MVP success criteria

- Two stores can independently use the same email, product slug, SKU, order number, and provider-side reference without resolving each other's data.
- A platform administrator can provision a store, while an unauthenticated user or tenant administrator cannot.
- A guest or registered customer can complete checkout without overselling the last unit.
- An unpaid reservation expires after ten minutes and restores availability exactly once.
- Repeated MoMo callbacks and repeated VietQR staff actions never duplicate order, inventory, ledger, or refund effects.
- Late or mismatched transfers are visible for reconciliation without reviving expired orders automatically.
- Catalog, price, address, or shipping changes do not modify an existing order.
- Automated tests prove cross-store denial for every tenant-owned resource.
