# Domain Model

## Shared value objects

- `StoreID`, `UserID`, and aggregate-specific typed identifiers
- `Money { AmountMinor int64, Currency string }`
- `Quantity` with explicit unit/precision policy
- `Email`, `Address`, and UTC timestamps
- `IdempotencyKey`

## Store aggregate

- Store: identity, status, default currency, locale, timezone, and settings version.
- Membership: user-to-store relationship and role assignments.
- Role/Permission: store-scoped authorization.

Rules:

- Suspended stores cannot accept new checkout operations.
- Store default currency changes do not rewrite existing prices or orders.

## Catalog aggregate

- Product: store, name, slug, description, status, product type, and attributes.
- Variant: SKU, option values, barcode, physical properties, and status.
- Category: store-owned hierarchy/classification.
- Media reference: metadata pointing to object storage.

Rules:

- Product slug and variant SKU are unique within a store.
- Important query fields remain typed columns; optional product-type attributes may use validated JSON.
- Publishing requires at least one sellable variant and valid required attributes.

## Pricing aggregate

- PriceList: store, currency, channel/customer scope, validity window, priority.
- Price: variant, amount, validity window.
- Coupon/Promotion: constrained MVP discount definition and usage policy.

Rules:

- Money currencies cannot be combined without explicit conversion.
- Price selection is deterministic when multiple lists match.
- Checkout recomputes authoritative totals; client totals are never trusted.

## Inventory aggregate

- StockItem: store, variant, on-hand quantity, version.
- Reservation: cart/order reference, quantity, status, expiration.
- StockLedgerEntry: immutable reasoned adjustment.

Core relationship:

`available = on_hand - active_reserved`

Rules:

- Availability never becomes negative unless an explicit backorder policy permits it.
- Reservation, confirmation, release, and expiry are idempotent.
- Manual adjustments require a reason and audit record.

## Cart aggregate

- Cart: store, optional customer, currency, status, expiration.
- CartLine: variant, requested quantity, and optional presentation snapshot.

Rules:

- A cart belongs to exactly one store and currency.
- Cart totals are estimates; checkout calculates final authoritative totals.

## Order aggregate

- Order: store, customer reference, number, state, currency, totals, snapshots, and version.
- OrderLine: variant reference plus immutable name, SKU, quantity, unit price, discounts, tax, and total snapshots.
- OrderAddress: immutable billing/shipping snapshot.

Initial states:

`pending_payment -> paid -> processing -> shipped -> completed`

Exceptional transitions include `cancelled`, `payment_failed`, and later `refunded` with explicit guards.

Rules:

- Order totals equal the sum of persisted components according to one documented rounding policy.
- State transitions use optimistic concurrency or equivalent locking.
- Catalog edits never modify order snapshots.
- Order number uniqueness is store-scoped.

## Payment aggregate

- Payment: store, order, provider, amount, currency, state.
- PaymentAttempt: provider request/response references and idempotency key.
- WebhookReceipt: provider event ID, signature validation result, processing state.

Rules:

- Provider event IDs and idempotency keys have appropriate unique constraints.
- Duplicate callbacks return success after confirming prior processing.
- A successful capture cannot silently exceed the authorized/order amount.

## Relationships that require explicit policy later

- Guest checkout versus required customer account
- Tax calculation source and rounding
- Reservation expiry duration
- Partial payment/refund/fulfillment
- Multi-currency presentation and conversion
- Product deletion/archival retention
- Data retention and deletion obligations
