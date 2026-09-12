# System architecture

## Scope and status

This document describes the target architecture for the MVP and its intended growth path. It is not an implementation-completion claim; see [development status](development-status.md).

The system begins as a modular monolith deployed as one stateless API process and one stateless worker process. PostgreSQL is the authoritative transactional store. Redis provides bounded ephemeral coordination. Kafka carries asynchronous integration events published through a transactional outbox. Object storage holds product media and generated documents.

The API uses Gin for route registration and transport middleware while the standard `http.Server` retains lifecycle and timeout ownership. Runtime PostgreSQL adapters use GORM over one explicitly configured `database/sql` pool. Gin and GORM remain outside application and domain packages, and Goose SQL migrations remain the only schema authority. See the [Gin and GORM placement guide](gin-gorm-guide.md) and [ADR 005](decisions/005-gin-gorm-runtime-adapters.md).

## Design goals

- Preserve store isolation at every boundary.
- Keep money, inventory, payment, refund, and order changes transactionally correct.
- Make module ownership clear enough to change one business area without coupling the whole application.
- Scale common stateless work horizontally before adding distributed data ownership.
- Operate the same application in shared SaaS and customer-managed deployments.
- Keep external providers and Kafka out of the checkout database commit.

## System context

```text
Storefront/Admin UI
        |
   HTTPS API
        |
  +-----+-------------------+
  | API process             |
  | auth, policy, use cases |
  +-----+-------------------+
        |
  PostgreSQL ---- outbox ---- Worker ---- Kafka ---- consumers
        |                       |
      Redis                external providers
        |
  object storage
```

All internet-facing requests pass through trusted TLS termination. Host validation selects the store before tenant authentication. Provider callbacks use fixed, explicitly configured callback hosts and resolve the store from server-created state or a provider reference, never from an arbitrary payload store identifier.

## Deployment modes

### Shared SaaS

- One deployment may serve many independent stores from `{store}.{base-domain}`.
- A deployment-managed platform bearer token authorizes tenant provisioning. Individual platform accounts, activation, and suspension workflows are deferred.
- The store subdomain is immutable for the MVP and maps to one `store_id`.
- Shared PostgreSQL tables carry mandatory store scope. Redis keys, jobs, metrics labels, and events also carry store scope.
- No `X-Forwarded-Host` or request-body field is tenant authority. Proxy configuration must preserve the validated public host.

### Customer-managed

- The same API, worker, migrations, and module contracts are deployed by the customer.
- Trusted deployment configuration selects one routing mode. Single-store mode maps one exact configured tenant host to one configured `store_id`; multi-store mode resolves `{store}.{base-domain}` through the persisted immutable subdomain.
- The platform/authentication hosts are configured separately from tenant hosts and never resolve to a store. Requests cannot select or override `store_id` through a path, header, query, or body field.
- The customer owns TLS, secrets, backups, monitoring, dependency lifecycle, and disaster recovery.
- The application does not weaken tenant scoping in a single-store deployment; this prevents configuration changes from exposing unscoped access paths.

Custom domains and automatic cross-deployment data movement require later ADRs.

## Runtime components

| Component | Responsibility | Must not become authoritative for |
| --- | --- | --- |
| API | Host validation, authentication, authorization, validation, synchronous use cases | cached prices, client totals, provider callbacks not yet persisted |
| Worker | Reservation expiry, outbox publication, event consumers, notifications, reconciliation retries | accepting a checkout without PostgreSQL state |
| PostgreSQL | Aggregates, constraints, ledgers, snapshots, idempotency, outbox/inbox, audit | large media blobs or ephemeral rate counters |
| Redis | Rate limits, short-lived OAuth state, bounded cache, ephemeral coordination | orders, stock, payments, permissions, or durable locks |
| Kafka | Durable asynchronous facts and integration fan-out | the synchronous checkout commit or request authorization |
| Object storage | Product media and generated documents | tenant authorization metadata or mutable business state |

## Module ownership

| Module | Owns | Primary interfaces used by other modules |
| --- | --- | --- |
| Platform administration | deployment-token authentication and tenant provisioning policy | provision store |
| Identity | users, credentials, social links, sessions, guest principals | authenticate store-bound principal |
| Stores | stores, subdomains, memberships, roles, regional settings | resolve store, authorize membership, read regional settings |
| Catalog | products, variants, categories, options, media references | read sellable variant snapshot |
| Pricing | price lists, prices, post-MVP coupons and discount evaluation | resolve authoritative checkout price |
| Inventory | stock items, reservations, stock ledger | reserve, confirm, release stock |
| Customers | profiles and addresses | read customer-owned checkout data |
| Carts | mutable shopping intent | read/close cart |
| Orders | checkout orchestration, order snapshots, state machine, guest order access | create and transition order |
| Payments | provider settings, payment attempts, webhooks, reconciliation, refunds | initiate and confirm payment/refund |
| Shipping | flat/free rules, shipment record, manual tracking | quote shipping, record fulfillment |
| Audit | immutable privileged-action records | append audit record in required transaction |
| Eventing | outbox/inbox persistence, publication, delivery deduplication | append fact in caller transaction, claim publications, deduplicate consumption |

