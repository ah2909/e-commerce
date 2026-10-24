# Event catalog

## Contract rules

Events are versioned facts named in past tense. A producer writes an outbox row in the same PostgreSQL transaction as its state change. Kafka delivery and consumer processing are at least once.

Every tenant event envelope contains:

```json
{
  "event_id": "uuid",
  "event_type": "order.created.v1",
  "schema_version": 1,
  "occurred_at": "RFC3339 UTC timestamp",
  "store_id": "uuid",
  "aggregate_type": "order",
  "aggregate_id": "uuid",
  "aggregate_version": 1,
  "correlation_id": "opaque request or workflow identifier",
  "payload": {}
}
```

- Consumers deduplicate by `(store_id,consumer_name,event_id)` or an equally tenant-scoped unique business key in the same transaction as their effect. The validated envelope `store_id` must match the inbox row and affected business records.
- Ordering is guaranteed only where the chosen topic key and consumer design provide it. Consumers validate aggregate version/state and tolerate duplicates and unrelated reordering.
- Additive optional fields may be added compatibly. Removing, renaming, changing meaning/type, or newly requiring a field creates a new event version.
- Payloads contain the minimum identifiers and snapshot data needed by consumers. Credentials, token material, raw provider bodies, and unnecessary personal data are forbidden.
- Events do not authorize actions. Every consumer applies its own store scope and invariant checks.
- Exhausted retries enter a visible dead-letter or reconciliation state with alerting; they are never silently discarded.

## Store and identity events

| Event | Producer | Initial consumers | Required payload identifiers |
| --- | --- | --- | --- |
| `store.provisioned.v1` | Platform administration | Audit projection, provisioning | store, platform actor |
| `store.suspended.v1` | Platform administration | Session/reconciliation workers | store, platform actor, reason |
| `membership.changed.v1` | Stores | Notification, audit projection | store, user, role/status, tenant actor |

Tenant provisioning still writes its mandatory audit record synchronously. An event consumer is not a substitute for required audit atomicity.

## Catalog and pricing events

| Event | Producer | Initial consumers | Required payload identifiers |
| --- | --- | --- | --- |
| `product.published.v1` | Catalog | Search, analytics | store, product, version |
| `product.updated.v1` | Catalog | Search | store, product, version |
| `product.archived.v1` | Catalog | Search | store, product, version |
| `price.changed.v1` | Pricing | Cache invalidation, analytics | store, price list, variant, version |

Search consumers fetch authorized projections or receive only the minimal public catalog snapshot. They must not query Catalog tables directly from another module.

## Inventory events

| Event | Producer | Initial consumers | Required payload identifiers |
| --- | --- | --- | --- |
| `inventory.changed.v1` | Inventory | Analytics, low-stock notification | store, variant, ledger entry, reason |
| `inventory.reserved.v1` | Inventory | Operations metrics | store, reservation, order, variant, expiry |
| `inventory.reservation_expired.v1` | Inventory | Operations metrics | store, reservation, order, variant |
| `inventory.reservation_released.v1` | Inventory | Operations metrics | store, reservation, order, variant, reason |
| `inventory.reservation_confirmed.v1` | Inventory | Analytics | store, reservation, order, variant |

The checkout transaction persists the order and reservation state directly through declared application contracts. Events communicate committed facts; they do not make the initial checkout eventually consistent.

## Order and shipping events

| Event | Producer | Initial consumers | Required payload identifiers |
| --- | --- | --- | --- |
| `order.created.v1` | Orders | Email, analytics | store, order, customer/guest reference |
| `order.expired.v1` | Orders | Email, analytics | store, order, reason |
| `order.paid.v1` | Orders | Fulfillment, email, analytics | store, order, payment |
| `order.cancellation_requested.v1` | Orders | Payments, operations | store, order, reason, actor type |
| `order.cancelled.v1` | Orders | Email, analytics | store, order, reason |
| `shipment.prepared.v1` | Shipping | Operations | store, order, shipment, state |
| `shipment.updated.v1` | Shipping | Operations | store, order, shipment, state |
| `order.shipped.v1` | Shipping | Orders, email | store, order, shipment |
| `order.completed.v1` | Shipping | Email, analytics | store, order, shipment |

