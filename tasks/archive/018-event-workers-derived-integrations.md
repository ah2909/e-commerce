# Task: Deliver event workers and derived integrations

## Context

This is roadmap slice 13 and follows the accepted checkout, payment, refund, and fulfillment slices. The current system writes tenant-scoped outbox facts atomically but does not publish them; Kafka, consumer inboxes, derived projections, terminal-failure handling, and backlog visibility are not implemented. Requirements come from the [product requirements](../../docs/product-requirements.md), [architecture](../../docs/architecture.md), [database design](../../docs/database-design.md), [event catalog](../../docs/event-catalog.md), [testing strategy](../../docs/testing-strategy.md), and [implementation roadmap](../../docs/implementation-roadmap.md).

Task 015 remains separately blocked only on live MoMo sandbox UAT. This task may publish and consume its already persisted event contracts, but it does not claim or waive that external acceptance gate. The accepted Identity risks in Task 004 are also unchanged.

## Outcome

- Concurrent worker instances publish committed outbox events to Kafka at least once without holding database transactions open during broker calls.
- Publisher crashes, broker failures, duplicate publication, delayed or out-of-order delivery, malformed messages, and consumer restarts have bounded, observable, recoverable outcomes.
- Each consumer validates the complete event envelope and tenant scope, then commits a tenant-scoped inbox record and its PostgreSQL effect atomically before acknowledging Kafka.
- Concrete consumers maintain a public-data Catalog search projection, tenant/event/day analytics counts, and durable email-notification intents for order lifecycle events.
- Operators can inspect publisher backlog and terminal failures and requeue an exact failed publication or dead-letter record without editing tables directly.
- Kafka remains outside checkout and every other commerce transaction; broker unavailability cannot prevent an authoritative PostgreSQL commit.

## In scope

- Extend Eventing with concurrent-safe outbox claims, expiring leases, bounded retries with exponential backoff and jitter, publication metadata, terminal classification, and exact-record requeue support.
- Add Kafka producer and consumer-group adapters, deterministic topic routing, strict envelope serialization/validation, bounded message sizes, correlation propagation, and graceful worker shutdown.
- Add Eventing-owned tenant-scoped consumer inbox records and durable dead-letter records for malformed, unsupported, permanently failed, and retry-exhausted deliveries.
- Add independent consumer groups for Catalog search, analytics, and email-notification intent processing.
- Add a Search-owned PostgreSQL projection built only from purpose-limited public Catalog data fetched through an explicit Catalog application interface.
- Add an Analytics-owned daily count projection keyed by store, UTC date, and event type; no customer contact, address, token, credential, or raw provider data is retained.
- Add a Notifications-owned durable email-intent projection for selected order lifecycle facts, using immutable order contact data through an explicit Orders application interface.
- Add an operator CLI for bounded backlog/dead-letter inspection and exact tenant-scoped requeue, plus structured backlog/lag/retry/terminal logging without unbounded tenant labels.
- Add migration 12, worker/configuration wiring, local Kafka topic bootstrap, event/schema/status documentation, and focused reliability, isolation, concurrency, and integration tests.

## Out of scope

- A production email vendor or SMTP delivery adapter, tenant-managed templates/sender identities, unsubscribe/preferences, SMS, push notifications, or claiming that an email was externally delivered. This slice proves a durable email consumer and intent boundary only.
- A public search HTTP endpoint, search ranking/recommendations, Elasticsearch/OpenSearch, full-text language tuning, or replacing authoritative Catalog reads. The projection is a rebuildable integration example.
- Customer-facing analytics/reporting APIs, data warehouse export, BI dashboards, or retention periods not yet approved by an operator.
- Kafka cluster provisioning outside local development, production ACLs/TLS/SASL, hosted observability dashboards/alerts, or deployment/runbook approval; those belong to operational hardening.
- Changing authoritative order, payment, inventory, refund, or fulfillment state from events; those states are already committed synchronously.
- Event sourcing, exactly-once delivery claims, cross-region replication, schema-registry infrastructure, partitioning, or microservice extraction.
- Adding every aspirational event from the catalog. This task consumes currently emitted facts and adds producer events only where required to keep an included projection correct.

## Invariants and security constraints

