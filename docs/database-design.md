# Database design

## Purpose and status

This document is the logical PostgreSQL design for the target MVP. It guides future migrations but does not imply that every table exists; see [development status](development-status.md) for the implemented schema.

PostgreSQL is authoritative for tenant identity, commerce, payments, idempotency, audit, and durable event handoff. Redis, Kafka, search indexes, and object storage are derived or ephemeral systems and cannot replace these records.

Runtime repositories use module-local GORM persistence models over an explicitly bounded `database/sql` pool. Goose SQL migrations alone define and version the physical schema; GORM automatic or programmatic schema mutation is prohibited. Explicit parameterized SQL remains appropriate for locks, guarded updates, complex tenant joins, and other behavior whose concurrency semantics must stay visible.

## Global conventions

### Tenant keys

- Every tenant-owned table has a non-null `store_id uuid`.
- The primary or candidate key used by another tenant table includes `store_id`.
- Tenant foreign keys include `store_id` on both sides so the database rejects cross-store relationships.
- Every tenant repository method requires `store_id`; no unscoped tenant query is exposed.
- Tenant query indexes begin with `store_id` unless a documented global worker query has a different leading predicate.
- Globally unique deployment records, such as store subdomains, are explicitly identified and kept outside tenant repositories.

Example relationship:

```sql
PRIMARY KEY (store_id, order_id)
FOREIGN KEY (store_id, order_id)
    REFERENCES orders (store_id, order_id)
```

Application scope remains mandatory even if PostgreSQL row-level security is later added. RLS is defense in depth only after transaction-scoped tenant settings and connection-pool reset behavior have dedicated tests.

Runtime repositories use module-local GORM persistence models and require explicit tenant predicates for every tenant-owned operation. GORM models describe mappings only: Goose remains authoritative for schema versioning, constraints, indexes, triggers, and data changes. Concurrency-sensitive locks and guarded mutations may use parameterized raw SQL through GORM when making the database behavior explicit is safer than implicit ORM behavior.

### Identifiers and versions

- Existing opaque UUID identifiers are retained. Changing identifier version or adding internal sequence keys requires measured index evidence and a migration ADR.
- Public order numbers are allocated per store and constrained by `(store_id, order_number)`.
- Aggregates that can receive concurrent commands use `version bigint NOT NULL` and guarded updates such as `WHERE store_id = $1 AND id = $2 AND version = $3`.
- External provider IDs are never assumed globally unique; uniqueness includes store and provider.

### Money, quantity, and time

- Money columns use `bigint` minor units plus `varchar(3)` ISO currency. No floating-point or unconstrained decimal money is used.
- The first release uses VND, whose normal minor-unit amount equals the displayed đồng amount, but currency remains persisted for history and validation.
- Physical-item quantities use `bigint` with non-negative checks. Fractional units are unsupported.
- Instants use `timestamptz` and are written in UTC. Business dates, if later needed, use explicit `date` plus store-timezone rules.
- Expiry decisions use database time inside the transaction to avoid process-clock disagreements.

### Text and JSON

- Canonical lookup values such as email, slug, SKU, barcode, provider, state, and references use normalized typed columns.
- User-facing Unicode text is normalized at the application boundary and length-constrained in both application and schema where practical.
- JSONB is limited to validated option values, small provider metadata, event payloads, and audit metadata. Fields used for joins, authorization, money, state, sorting, or common filters remain columns.
- Raw credentials, access tokens, and unnecessary full provider payloads are not persisted.

### Lifecycle and deletion

- Products, variants, categories, customers, addresses, and provider configurations use status or `archived_at` for removal from normal use.
- Orders, order lines, payment/refund records, inventory ledger, audit, outbox, and inbox records are append-only or state-transitioned and are not cascade-deleted with mutable source records.
- Cascades are allowed only for subordinate security/session records whose parent removal is itself explicitly authorized and compatible with retention requirements.
- Production retention periods must be configured from legal and operational requirements before launch. Until then, financial, order, and audit data are retained.

## Module-owned tables

Names are logical targets; migrations may introduce them incrementally by roadmap slice.

### Platform administration

