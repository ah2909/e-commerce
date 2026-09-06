# Architecture

## Style

Start as a modular monolith deployed as one API process plus one worker process. Keep module contracts clear enough that a module may be extracted later, but do not pay the distributed-systems cost before evidence requires it.

## Runtime components

- API: authentication, request validation, authorization, and synchronous use cases.
- Worker: outbox publication and asynchronous consumers.
- PostgreSQL: authoritative transactional state.
- Redis: bounded cache and ephemeral coordination only.
- Kafka: durable integration events and side effects.
- Object storage: product media and generated documents.

## Modules

| Module | Owns | Does not own |
| --- | --- | --- |
| Identity | users, credentials, sessions | store permissions |
| Stores | stores, memberships, roles, settings | products or orders |
| Catalog | products, variants, categories, attributes | sell price or stock |
| Pricing | price lists, prices, coupons | product descriptions |
| Inventory | stock ledger and reservations | orders or payments |
| Customers | customer profiles and addresses | identity credentials |
| Carts | mutable shopping intent | final order history |
| Orders | checkout orchestration, snapshots, state machine | provider transactions |
| Payments | payment attempts, webhooks, provider adapters | order line ownership |
| Shipping | shipment options, shipments, provider adapters | inventory ownership |
| Audit | sensitive action records | business aggregate state |

## Dependency direction

- Transport adapters depend on application use cases.
- Application use cases coordinate domain objects and declared ports.
- Infrastructure implements ports and depends inward.
- Domain code has no dependency on transport or infrastructure.
- Cross-module reads use explicit query interfaces or stable projections.

## Multi-tenancy

Use shared tables with a mandatory `store_id` column for tenant-owned records.

Controls:

- Authenticated middleware resolves an immutable tenant context.
- Application methods require the tenant identifier explicitly.
- Repositories require it in every operation.
- Database indexes and unique constraints lead with/include `store_id` as appropriate.
- Optional PostgreSQL row-level security may be added as defense in depth after transaction-scoped tenant settings and connection-pool behavior are proven.

## Checkout transaction boundary

The initial checkout transaction should:

1. Lock or atomically validate required inventory.
2. Create inventory reservations.
3. calculate/validate totals using authoritative pricing.
4. Create the order and immutable line/address snapshots.
5. Store an outbox event in the same PostgreSQL transaction.
6. Commit and return the pending-payment order.

Provider payment confirmation occurs through an idempotent payment flow. Kafka availability is not part of the checkout commit path.

## Event delivery

- Store events in an outbox table in the same transaction as aggregate changes.
- A publisher claims unpublished rows, emits them to Kafka, and records publication.
- Assume at-least-once delivery; every consumer must be idempotent.
- Include `event_id`, `event_type`, `occurred_at`, `store_id`, `aggregate_id`, schema version, correlation ID, and payload.
- Do not place credentials or unnecessary personal data in events.

## Security baseline

- Deny by default; authorize actions using store membership and permissions.
- Hash passwords with a modern adaptive algorithm.
- Rotate refresh tokens and detect reuse.
- Validate webhook signatures from the raw request body.
- Encrypt traffic and protect secrets outside source control.
- Rate-limit authentication, checkout, and webhook endpoints appropriately.
- Audit role, price override, refund, and manual inventory changes.

## Observability

- Structured JSON logs with request/correlation IDs.
- Metrics for request latency/errors, checkout outcomes, outbox lag, consumer lag, inventory conflicts, and payment-webhook failures.
- Distributed trace context propagated through HTTP and events.
- Liveness checks only process health; readiness checks required dependencies.
