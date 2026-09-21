# Event worker operations

## Runtime model

The worker publishes committed `outbox_events` to Kafka and runs three independent consumer groups. PostgreSQL remains authoritative; Kafka availability never gates API writes or checkout.

| Topic | Event families |
| --- | --- |
| `commerce.catalog.v1` | Catalog and Pricing |
| `commerce.inventory.v1` | Inventory |
| `commerce.orders.v1` | Orders |
| `commerce.payments.v1` | Payments and refunds |
| `commerce.shipping.v1` | Shipment preparation and correction |

Messages use `store_id:event_id` as the key. Consumers require the deterministic topic, key, bounded diagnostic headers, aggregate type, and event-specific payload schema to agree before dispatch. Undeclared or mistyped payload fields are digest-only dead letters and are not replayable. Delivery is at least once, so every PostgreSQL consumer commits its tenant-scoped inbox row and derived effect in one transaction before committing the Kafka offset.

The included consumers are:

- `search-projection-v1`, which stores only public Catalog fields and has no public search API yet.
- `analytics-projection-v1`, which stores tenant/event/UTC-day counts without event payload or personal data.
- `email-intents-v1`, which stores purpose-limited pending email intents from immutable order contact snapshots. No external email delivery is claimed.

## Configuration

`KAFKA_BROKERS` is a required comma-separated broker list. Production also requires TLS and paired SASL credentials. Event worker limits use:

| Variable | Default | Constraint |
| --- | --- | --- |
| `EVENT_BATCH` | `50` | 1–500 |
| `EVENT_POLL` | `1s` | positive, at most 1 minute |
| `EVENT_CLAIM_LEASE` | `30s` | positive, at most 10 minutes |
| `EVENT_MAX_ATTEMPTS` | `12` | 1–100 |
| `EVENT_RETRY_BASE` | `1s` | positive, at most 1 minute |
| `EVENT_RETRY_MAX` | `5m` | at least the base and at most 24 hours |

Local Compose creates the five fixed topics with three partitions and one replica. Production operators set replication and ACLs for the selected cluster and configure every event topic with a replay floor of 30 days.

## Failure and recovery

Publishers claim due rows in short PostgreSQL transactions with expiring lease tokens, publish outside the transaction, and condition completion on the token. A crash after Kafka acknowledgement but before the database mark can republish; consumer inboxes make that safe.

Each claimed record receives an independent bounded broker deadline. Leasing alone does not increment its attempt count, and cancellation releases untouched claims, so a slow record cannot exhaust later records in the batch.

Retryable publication failures use bounded exponential backoff with deterministic jitter. Unsupported contracts, permanent broker failures, and exhausted attempts become terminal. Malformed consumer records retain broker coordinates, a digest, and redacted classification only. Validated terminal envelopes may be replayed to their current canonical route even when the original record used the wrong topic; malformed bodies are inspect-only.

Use the local operator command with explicit tenant identity:

```text
go run ./cmd/event-reconcile status
go run ./cmd/event-reconcile list-outbox <store-id> <limit>
go run ./cmd/event-reconcile list-dead-letters <store-id|unscoped> <limit>
go run ./cmd/event-reconcile requeue-outbox <store-id> <event-id> <reason>
go run ./cmd/event-reconcile replay-dead-letter <store-id> <dead-letter-id> <reason>
```

`status` reports global due, delayed-retry, publisher-terminal, and consumer dead-letter counts plus the oldest currently due timestamp without tenant labels. Listings accept limits from 1 through 100, expose identifiers and redacted failure metadata only, and require a store ID for validated tenant records. The literal `unscoped` lists only malformed records whose tenant identity was not trusted. Requeue and replay refuse foreign, ambiguous, non-terminal, already-resolved, or malformed records. Reasons are normalized, bounded, and persisted.

Published outbox and processed inbox rows have an approved 30-day window; resolved dead letters have a 90-day window. Unresolved/terminal rows are indefinite. Migration 13 adds the retention indexes. Use `retention-status` before the confirmation-gated `retention-clean`, keep `EVENT_RETENTION_HOLD=true` during incidents or restores, and follow [the operations runbook](operations-runbook.md). Never delete projections or unresolved rows as incident recovery.