A module exclusively owns its tables and domain rules. Another module calls an application interface; it does not query the owner's tables. Cross-module transactions are exceptional and explicit. Checkout, tenant provisioning, payment confirmation, and order expiry use an application-level unit of work whose participating repositories remain module-owned. Eventing owns outbox/inbox mechanics while each producer owns the meaning and versioning of its facts.

## Dependency direction

- Transport adapters depend on application use cases.
- Application use cases coordinate domain types and consumer-owned ports.
- Infrastructure implements those ports and depends inward.
- Domain packages do not depend on HTTP, SQL, Kafka, Redis, or vendor SDK types.
- Shared packages contain only stable primitives such as identifiers, clocks, money, and problem metadata; they do not become a miscellaneous business layer.
- Synchronous cross-module reads use explicit query interfaces or stable projections.
- Asynchronous side effects consume versioned facts from the outbox.

## Request security boundary

Tenant API processing follows this order:

1. Normalize and validate the direct request host against deployment configuration.
2. Resolve the immutable store and reject unknown or suspended stores where the use case requires activity.
3. Authenticate a user or guest credential using both `store_id` and token digest.
4. Resolve a current workforce membership when the operation is administrative.
5. Authorize an explicit permission with deny-by-default policy.
6. Invoke a tenant-scoped use case and repository.

Platform provisioning uses a base-domain route and a distinct platform principal authenticated by a high-entropy deployment secret. It creates owner credentials without creating an owner session; the owner signs in on the new store host. A tenant owner is not a platform administrator.

Guest order access uses an independently revocable, high-entropy digest stored with `store_id` and `order_id`. Order number, email, phone, or a guest session identifier alone never grants access.

## Critical transactional flows

### Checkout

The initial checkout transaction:

1. Validates the active store, cart ownership, VND currency, address, and shipping selection.
2. Loads authoritative sellable variants and prices through module interfaces.
3. Locks or atomically updates inventory and creates ten-minute reservations.
4. Persists the order, lines, address, totals, shipping, and tax-presentation snapshots.
5. Persists the selected pending payment and an idempotency record.
6. Writes `order.created.v1` and reservation events to the outbox.
7. Commits before starting external payment work.

Retrying the same store-scoped idempotency key returns the original outcome. PostgreSQL failure rolls back every step. Kafka or provider failure leaves a durable pending order that can be retried or expired.

### VietQR

Generating a VietQR image records a payment instruction, not a payment. The reference binds store, order, expected amount, and payment attempt. Owner/admin, or staff with explicit permission, verifies the destination bank account and records one audited confirmation. Duplicate confirmation returns the recorded result.

Expired, late, wrong-reference, or wrong-amount transfers enter a reconciliation queue. Reconciliation never silently restores inventory or a cancelled/expired order. An authorized operator either associates a valid payment under an explicit recovery use case or records an externally settled full refund.

### MoMo

The Payments module creates a store-scoped attempt with a unique idempotency key and bounded provider timeout. Callback and webhook handlers validate the signature from the raw body, provider merchant identity, amount, currency, order reference, and event uniqueness before changing state. Duplicate events acknowledge the prior result.

Validated MoMo success and audited VietQR confirmation use one PostgreSQL transaction coordinated by Payments. Every competing workflow locks the order, payment, reservations ordered by reservation ID, and affected stock items ordered by variant ID in that global order, then rechecks states and reservation deadlines using database time. If the order is still payable and every reservation is active, guarded stock updates decrement `on_hand` by each reserved quantity, the transaction appends matching ledger entries, confirms the reservations, marks the payment succeeded and order paid, records receipt/audit data, and writes the resulting outbox facts atomically. Any failed stock invariant rolls back the complete transition. If expiry or cancellation already won, or the database deadline has passed, it records the received funds as reconciliation required and emits only that fact; it never revives the order or inventory.

The expiry worker discovers a bounded set of candidate order IDs without retaining row locks. It processes one candidate per transaction by first claiming the order row with `FOR UPDATE SKIP LOCKED`, then acquiring the remaining global-order locks and rechecking state and database time. If confirmation already committed, expiry is a no-op; if expiry commits first, a later confirmed transfer enters reconciliation. Candidate duplication across workers is harmless because only one can claim and transition the order.

### Cancellation and refund

An unpaid order may be cancelled by its customer/guest or authorized staff, releasing active inventory once. Staff may cancel before fulfillment. Cancelling a paid order creates a full-refund request; payment remains paid until the refund succeeds or is externally confirmed. MoMo refund operations are idempotent provider calls. VietQR refunds are manually settled and then recorded with actor, reason, external reference, and timestamp.

## Event delivery