The initial release stores no platform identity rows. Store provisioning is authorized by one deployment-managed bearer secret and records actor type `platform_admin` with identifier `deployment`. Individual platform accounts and sessions require a later design and migration.

### Stores

| Table | Purpose | Important constraints/indexes |
| --- | --- | --- |
| `stores` | Tenant identity, immutable subdomain, regional defaults, status | PK `store_id`; unique normalized subdomain; status/settings checks |
| `store_memberships` | Workforce role and status | PK `(store_id,user_id)`; tenant FK to users; active-owner concurrency rule |
| `store_staff_permissions` | Optional explicit staff permissions | PK `(store_id,user_id,permission)`; tenant FK to membership |

Subdomain lookup has a global unique index because routing occurs before tenant authentication. Normal store APIs still load by `store_id` after resolution.

### Identity

| Table | Purpose | Important constraints/indexes |
| --- | --- | --- |
| `users` | Store-owned account | PK `(store_id,user_id)`; unique partial `(store_id,canonical_email)` |
| `identity_password_credentials` | Adaptive password hash | PK/FK `(store_id,user_id)` |
| `identity_social_identities` | Google/Facebook link | PK `(store_id,provider,provider_subject)`; unique `(store_id,user_id,provider)` |
| `identity_sessions` | Absolute user-session lifecycle | PK `(store_id,session_id)`; `(store_id,user_id,created_at DESC)` |
| `identity_access_tokens` | Short-lived access-token digests | PK `(store_id,token_digest)`; session FK |
| `identity_refresh_tokens` | Rotating refresh-token digests | PK `(store_id,token_digest)`; session/expiry indexes |
| `identity_guests` | Store-bound anonymous principal | PK `(store_id,guest_id)`; expiry index for cleanup |
| `identity_guest_sessions` | Guest session lifecycle | same store-matching keys and rotation constraints as user sessions |
| `identity_guest_access_tokens` | Guest access-token digests | PK `(store_id,token_digest)` |
| `identity_guest_refresh_tokens` | Guest refresh-token digests | PK `(store_id,token_digest)` |

Token tables store fixed-length cryptographic digests, never bearer values. Cleanup by expiry uses bounded batches and does not infer authentication validity from cleanup completion.

### Catalog

| Table | Purpose | Important constraints/indexes |
| --- | --- | --- |
| `catalog_products` | Product content and lifecycle | PK `(store_id,product_id)`; unique `(store_id,slug)`; browse keyset index |
| `catalog_variants` | Sellable physical variation | PK `(store_id,variant_id)`; FK to product; unique `(store_id,sku)`; optional barcode uniqueness |
| `catalog_categories` | Store hierarchy | PK `(store_id,category_id)`; tenant parent FK; unique store slug |
| `catalog_product_categories` | Product/category relation | PK `(store_id,product_id,category_id)`; both tenant FKs |
| `catalog_media` | Ordered object-storage references | PK `(store_id,media_id)`; owner type/id validation; `(store_id,product_id,position)` where modeled directly |

Published-list queries use partial indexes such as `(store_id, published_at DESC, product_id DESC) WHERE status = 'published'`. List endpoints use keyset pagination; unrestricted offset scans are not part of the public contract.

### Pricing

| Table | Purpose | Important constraints/indexes |
| --- | --- | --- |
| `pricing_price_lists` | Eligibility, priority, VND currency, validity | PK `(store_id,price_list_id)`; active-window lookup index |
| `pricing_prices` | Variant amount in a price list | PK `(store_id,price_list_id,variant_id)`; variant resolution index |
| `pricing_coupons` | Post-MVP constrained fixed/percentage rule | PK `(store_id,coupon_id)`; unique `(store_id,canonical_code)` |
| `pricing_coupon_redemptions` | Post-MVP idempotent usage accounting | PK `(store_id,redemption_id)`; unique order/coupon application; customer usage index |

Amounts, minimums, and maximums are non-negative. Percentage uses an integer basis-point or similarly explicit scaled representation, never float.

### Inventory

