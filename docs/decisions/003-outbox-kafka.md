# ADR 003: Transactional outbox before Kafka

Status: Accepted

## Decision

Write events to a PostgreSQL outbox within the business transaction, then publish asynchronously to Kafka.

## Rationale

Database state must not diverge from emitted events when either PostgreSQL or Kafka fails.

## Consequences

- Delivery is at least once, so consumers must be idempotent.
- Outbox age, failures, and Kafka consumer lag must be observable.
- Kafka is not on the synchronous checkout commit path.