- PostgreSQL remains authoritative. A producer appends an event in the same transaction as its aggregate change, and Kafka is never contacted from that transaction.
- Every validated tenant event has non-empty `store_id`; publisher, inbox, derived-effect, dead-letter, inspection, and requeue queries use explicit store scope whenever a valid tenant identity exists.
- The Kafka message key is the documented composite `store_id:event_id`. Consumers assume at-least-once delivery and no cross-aggregate ordering; they use aggregate version/current-state checks where order matters.
- A consumer inserts `(store_id, consumer_name, event_id)` and applies its effect in one PostgreSQL transaction. A duplicate event returns the recorded outcome and is acknowledged without repeating the effect.
- Envelope and payload bytes are bounded before decode. Type, schema version, UUIDs, UTC timestamp, aggregate identity/version, correlation ID, payload object, and envelope/payload tenant agreement are validated before dispatch.
- Unknown type/version, malformed envelope, or permanent contract failure is recorded durably and acknowledged only after the dead-letter record commits. Transient failures are not acknowledged and use bounded retry/backoff; exhausted failures become visible terminal records.
- Invalid messages without a trustworthy tenant are stored only by broker coordinates plus digest, classification, and bounded redacted diagnostic metadata; raw message bodies and unnecessary personal data are not retained or logged.
- Search receives only public Catalog fields. Analytics receives only event identity/type/time and minimal non-personal dimensions. Notification intents may contain the minimum immutable recipient/contact snapshot needed by a later sender and are never logged.
- Consumers never query another module's tables directly. Search and Notifications call explicit Catalog and Orders application interfaces; Eventing owns only transport, inbox, and dead-letter mechanics.
- Claim leases recover abandoned work. A stale publisher cannot mark another worker's claim successful, and a publish-success/mark-failure race may republish but cannot lose the event.
- Kafka or one consumer group failing does not stop expiry jobs or unrelated consumer groups. Shutdown stops intake, bounds in-flight work by context, and releases or expires claims without corrupting offsets.

## Event and integration contract

- Use versioned domain topics `commerce.catalog.v1`, `commerce.inventory.v1`, `commerce.orders.v1`, `commerce.payments.v1`, and `commerce.shipping.v1`; routing is an explicit allowlist by event type, not caller input.
- Serialize the documented event envelope as JSON and carry event type, schema version, store, event, aggregate, and correlation identifiers in bounded Kafka headers for diagnosis. The JSON envelope remains authoritative.
- Configure separate consumer groups `search-projection-v1`, `analytics-projection-v1`, and `email-intents-v1`; group names and topic names are deployment-configurable only through validated startup configuration with safe defaults.
- Search consumes Catalog publication and any included Catalog change/archive facts and upserts/deletes by `(store_id, product_id)` only after fetching the public projection for that exact store and product.
- Analytics consumes the supported current event set and increments one `(store_id, event_date, event_type)` count in the inbox transaction.
- Email intents consume `order.created.v1`, `order.paid.v1`, `order.expired.v1`, `order.cancelled.v1`, `order.shipped.v1`, and `order.completed.v1`; unsupported or inapplicable events are durably classified rather than guessed.
- Requeue is an explicit local operator command requiring consumer/publication identity and store ID where validated. It records operator-supplied reason and never rewrites the original event or derived business state.

## Technical solution

