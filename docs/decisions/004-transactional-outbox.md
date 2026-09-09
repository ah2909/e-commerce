# ADR 004: Transactional outbox before Kafka

Status: Accepted

## Context

PostgreSQL aggregate state and emitted integration facts must not diverge when a database commit, process, network, or Kafka operation fails. Kafka cannot participate in the business transaction and must not prevent checkout from committing.

## Decision

Write versioned events to a PostgreSQL outbox in the same transaction as the producing aggregate change. A worker claims committed rows in bounded concurrent-safe batches, publishes to Kafka, and records publication metadata.

Assume at-least-once publication and consumption. Consumers with business effects use a durable inbox or equivalent unique constraint in the same transaction as the effect.

## Consequences

- A crash after Kafka accepts an event but before the outbox is marked may publish it again; every consumer must be idempotent.
- Event envelopes carry store, aggregate, version, correlation, and schema identity while excluding credentials and unnecessary personal data.
- Kafka is not on the synchronous checkout, payment-recording, or tenant-provisioning commit path.
- Outbox age, attempts, terminal failures, Kafka consumer lag, inbox failures, and dead-letter/reconciliation age require metrics and alerts.
- Append-heavy outbox/inbox tables may be partitioned only after measured maintenance, retention, or query pressure justifies it and recovery is tested.