Email consumers receive a purpose-limited contact snapshot or retrieve it through an explicit Orders interface. General analytics events avoid delivery addresses, phone numbers, and email where identifiers suffice.

`order.shipped.v1` and `order.completed.v1` use the order as the envelope aggregate. Their required `order_id` must equal that aggregate identity; `shipment_id` remains a required related identifier.

## Payment and refund events

| Event | Producer | Initial consumers | Required payload identifiers |
| --- | --- | --- | --- |
| `payment.action_required.v1` | Payments | Storefront response/projection | store, order, payment, attempt, provider, expiry |
| `payment.confirmed.v1` | Payments | Email, analytics | store, order, payment, provider |
| `payment.failed.v1` | Payments | Orders, notification | store, order, payment, attempt, failure class |
| `payment.reconciliation_required.v1` | Payments | Operations notification | store, payment/attempt, reason |
| `refund.requested.v1` | Payments | Refund worker, operations | store, order, payment, refund |
| `refund.succeeded.v1` | Payments | Orders, Inventory policy, email | store, order, payment, refund |
| `refund.failed.v1` | Payments | Operations notification | store, order, payment, refund, failure class |

`payment.confirmed.v1`, `order.paid.v1`, and `inventory.reservation_confirmed.v1` describe business states already committed together by the payment-confirmation transaction. Their consumers do not transition Orders or Inventory. A duplicate webhook or manual command reuses the existing outcome and does not emit a second business transition. Late, expired, cancelled, or mismatched funds emit reconciliation only.

`refund.succeeded.v1`, `order.cancelled.v1`, and refund-restock inventory facts describe states already committed by the refund-application transaction. Consumers must not repeat the payment transition, order transition, or stock mutation.

## Publication and consumption

### Outbox publisher

- Claims unpublished rows in bounded batches with concurrent-worker-safe locking.
- Uses `(store_id,event_id)` as the producer idempotency identity when the Kafka client supports composite identities; otherwise it encodes both values into the producer key.
- Records attempts, next-attempt time, last failure class, and publication metadata.
- Retries transient broker errors with bounded exponential backoff and jitter.
- Exposes oldest unpublished age, retry count, and terminal failures.

The implemented routing uses `commerce.catalog.v1`, `commerce.inventory.v1`, `commerce.orders.v1`, `commerce.payments.v1`, and `commerce.shipping.v1`. The allowlisted event type selects the topic; callers cannot supply a topic. Messages use `store_id:event_id` as the key, so consumers must tolerate aggregate reordering and use current state or aggregate versions where order matters.

PostgreSQL cannot atomically commit Kafka acknowledgement with its transaction. A crash after publish and before marking may republish the event; consumer idempotency is mandatory.

### Consumers

- Validate the envelope, event-specific required/allowed payload fields, aggregate/payload identity, deterministic topic, composite key, and bounded headers before processing.
- Apply inbox insert and business effect atomically when both use PostgreSQL.
- Acknowledge Kafka only after the durable effect or durable terminal classification.
- Retry transient dependency failures; reject malformed/permanently unsupported messages to an observable dead-letter path.
- Propagate correlation and causation identifiers to new outbox events without creating infinite cycles.

The initial consumer groups are `search-projection-v1`, `analytics-projection-v1`, and `email-intents-v1`. Search contains public Catalog fields only, analytics stores tenant/type/day counts only, and email processing stops at a durable purpose-limited intent until a production sender is selected. Operational behavior and exact requeue commands are documented in [event worker operations](event-operations.md).

## Retention and privacy

- Retain published outbox and processed inbox rows for at least 30 days, Kafka topics for the same replay floor, and resolved dead letters for 90 days. Unresolved/terminal rows remain indefinite; use the incident hold and bounded cleanup in the [operations runbook](operations-runbook.md).
- Partition only when measured table/index maintenance or retention deletion justifies it, following [database design](database-design.md).
- Redaction is part of schema review. Encrypting a topic does not justify publishing secrets or unnecessary personal data.
- Only fully contract-valid envelopes may be retained for replay; every other terminal record stores broker coordinates, a digest, and bounded diagnostics only.