| Table | Purpose | Important constraints/indexes |
| --- | --- | --- |
| `inventory_stock_items` | Variant on-hand state | PK `(store_id,variant_id)`; non-negative on-hand; version |
| `inventory_reservations` | Ten-minute order allocation | PK `(store_id,reservation_id)`; unique `(store_id,order_id,variant_id)`; active expiry index |
| `inventory_ledger_entries` | Immutable quantity audit | PK `(store_id,ledger_entry_id)`; variant/time and business-reference indexes |

`on_hand` is physical stock and active reservations reduce only computed availability. Reservation creation does not decrement `on_hand`; confirmation atomically decrements it, closes the active reservation, and appends an equal inventory-ledger delta. Reservation mutation locks the stock item or uses one guarded atomic update. The invariant is checked in the same transaction as reservation creation or confirmation.

Expiry workers use the active-reservation expiry index to discover a bounded set of distinct `(store_id,order_id)` candidates without retaining reservation locks. Each candidate is processed in its own transaction: claim the order row first with `FOR UPDATE SKIP LOCKED`, then lock payment, reservations ordered by reservation ID, and stock items ordered by variant ID. After acquiring all locks, recheck order/payment/reservation state and database time before expiring the order and active reservations without changing `on_hand`. Duplicate discovery is safe because a competing worker cannot claim the same order row, and no path acquires a reservation lock before the order lock.

### Customers

| Table | Purpose | Important constraints/indexes |
| --- | --- | --- |
| `customers` | Store customer profile | PK `(store_id,customer_id)`; optional unique user relationship; normalized contact lookup |
| `customer_addresses` | Mutable saved address | PK `(store_id,address_id)`; tenant FK to customer; active customer index |

Contact indexes are added only for supported store workflows. They do not grant access and must not enable a cross-store lookup.

### Carts

| Table | Purpose | Important constraints/indexes |
| --- | --- | --- |
| `carts` | User/guest shopping intent | PK `(store_id,cart_id)`; exactly one owner type; active owner index; expiry index |
| `cart_lines` | Requested variant quantities | PK `(store_id,cart_id,variant_id)`; positive quantity; tenant FKs |

One partial unique index may enforce one active cart per principal and currency if that becomes the API contract. Cart rows are not used as historical order data.

Successful checkout stores a deferred tenant-composite `converted_order_id` link while changing the cart to `converted`. The matching order-to-cart link is also deferred so fixture cleanup and future archival can preserve both records without weakening the atomic checkout invariant.

Migration 7 adds the nullable cart conversion column under an `ACCESS EXCLUSIVE` metadata lock and validates its tenant-composite foreign key against existing carts. The conversion check is `NOT VALID` to avoid a historical-table scan while still enforcing new writes.

### Orders

| Table | Purpose | Important constraints/indexes |
| --- | --- | --- |
| `orders` | Order header, state, totals, owner and version | PK `(store_id,order_id)`; unique `(store_id,order_number)`; owner/history keyset indexes |
| `order_lines` | Immutable product/price snapshots | PK `(store_id,order_id,line_id)`; tenant order FK; positive quantity and checked totals |
| `order_addresses` | Immutable billing/delivery snapshots | PK `(store_id,order_id,address_type)` |
| `order_guest_grants` | Digested guest order capability | PK `(store_id,token_digest)`; unique active grant policy; order FK |
| `order_idempotency` | Checkout request/result binding | PK `(store_id,idempotency_key)`; request fingerprint and order FK |
| `order_state_history` | Durable transition history | PK `(store_id,history_id)`; `(store_id,order_id,occurred_at)` |

MVP order totals use separate subtotal, shipping, VAT-inclusive display metadata, and grand-total columns with database checks for non-negative components and arithmetic consistency where expressible. The post-MVP coupon slice adds discount totals and line snapshots through an expand-compatible migration. Application domain rules remain authoritative for transition meaning.

Per-store `order_number_counters` allocate communication-friendly `ORD-` numbers transactionally. Inventory reservation `order_id` remains an opaque application reference because enforcing an Orders foreign key would break the existing standalone Inventory contract; checkout creates the order and reservations in one transaction through explicit module participants.

### Payments and refunds