- The producing module writes an outbox row in the same transaction as its aggregate change.
- Commerce events describe an already committed outcome; consumers do not use `payment.confirmed.v1` to transition Orders or Inventory.
- Workers claim rows in bounded batches using locking that permits concurrent publishers.
- Publication is at least once. A publish marker is diagnostic, not proof that a consumer applied the event.
- Consumers with business effects use a durable inbox or an equivalent unique constraint in the same transaction as the effect.
- Events include `event_id`, `event_type`, `schema_version`, `occurred_at`, `store_id`, `aggregate_type`, `aggregate_id`, `correlation_id`, and a minimal payload.
- Outbox and inbox identities include `store_id`; one tenant's event identifier cannot deduplicate or suppress another tenant's work.
- Events may be out of order across aggregates. Consumers use aggregate version or explicit state checks where order matters.
- Credentials, raw provider payloads, access tokens, and unnecessary personal data are excluded.

## Data and secrets

- PostgreSQL stores UTC `timestamptz`; APIs expose RFC 3339 timestamps. Store timezone is presentation/configuration data.
- Money is an integer minor-unit amount paired with ISO currency, even while the MVP uses VND.
- Provider secrets are encrypted with an application data-encryption key supplied through the deployment secret manager. Ciphertext includes key version so rotation can re-encrypt incrementally.
- Secret values are write-only through the API, redacted in logs, absent from events, and decrypted only immediately before an adapter call.
- Product media uses object keys, checksums, MIME metadata, size limits, and store ownership; signed access is time-bounded.

## Availability and failure policy

- Start with one region and a documented basic recovery target rather than claiming high availability not yet proven.
- API liveness checks only process health. Readiness checks PostgreSQL and Redis dependencies needed for served routes; Kafka does not gate checkout readiness.
- Every external request has connection and total deadlines and classifies retryable versus permanent errors.
- Retries use bounded exponential backoff with jitter and a terminal dead-letter or reconciliation state.
- PostgreSQL is backed up automatically and restore exercises are required before production readiness. Recovery-point and recovery-time objectives must be set by the operator before launch.
- Deploy migrations before code that requires them. Use expand/contract migrations for live schemas and forward fixes rather than destructive down migrations.

## Observability

- Structured JSON logs carry request, correlation, store, actor type, and operation identifiers while excluding secrets and unnecessary personal data.
- Distributed trace context flows through HTTP, outbox events, Kafka, and provider adapters.
- Core metrics cover request latency/errors, database pool saturation, slow queries, checkout outcomes, reservation conflicts/expiry lag, payment and refund failures, reconciliation age, outbox age, consumer lag, and job retries.
- Avoid unbounded `store_id` labels in global metrics. Use logs/traces or controlled top-tenant views for tenant-level diagnosis.
- Alert on customer impact and backlog age, not only infrastructure utilization.

## Scaling path

Immediate design practices:

- Run multiple stateless API and worker instances behind a load balancer.
- Keep requests bounded with validation, pagination, timeouts, and maximum batch sizes.
- Index real query shapes with `store_id` leading tenant scans.
- Use database connection budgets per process and observe pool wait time.
- Claim asynchronous work in bounded batches; separate worker pools by workload class so email or indexing cannot starve reservation expiry or payment reconciliation.
- Cache only safe, versioned, reconstructable values and protect against cache stampedes.

Add complexity only when measurements justify it:

| Signal | Preferred next step |
| --- | --- |
| Read latency or primary CPU is dominated by stale-tolerant queries | Add projections and read replicas; keep checkout and authorization on primary |
| Append-only indexes or maintenance dominate audit/outbox/ledger/webhook tables | Time-partition that table with tested retention and uniqueness strategy |
| One tenant causes sustained pool, storage, or maintenance pressure | Introduce a tenant-placement interface and move that tenant through a verified migration |
| Worker backlog differs sharply by workload | Split worker deployments and Kafka consumer groups before splitting code ownership |
| A module needs independent ownership, release cadence, failure isolation, or regulatory boundary | Evaluate service extraction with an ADR and data migration plan |
| Regional latency or residency becomes contractual | Add region-aware tenant placement; do not attempt active-active writes without a new consistency design |

No fixed store count or traffic estimate was supplied. Capacity is therefore established through production metrics and load tests, not architectural claims.

## Related documents

- [Product requirements](product-requirements.md)
- [Domain model](domain-model.md)
- [Database design](database-design.md)
- [Event catalog](event-catalog.md)
- [Testing strategy](testing-strategy.md)
- [Implementation roadmap](implementation-roadmap.md)
- [Development status](development-status.md)
- [ADR 001: modular monolith](decisions/001-modular-monolith.md)
- [ADR 002: shared-schema tenancy](decisions/002-shared-schema-tenancy.md)
- [ADR 003: identity and host-selected stores](decisions/003-identity-sessions-and-store-authorization.md)
- [ADR 004: transactional outbox](decisions/004-transactional-outbox.md)