1. Add an Eventing application layer with strict envelope codecs, event/topic allowlists, failure classes, retry policy, publisher and consumer runner ports, and bounded operator queries. Domain/application code remains independent of Kafka and GORM.
2. Add migration 12 to extend `outbox_events` with claim token/lease, terminal and broker metadata; add `consumer_inbox` and Eventing dead letters; and add Search, Analytics, and Notifications projection tables with store-leading keys, constraints, and active-work indexes.
3. Claim due outbox rows in short PostgreSQL transactions using `FOR UPDATE SKIP LOCKED`, stamp a random lease token/deadline, publish outside the transaction, and condition completion/failure updates on the same token. Compute retry scheduling from database time and cap attempts/backoff.
4. Use `github.com/twmb/franz-go` as the Kafka dependency because it provides context-aware idempotent production, consumer groups, manual offset control, bounded fetch/produce configuration, and testable interfaces without leaking Kafka types inward. Consumer inboxes remain the business-effect deduplication authority.
5. Run consumers with manual acknowledgement/offset commit only after the inbox/effect or durable terminal classification commits. Limit records and bytes in flight, isolate group failures, and cancel/drain within the configured shutdown deadline.
6. Implement Search, Analytics, and Notifications as separate modules owning their projection tables. Search and Notifications use narrow application queries to Catalog/Orders; Analytics derives only approved non-personal dimensions from the validated envelope.
7. Add a bounded `cmd/event-reconcile` operator CLI for status/list/requeue operations. Requeue clears only retry/terminal/lease state or marks a validated dead-letter eligible for replay; malformed records retain only a digest and are inspect-only. The command records reason and refuses ambiguous or cross-store identities.
8. Extend worker configuration for batch size, claim lease, poll interval, maximum attempts, retry bounds, message size, topic/group names, and consumer concurrency. Validate all limits and keep Kafka out of API readiness.
9. Bootstrap the fixed local topics in Compose/test setup and add Kafka integration tests using the disposable broker. Production topic creation, ACLs, replication, retention, and alert routing remain explicit operational-hardening inputs.
10. Update the event catalog, architecture/database/status documentation, configuration examples, and operator notes with the implemented topic mapping, retry/terminal semantics, projection limitations, and rebuild/replay procedure.

## Migration and compatibility

- Deploy migration 12 before enabling Task-18 publisher/consumers. The API and existing expiry workers remain compatible with schema 11; Task-18 worker loops are gated on schema 12 so a rolling binary can run safely before the migration.
- The outbox alteration is additive: nullable claim/publication/terminal columns are added first, existing unpublished rows remain due under their current `next_attempt_at`, and no event payload is rewritten or backfilled.
- Build new indexes with the project's lock-sensitive migration policy and validate exact query plans against a populated schema-11 copy. New inbox/projection tables start empty and are rebuildable from retained Kafka/outbox history.
- Existing outbox rows route only when their event type is on the explicit contract allowlist. Unknown legacy rows become visible terminal publications rather than being silently dropped or sent to an arbitrary topic.
- If migration or worker rollout fails, keep schema-11-compatible API/expiry behavior running, disable the new event loops, and forward-fix migration 12. Published offsets, inbox records, notification intents, and projections are retained; production rollback does not delete them.
- Topic retention and outbox/inbox deletion periods are intentionally not hard-coded until the operator approves replay and recovery windows in Task 19. This task adds bounded queries/indexes and backlog visibility but no destructive retention job.
- The only new runtime library is `franz-go` and its minimal transitive graph; dependency verification includes `make vulncheck`.

## Ordered implementation plan

1. Define strict event envelope/topic/retry/failure contracts and unit tests for size bounds, tenant agreement, allowlists, classification, backoff caps, and redacted diagnostics.
2. Add migration 12 and GORM repositories for leased outbox claims, conditional results, inboxes, dead letters, operator requeue, and the three tenant-owned projections; cover fresh/populated schema-11 upgrade and exact constraints/indexes.
3. Implement the Kafka producer adapter and concurrent outbox publisher with short claims, lease recovery, bounded batches, idempotent client settings, conditional marking, and publish/mark crash-window tests.
4. Implement the generic consumer-group runner with strict decode, manual acknowledgement, transactional inbox/effect handling, retry/terminal behavior, partition rebalance handling, and graceful cancellation.
5. Implement Search, Analytics, and Notifications consumers plus their explicit Catalog/Orders query ports, projection repositories, duplicate/out-of-order handling, and privacy tests.
6. Wire isolated publisher and consumer loops into `cmd/worker`; add validated configuration, structured backlog summaries, local topic bootstrap, and the exact-record reconciliation CLI.
7. Add real PostgreSQL and Kafka tests for concurrent publishers, lease expiry, duplicate publish/delivery, cross-store identities, consumer crash before/after commit, malformed/unsupported messages, broker outage/recovery, terminal requeue, and group isolation.
8. Update event, architecture, database, development-status, configuration, and operator documentation; no OpenAPI change is expected because this slice adds no HTTP endpoint.
9. Run formatting, zero-issue lint, focused unit tests, race-enabled PostgreSQL/Kafka integration tests, dependency scanning, and `make verify` because this is a security-sensitive cross-module release slice.