| Table | Purpose | Important constraints/indexes |
| --- | --- | --- |
| `payment_provider_configs` | Enabled provider and encrypted settings | PK `(store_id,provider)`; key version; version; no plaintext secret |
| `payments` | Order-level payable amount and state | PK `(store_id,payment_id)`; tenant order FK; state/version indexes |
| `payment_attempts` | Provider/manual attempt and idempotency | PK `(store_id,attempt_id)`; unique `(store_id,provider,idempotency_key)`; provider reference index |
| `payment_webhook_receipts` | Validated/deduplicated callback receipt | PK `(store_id,provider,provider_event_id)`; body digest; processing/received index |
| `payment_reconciliation_items` | Late/mismatched/ambiguous funds | PK `(store_id,reconciliation_id)`; unresolved age index |
| `refunds` | Full-refund workflow | PK `(store_id,refund_id)`; payment FK; unique idempotency/provider references |

Webhook processing inserts or claims the receipt and applies the business effect in one PostgreSQL transaction. When the store cannot be trusted from the payload, a server-owned provider configuration/reference resolves the candidate store before tenant-scoped loading; all signed fields are still validated.

Provider configuration ciphertext contains algorithm, nonce, ciphertext, and key version in bounded binary/text columns. The encryption master or wrapping key remains outside PostgreSQL.

### Shipping

| Table | Purpose | Important constraints/indexes |
| --- | --- | --- |
| `shipping_rules` | Versioned free/flat quote rule | PK `(store_id,shipping_rule_id)`; one current active rule |
| `shipments` | Manual carrier/tracking lifecycle | PK `(store_id,shipment_id)`; tenant order FK; order/status index |

Order shipping snapshots do not depend on the current rule. Tracking numbers are identifiers, not secrets or access credentials.

### Audit

| Table | Purpose | Important constraints/indexes |
| --- | --- | --- |
| `audit_records` | Immutable privileged/financial action with actor type and identifier | PK `(store_id,audit_id)`; store/time and subject/time indexes |

### Eventing

| Table | Purpose | Important constraints/indexes |
| --- | --- | --- |
| `outbox_events` | Transactional integration facts | PK `(store_id,event_id)`; tenant aggregate/version uniqueness; unpublished claim index |
| `consumer_inbox` | Consumer-specific event deduplication | PK `(store_id,consumer_name,event_id)`; processed/retention index |

Eventing owns the outbox/inbox schema, repositories, publisher, and delivery-deduplication contract. A producing module owns its event semantics and appends through Eventing's transaction-aware interface. Every insert, lookup, claim, uniqueness rule, and aggregate reference includes the envelope's validated `store_id`; an `event_id` never establishes tenant scope by itself. The publisher does not update business aggregates.

## Critical transaction boundaries

### Tenant provisioning

Store, first user/password, owner membership, and platform-actor audit insert commit together. No owner session is created. A duplicate subdomain, credential failure, membership failure, or audit failure rolls back all records. Platform authentication occurs before this unit of work.

### Checkout

One transaction validates cart ownership, persists price/shipping/address snapshots, creates inventory reservations, creates order/payment/idempotency records, converts the cart, and writes outbox events. Provider calls start only after commit.

### Payment confirmation

Payments coordinates one PostgreSQL transaction for validated webhook success or audited manual confirmation. Competing confirmation, expiry, and cancellation workflows use one global lock order: order, payment, reservations ordered by reservation ID, then affected `inventory_stock_items` ordered by variant ID. After locking, the transaction reads database time and rechecks all states and deadlines. If the order remains payable and every reservation is active and unexpired, guarded stock updates decrement each stock item's `on_hand` by its reserved quantity, reservation confirmation and matching immutable ledger deltas record the consumption, and receipt/manual-command deduplication, payment success, order payment, audit where applicable, and outbox facts commit together. A missing row, insufficient `on_hand`, version conflict, or ledger failure rolls back every effect. If expiry or cancellation has committed, any reservation is no longer active, or the database deadline has passed, the same transaction records the received funds as reconciliation required and emits no payment-confirmed, order-paid, reservation-confirmed, or stock-changed fact. Concurrent inventory adjustment locks stock rows by the same variant ordering before its guarded update. No consumer completes these business transitions from an event.

### Cancellation and refund

