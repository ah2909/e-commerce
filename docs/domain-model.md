# Domain model

## Conventions

- Every tenant-owned aggregate carries `StoreID` and can be loaded only with that scope.
- Aggregate identifiers are opaque UUIDs. Human-facing order numbers are store-scoped references, not authorization credentials.
- `Money` is `{ amount_minor int64, currency ISO-4217 }`; values of different currencies cannot be combined.
- Physical-item quantity is a non-negative integer. Fractional inventory requires a future explicit unit policy.
- Timestamps are UTC instants. Store timezone affects display and business scheduling only.
- Mutable aggregates carry a version for optimistic concurrency where concurrent updates are meaningful.
- Archive status or `archived_at` removes mutable records from normal use without deleting historical references.

## Platform administration

### Platform administrator

The initial platform administrator is a deployment-scoped principal authenticated by one configured bearer secret. It is not a database account and remains separate from store users and memberships.

Rules:

- Only the authenticated platform principal may create a SaaS tenant.
- Tenant owners cannot satisfy a platform permission.
- Provisioning creates the store, initial tenant user/password, owner membership, and platform-actor audit record atomically without issuing an owner session.
- Individual operator accounts, platform sessions, activation, and suspension workflows are deferred.

## Store

### Store

Fields include identity, immutable subdomain, name, status, VND default currency, `vi-VN` locale, `Asia/Ho_Chi_Minh` timezone, and settings version. Shipping owns shipping rules.

### Membership

A membership binds one tenant user to the same store and a fixed `owner`, `admin`, or `staff` role. Permissions are explicit and deny by default.

Rules:

- At least one active owner must remain.
- Owner and admin can confirm VietQR and initiate refunds by default.
- Staff require explicit payment-confirmation and refund permissions.
- Role or status changes are audited and concurrency-safe.
- Store scope is selected before membership lookup.

## Identity

### User and credential

A user, password credential, social identity, session, access token, and refresh token belong to one store. Email and social-provider subject uniqueness are per store.

### Guest

A guest and its rotating session belong to one active store and cannot satisfy workforce permissions.

Rules:

- The same person may create unrelated accounts at multiple stores.
- Tokens are opaque, stored as digests, rotated on refresh, and rejected on another store host.
- Refresh-token reuse revokes or marks the session compromised.
- Disabled users and suspended stores cannot establish or refresh shopping sessions.
- Guest order access is a separate opaque grant; possession of a guest session does not expose every order matching an email or phone.

## Catalog

### Product

A product contains store, name, slug, description, status, category assignments, and media references. Status begins as `draft`, may become `published`, and ends as `archived`.

### Variant

A variant contains store, product, SKU, optional barcode, option values, weight, dimensions, status, and version. A product has at least one variant.

### Category and media reference

Categories provide a store-owned hierarchy. Media references point to object storage and contain ordering and accessibility metadata, not binary content.

Rules:

- Product slug and variant SKU are unique within a store, including archived records unless an explicit reuse policy is introduced.
- Barcode uniqueness is store-scoped when present.
- Weight and dimensions are non-negative and stored in documented base units.
- Core query fields remain typed columns; validated JSON may hold optional option values and non-indexed attributes.
- Publication requires a sellable active variant, valid required attributes, and at least one current price.
- Archiving prevents new sale but never changes an order snapshot.

## Pricing

### Price list and price

A price list defines store, VND currency, optional channel/customer scope, validity interval, priority, and status. A price assigns a non-negative amount to one variant.

### Coupon

Coupons are a post-MVP Pricing extension. A future coupon may be either a fixed VND amount or percentage with explicit validity, minimum subtotal, total usage limit, optional per-customer limit, and maximum discount where applicable.

Rules:

- Price selection is deterministic by eligibility, priority, and stable tie-breaker.
- Overlapping price lists that would remain ambiguous are rejected.
- MVP checkout recalculates authoritative item prices; client totals are hints only.
- Future discount evaluation rounds once at a documented component boundary using integer arithmetic and cannot make a line or order component negative.
- Storefront prices are VAT-inclusive for the first Vietnam-focused release; VAT invoice issuance is outside the MVP.

## Inventory

### Stock item

One stock item exists for each active variant at the store's single logical stock location. It records on-hand quantity and version.

### Reservation

A reservation binds quantity to a cart checkout/order, expires ten minutes after successful checkout creation, and has `active`, `confirmed`, `released`, or `expired` status.

### Stock ledger entry

The ledger is an immutable record of receipt, manual adjustment, reservation confirmation, cancellation return, or refund-related restock. Each entry has a reason, actor or source, business reference, quantity delta, and resulting quantity where needed for audit.

`available = on_hand - active_reserved`

Rules:

- Available quantity cannot become negative; backorders are unsupported.
- Reserve, confirm, release, and expire operations are atomic and idempotent.
- Confirmation decrements `on_hand` by the reserved quantity, closes the active reservation, and appends the matching ledger delta in the payment-confirmation transaction.
- Concurrent buyers cannot both reserve the last unit.
- Reservation expiry discovers bounded candidate orders without retaining reservation locks, claims one order first, acquires the shared global lock order, and then rechecks database time and state.
- A late payment does not reactivate an expired reservation.
- Manual adjustment requires a reason, permission, and audit record.

## Customer

A customer profile belongs to one store and may optionally reference a tenant user. Addresses are store-owned mutable records used to populate immutable order snapshots.

Rules:

