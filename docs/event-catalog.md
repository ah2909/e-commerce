# Event Catalog

Events are versioned facts, not commands. Names use past tense.

| Event | Producer | Initial consumers | Required identifiers |
| --- | --- | --- | --- |
| `store.created.v1` | Stores | Audit, provisioning | store, actor |
| `product.published.v1` | Catalog | Search, analytics | store, product |
| `product.updated.v1` | Catalog | Search | store, product |
| `inventory.changed.v1` | Inventory | Analytics, low-stock notification | store, variant, ledger entry |
| `order.created.v1` | Orders | Email, analytics | store, order, customer reference |
| `order.paid.v1` | Orders/Payments contract | Fulfillment, email | store, order, payment |
| `order.cancelled.v1` | Orders | Inventory release, email | store, order, reason |
| `payment.failed.v1` | Payments | Orders, notification | store, order, payment attempt |

## Event rules

- Consumers deduplicate by `event_id` or a consumer-specific inbox record.
- Payload changes that break consumers require a new schema version.
- Consumers must tolerate unknown additive fields.
- Store IDs are mandatory for tenant-owned events.
- Emit the minimum personal data necessary.
- Retrying an event must not duplicate business effects.