Eligibility and order version are locked before transition. Unpaid cancellation and reservation release are atomic. Paid cancellation creates one full-refund record; provider work occurs after commit and later applies one durable result. Financial state is not marked refunded before settlement evidence.

## Query and index policy

- Define indexes from API, worker, and operational query shapes; do not index every column.
- Tenant lists use `(store_id, sort_value DESC, id DESC)` and opaque keyset cursors.
- Active-work queues use narrow partial indexes by state and next-attempt/expiry time.
- Foreign-key columns used for deletes, state transitions, or lookups receive matching indexes.
- Avoid wide JSONB GIN indexes until a measured query justifies their write and maintenance cost.
- Examine `EXPLAIN (ANALYZE, BUFFERS)` with realistic tenant skew before accepting a performance claim.
- Keep transactions short and avoid external calls while locks are held.
- Set statement, lock, and idle-in-transaction timeouts appropriate to request and worker workloads.
- Bound database connections across all replicas below PostgreSQL capacity; observe wait time before raising pool size.

## Partitioning, replicas, and tenant placement

Normal transactional tables begin unpartitioned. Premature partitioning complicates uniqueness, foreign keys, migrations, and operational recovery.

Consider time partitioning only for append-heavy `audit_records`, `outbox_events`, `consumer_inbox`, `inventory_ledger_entries`, or `payment_webhook_receipts` when measured table/index size, autovacuum lag, retention deletion, or query plans cause sustained operational harm. Before rollout, test partition creation, uniqueness semantics, pruning, backup/restore, retention, and worker queries.

Add read replicas only for stale-tolerant catalog, reporting, or projections. Authorization, checkout, inventory, order transitions, payment confirmation, and read-after-write paths use the primary unless an explicit consistency design says otherwise.

If one store becomes a sustained noisy neighbor, introduce a tenant-placement interface that maps `store_id` to a database placement. Migration requires dual-read prevention, write fencing, checksum/reconciliation, cutover, rollback/forward-fix procedure, and cross-placement observability. No repository may guess placement from a caller-controlled value.

## Migration policy

Migration 00002 backfills existing task-3 memberships with passwordless user rows and the `owner` role before adding the composite user foreign key and non-null role constraint. It leaves no role default; every later membership insert must state its role. The migration is still pre-release and may be edited only while confirmed undeployed outside disposable databases.

- Migrations are forward-only, versioned, and run before code that requires them.
- Prefer expand/backfill/validate/contract for populated tables.
- Add foreign keys or checks with a locking plan; use `NOT VALID` plus later validation where it materially reduces blocking.
- Create large indexes concurrently where supported by the migration runner and transaction policy.
- Backfills are restartable, bounded, observable, and scoped; ambiguous tenant data causes a controlled failure rather than guessed ownership.
- Each migration plan describes table size, lock risk, deployment order, compatibility window, and forward-fix recovery.
- Production migration and destructive database reset require explicit deployment/data authorization.

## Backup and recovery

- Use automated encrypted PostgreSQL backups with point-in-time recovery where the chosen service supports it.
- Back up object-storage metadata and ensure object versioning/lifecycle matches database retention.
- Redis and search/Kafka-derived projections are rebuilt from authoritative data or documented offsets; they are not the only copy of commerce state.
- Restore tests must verify tenant isolation, order/payment/ledger consistency, outbox continuity, secret decryptability for active key versions, and application startup.
- Operators set and verify concrete RPO/RTO before production. The current requirement is a basic single-region baseline, not an untested high-availability claim.

## Required database tests

- Cross-store read, update, delete, relationship, uniqueness, and credential denial for every tenant table.
- Same email, slug, SKU, order number, and provider reference can coexist in different stores.
- Concurrent last-unit reservation cannot oversell.
- Checkout and provisioning roll back on every required subordinate/audit/outbox failure.
- Reservation confirmation versus expiry and payment success versus cancellation are deterministic under concurrency.
- Duplicate checkout keys, webhooks, manual confirmations, refund requests, outbox publications, and consumer deliveries have one business effect.
- Archived catalog/customer/provider records remain referenced by immutable order/financial history without becoming sellable or usable.
- Migration tests cover empty and populated schemas, lock-sensitive paths, ambiguous tenant data, and repeat execution where supported.