## Acceptance criteria

- [x] Given due outbox rows and multiple worker instances, when they publish concurrently, then each row is claimed by at most one active lease, successful rows record broker metadata, and a crash or expired lease makes work eligible again without event loss.
- [x] Given Kafka is unavailable or returns retryable/permanent failures, when publication runs, then commerce commits remain unaffected, retries are bounded with backoff, terminal failures are durable and observable, and an exact authorized operator requeue can resume work.
- [x] Given publish succeeded but the database mark failed, when the row is retried, then a duplicate may reach Kafka but every included consumer produces one tenant-scoped business effect.
- [x] Given duplicate, delayed, or out-of-order valid events, when Search, Analytics, or Notifications consumes them, then inbox/effect atomicity prevents duplicate effects and current-version/state rules prevent projection regression.
- [x] Given Store A and Store B reuse an event ID, aggregate ID, broker offset pattern, or projection identifier, when events are published, consumed, inspected, or requeued, then each store remains independent and neither can suppress, read, or mutate the other's records.
- [x] Given malformed, oversized, unknown-version, unknown-type, payload-tenant-mismatch, or permanently invalid messages, when consumed, then they cannot reach a handler, leak raw sensitive content, or stall a partition indefinitely; a durable redacted terminal record is created before acknowledgement.
- [x] Given a supported Catalog event, when Search consumes it, then the store-scoped public projection is upserted or removed through the Catalog interface with no authoring-only or foreign-tenant fields.
- [x] Given supported events across UTC date boundaries, when Analytics consumes them, then exact tenant/type/day counts update once without retaining personal or secret payload data.
- [x] Given a supported order lifecycle event, when Notifications consumes it, then one purpose-limited tenant-owned email intent is recorded from the immutable order contact snapshot; no external delivery is claimed and foreign-order/contact access is denied.
- [x] Given worker shutdown, consumer rebalance, one failed group, or a broker outage, when other expiry jobs or consumer groups run, then unrelated work continues or drains within configured bounds and resumes safely.
- [x] Migration 12, topic/configuration documentation, both independent reviews, focused PostgreSQL/Kafka evidence, `make vulncheck`, and final `make verify` pass.

## Verification strategy

| Requirement or risk | Required evidence |
| --- | --- |
| Envelope and privacy | Unit/fuzz tests for strict JSON/header decoding, byte/depth/field bounds, UTC/UUID/version validation, tenant mismatch, allowlists, and redacted diagnostics |
| Concurrent publication | Synchronized real-PostgreSQL tests for `SKIP LOCKED` claims, lease ownership/expiry, conditional mark, retry scheduling, one active claimant, and the publish/mark duplicate window |
| Kafka behavior | Disposable Kafka tests for topic routing/key/header/envelope contract, broker outage/recovery, manual acknowledgement, consumer-group rebalance, restart, and bounded in-flight work |
| Inbox atomicity | Failure-injection PostgreSQL tests proving inbox and Search/Analytics/Notifications effects commit or roll back together, with duplicate redelivery producing one effect |
| Tenant isolation | Same-ID cross-store tests for outbox claims, inbox, dead letters, search documents, analytics counts, email intents, status queries, and exact requeue |
| Ordering and versions | Delayed/reversed aggregate events proving projections do not regress and consumers do not assume global or cross-aggregate ordering |
| Terminal handling | Retryable/permanent/exhausted/malformed cases, durable redacted records, partition progress after commit, and exact requeue audit/replay behavior |
| Module boundaries | Static/import and integration tests proving consumers use Catalog/Orders application interfaces and do not query foreign module tables |
| Migration and rollout | Fresh schema, populated schema-11 upgrade, existing unpublished-row compatibility, exact constraints/indexes, repeat migration, and schema-11 feature gating |
| Worker operability | Configuration bounds, graceful shutdown, isolated loop failure, backlog age/retry/terminal summaries, and no high-cardinality tenant metrics/log leakage |
| Dependency and final checks | `gofmt` on changed Go files, `make fmt-check`, `make lint`, focused `go test` commands, `make test-integration`, `make vulncheck`, and `make verify` |

## Required completion report