- Guest checkout can create order contact/address snapshots without creating a registered user.
- Email and phone are normalized for store-local lookup but are not authorization credentials.
- A customer or address is archived rather than deleted when retained orders or financial records reference it.
- APIs minimize personal data and require purpose-specific authorization.

## Cart

A cart belongs to one store, one currency, and either a user or guest principal. Lines reference variants and requested integer quantities. States are `active`, `converted`, `expired`, and `abandoned`.

Rules:

- Cart ownership is immutable and store-bound.
- A cart contains only variants from its store.
- Cart totals are estimates and do not reserve stock.
- Checkout idempotently converts a cart once and persists the resulting order relationship.

## Order

### Order and line snapshots

An order contains store, public store-scoped order number, customer/guest relationship, currency, financial totals, contact and address snapshots, shipping snapshot, state, version, and timestamps. Each MVP line persists variant reference plus name, SKU, selected options, quantity, unit price, tax presentation, and total. Discount snapshots are added with the post-MVP coupon capability.

### Guest order grant

A guest order grant stores a digest, store, order, expiry/revocation state, and creation metadata. It permits only documented operations for that order.

Order lifecycle:

```text
pending_payment -> paid -> processing -> shipped -> completed
       |            |          |
       +-> expired  +----------+-> cancellation_requested -> refund_pending -> cancelled
       +-> cancelled
```

An implementation may use more internal states, but it must preserve these observable meanings.

Rules:

- Customers/guests may cancel only `pending_payment` orders they can access.
- Authorized staff may cancel before shipment. Paid cancellation requires a full refund before terminal `cancelled` financial completion.
- Invalid or concurrent transitions fail through a central state machine and version check.
- Totals equal persisted components under one integer rounding policy.
- Product, price, address, tax presentation, and shipping changes do not modify snapshots.
- Order number supports customer communication but never grants access.

## Payment

### Provider configuration

A store may enable VietQR, MoMo, or both. Configuration records provider, public settings, encrypted secret envelope, key version, status, and version. Reads redact secrets.

### Payment and attempt

A payment binds one order, amount, VND currency, provider, and state. An attempt records a store-scoped idempotency key, provider reference, request fingerprint, response classification, deadline, and lifecycle.

### Webhook receipt and reconciliation item

A webhook receipt stores provider event identity, body digest, validation result, processing state, and minimal redacted metadata. A reconciliation item explains late, mismatched, ambiguous, or manually observed funds and records resolution.

Payment lifecycle:

```text
pending -> action_required -> succeeded
   |             |              |
   +-> failed    +-> expired    +-> refund_pending -> refunded
                                  \-> reconciliation_required
```

Rules:

- One successful payment cannot exceed or duplicate the order's payable total.
- Store, order, amount, currency, merchant identity, and provider reference must agree before confirmation.
- Provider event IDs and idempotency keys use tenant-aware unique constraints.
- Duplicate callbacks acknowledge prior processing without duplicating effects.
- VietQR generation produces `action_required`; only an authorized audited confirmation produces success.
- MoMo success requires a verified provider response or raw-body signed webhook.
- Late, wrong-amount, or wrong-reference funds enter reconciliation and do not restore expired commerce state automatically.
- Confirmation locks and rechecks order, payment, reservations, and stock items in the global order using database time; it either consumes `on_hand` and commits all ledger/paid/confirmed states together or records received funds for reconciliation.

## Refund

A refund belongs to one successful payment and records the full refundable amount, reason, state, initiator, provider/external reference, and timestamps. MVP states are `pending`, `processing`, `succeeded`, `failed`, and `reconciliation_required`.

Rules:

- Only full refunds are supported initially.
- Refund initiation is authorized, audited, idempotent, and cannot exceed captured funds less prior successful refunds.
- MoMo refund execution uses the provider adapter with bounded retries and provider idempotency.
- VietQR refund settlement occurs outside the system and requires an audited manual confirmation.
- Order state changes only after the refund outcome required by its transition is durable.

## Shipping

A shipping rule provides free or flat-rate VND shipping. A shipment records order, status, carrier name, tracking number, and timestamps. Staff enter carrier and tracking information manually.

Rules:

- The checkout shipping charge and delivery address are snapshotted on the order.
- A shipment belongs to one order and store.
- Shipment cannot be marked shipped from a cancelled, expired, or unpaid order.
- Automated carrier quotations, labels, and callbacks are outside the MVP.

## Audit

Audit records are append-only and contain store, actor type/identifier, action, subject, request/correlation ID, minimal structured metadata, and UTC occurrence time.

Rules:

- A required audit write participates in the same transaction as the privileged state change.
- Audit metadata excludes credentials, token material, full provider bodies, and unnecessary personal data.
- Corrections append a new record; existing records are not edited.

## Cross-aggregate workflows

- Tenant provisioning: Platform administration coordinates Stores, Identity, membership, and Audit in one transaction.
- Checkout: Orders coordinates Carts, Catalog, Pricing, Inventory, Customers, Shipping, Payments, and outbox contracts in one transaction boundary where required.
- Payment confirmation: Payments coordinates one transaction across module-owned repositories. A valid on-time result atomically decrements physical `on_hand`, appends matching ledger entries, confirms reservations, and commits payment success, order payment, audit where required, deduplication, and outbox facts; an expiry/cancellation winner commits reconciliation instead.
- Cancellation/refund: Orders validates eligibility, Payments owns refund execution, Inventory owns release/restock, and Audit records privileged actions.

Detailed table ownership and constraints are in [database design](database-design.md); integration facts are in the [event catalog](event-catalog.md).