- Behavior and design summary
- Files/modules changed
- Commands executed with results
- Risks, assumptions, and follow-ups

## Delivery record

- Stage: complete
- Active task: no
- Execution mode: automatic delivery (`continue`)
- Last command, completed stage, pending gates, and next suggested command: `make verify`; automatic delivery is complete with no pending Task 18 gate.
- Plan revision and approval: revision 1 prepared on 2026-09-20 and approved by the user on 2026-09-20 with `continue`.
- Plan: Eventing publisher/consumer mechanics, migration 12, Kafka topics, durable inbox/dead-letter/requeue, Search/Analytics/Notifications projections, worker wiring, compatibility, and ordered implementation are recorded above.
- Verification: acceptance criteria map to unit/fuzz tests, real PostgreSQL/Kafka reliability and isolation tests, migration evidence, dependency scanning, and broad final verification above.
- Implementation evidence: added migration 12; strict event envelope/topic contracts; leased outbox claims and bounded publisher retries; franz-go producer/consumer groups; transactional inbox/effect coordination; durable retry, terminal, and replay state; Search, Analytics, and email-intent projections; bounded operator CLI; worker configuration/shutdown coordination; local topic bootstrap; and event operating documentation.
- Focused verification: `go test ./...` passed; `make fmt-check` passed; `make lint` passed with zero issues; populated schema-11 migration/repeat tests passed; focused real-PostgreSQL Eventing tests passed for claims, claim theft, tenant isolation, inbox atomicity, retry exhaustion, Search, Analytics, email intents, and foreign-order denial; the disposable Kafka round-trip passed publish, group delivery, commit, and cancellation; `docker compose config --quiet`, local-link validation, and `git diff --check` passed. The first Kafka shutdown test exposed a blocked group leave and now uses franz-go's rebalance-aware close path with a passing regression.
- Architect review: changes required. High: an allowed unknown-schema outbox row rolled back the whole claim batch. Medium: topic/key contract enforcement was incomplete, terminal/dead-letter inspection was unavailable and due counts included future retries, and required Kafka failure-path evidence was incomplete.
- Security review: changes required. Medium: event-specific payload/aggregate/topic validation was incomplete and allowed false cross-object effects; arbitrary fields could be retained in replayable envelopes; and one batch deadline could consume attempts for records without a viable broker attempt.
- Fix evidence: outbox claims now carry unknown schemas to per-row terminal classification without poisoning the batch; attempts increment only after a real publication or terminal classification and untouched claims release; each publish has an independent deadline; strict per-event contracts enforce topic, key, headers, aggregate type, store and object identity, typed required/allowed payload fields; invalid payloads remain digest-only; status and bounded scoped/unscoped listings expose retry and dead-letter state; focused unit, PostgreSQL, and real-Kafka tests cover these corrections, restart redelivery, group isolation, and terminal partition progress.
- First focused re-review: prior findings were closed except three medium gaps. Shipment lifecycle notifications still trusted an unrelated same-store order ID; transport-mismatch dead letters retained a canonical envelope that the replay command refused to route canonically; and actual broker outage/recovery plus in-flight group rebalance evidence was absent.
- Second fix evidence: shipped/completed envelopes now use the order aggregate and bind the required payload order ID to it while rejecting every empty required identifier; replay reconstructs and publishes to the current allowlisted canonical topic while preserving the original topic as diagnostics; real Kafka tests restore bootstrap access during an in-flight publish and join a second group member while a record is blocked, then prove recovery and partition resumption. Focused unit, Shipping/PostgreSQL, and repeated Kafka integration tests pass.
- Architect re-review: pass. Poison-row handling, strict transport contracts, operability, canonical replay, broker recovery, and in-flight rebalance evidence have no remaining blocking findings.
- Security re-review: pass. Payload privacy, retry accounting, required identifiers, and same-store shipment/order binding have no remaining blocking findings.
- Final evidence: the first full gate found one stale fulfillment assertion after shipped/completed events became order aggregates; the corrected assertion passed its focused real-PostgreSQL package. The clean rerun of `make verify` then passed formatting, zero-issue lint, all unit tests, all integration tests at schema 12 including real Kafka recovery/rebalance coverage, and `govulncheck` with zero called or imported-package vulnerabilities.
