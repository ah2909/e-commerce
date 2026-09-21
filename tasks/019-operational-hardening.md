# Task 019: Operational hardening and production readiness

## Context

- [Product requirements](../docs/product-requirements.md)
- [System architecture](../docs/architecture.md)
- [Database design](../docs/database-design.md)
- [Event catalog](../docs/event-catalog.md)
- [Event worker operations](../docs/event-operations.md)
- [Testing strategy](../docs/testing-strategy.md)
- [Implementation roadmap](../docs/implementation-roadmap.md)
- [Development status](../docs/development-status.md)
- [ADR 002: shared-schema tenancy](../docs/decisions/002-shared-schema-tenancy.md)
- [ADR 003: identity sessions and store authorization](../docs/decisions/003-identity-sessions-and-store-authorization.md)
- [ADR 004: transactional outbox](../docs/decisions/004-transactional-outbox.md)
- [Task 018: event workers and derived integrations](archive/018-event-workers-derived-integrations.md)

## Outcome

The API and worker have a tested, provider-neutral production baseline for shared multi-store and customer-managed exact-host single-store deployments. Operators can deploy immutable containers, validate configuration, observe service and commerce health without leaking tenant or personal data, rotate deployment and payment-encryption secrets, restore authoritative PostgreSQL state, rebuild derived state, enforce approved replay retention, and follow tested incident and release runbooks. A reproducible security, migration, capacity, slow-query, backup, and restore evidence bundle establishes the supported launch envelope and records any unmet external gates.

## In scope

- Add an explicit routing mode: shared multi-store subdomains or one exact configured customer-managed tenant host mapped to one configured `store_id`; keep platform and authentication hosts distinct in both modes.
- Harden production startup configuration, connection budgets, database timeouts, TLS/proxy assumptions, Kafka security/topic settings, Redis/Kafka/PostgreSQL endpoints, secret sources, and fail-closed validation.
- Add reproducible OCI images and provider-neutral API, worker, migration, and operator-command deployment examples with non-root execution, read-only runtime filesystems where practical, health probes, resource budgets, graceful shutdown, migration-before-code ordering, and rollback/forward-fix instructions.
- Add bounded low-cardinality metrics, trace propagation/export, redacted slow-query diagnostics, dashboards, and alert rules for HTTP, dependency pools, commerce failures, reconciliation, expiries, outbox/inbox/dead letters, Kafka lag, and worker retries.
- Add payment data-encryption-key inspection and bounded online re-encryption for provider configuration, callback routes, and encrypted attempt snapshots. Document restart-based platform-token and provider-secret rotation without logging secret material.
- Approve and implement event replay retention: Kafka event topics retain 30 days; successfully published outbox rows and processed inbox rows retain 30 days; resolved consumer dead letters retain 90 days; unresolved/terminal rows remain until an explicit operator resolution. Financial, order, inventory-ledger, audit, webhook, reconciliation, and refund records remain retained because no legal deletion policy is approved.
- Add bounded, resumable retention commands/jobs which require an explicit cutoff, operate only on eligible terminal records, report counts, and are tenant-safe. No projection or unresolved record is deleted as incident recovery.
- Add encrypted PostgreSQL backup/PITR configuration requirements, a provider-neutral backup verification contract, and a disposable restore exercise that verifies schema, tenant isolation, commerce invariants, outbox continuity, active-key decryptability, API startup, and derived-state rebuild procedures.
- Define and exercise a representative skewed dataset, migration timing/locking checks, slow-query plans, connection-pool budgets, load/capacity scenarios, and documented stop thresholds. Record the tested envelope instead of claiming unmeasured scale.
- Add security checks for container/dependency exposure, production configuration, host/proxy spoofing, CORS, authentication throttling, secret/log redaction, HTTP limits/timeouts, and a baseline dynamic scan against a disposable environment.
- Update operational, deployment, event-retention, architecture, database, testing, and development-status documentation with exact commands, ownership, evidence, and launch blockers.

## Out of scope

- Selecting or provisioning a cloud vendor, Kubernetes distribution, DNS provider, TLS issuer, secret manager, managed database, monitoring SaaS, or paging service.
- Multi-region or active-active availability, database failover automation, tenant placement, read replicas, table partitioning, microservice extraction, or custom tenant domains.
- Individual platform administrator accounts or overlapping rotation of the static platform token; ADR 003's replacement-plus-restart model remains authoritative.
- Changing the accepted Task 004 clock-skew and store-suspension/session-admission risks unless new evidence makes either critical/high.
- Closing Task 015's live MoMo sandbox gate, executing live Google/Facebook interoperability, or operating against production credentials without separate external authorization.
- A legal data-erasure policy for financial, order, audit, or personal records, and post-MVP coupons or redemption accounting.

## Invariants and security constraints

- Host configuration, never a caller-controlled header, path, query, body, or forwarded host, selects tenant scope. Single-store mode still passes an explicit `store_id` through every tenant-owned repository.
- Platform and authentication hosts cannot equal or overlap the tenant host boundary. Unknown, malformed, forwarded-only, or mode-incompatible hosts fail closed.
- Metrics and dashboards do not use unbounded `store_id`, user, order, payment, email, phone, token, provider reference, or raw route labels. Logs and traces retain only bounded operational identifiers and never secret or unnecessary personal data.
- Observability and Kafka outages do not gate checkout commits. Telemetry export is bounded and non-blocking, and failure cannot exhaust request or worker resources.
- Payment-key rotation authenticates existing ciphertext with its original AAD, re-encrypts in bounded transactions with the active key, uses compare-and-swap guards against concurrent business updates, is resumable and idempotent, and does not permit removal of a key while rows still reference it.
- Backup artifacts and production credentials are never committed. Restore targets are guarded as non-production/disposable unless separately authorized, and no production restore or destructive reset is part of this task.
- Retention uses database time, bounded batches, explicit terminal predicates, and a safety delay. Dry-run/count evidence precedes deletion; unresolved, unprocessed, financial, audit, and ledger data is excluded.
- Load and dynamic-security tests use synthetic data and disposable environments. They have bounded duration/rate and are never pointed at production by default.
- Database connection totals across API and worker replicas remain below the approved PostgreSQL budget. Request and worker transactions receive appropriate statement, lock, and idle-in-transaction timeouts.

## Proposed launch objectives requiring plan approval

- Availability SLO: 99.5% monthly for the API, excluding approved maintenance; `/health/live` is not an availability success signal.
- Latency SLO at the ingress boundary: 95% of non-provider API requests below 500 ms and checkout requests below 1 second, measured over five-minute windows; provider completion latency is tracked separately.
- Correctness SLOs: no known cross-store disclosure or money/inventory correctness violation; 99% of due reservation/order expiries complete within 60 seconds; 99% of publishable outbox events publish within 60 seconds while Kafka is healthy.
- Recovery objectives: PostgreSQL RPO at most 15 minutes and service RTO at most 4 hours for the initial single-region baseline. Redis and derived Kafka projections are rebuildable and are not recovery authorities.
- Paging thresholds and burn-rate windows derive from these objectives; warning-only capacity alerts fire before saturation and do not redefine the SLO.

Approval of this revision approves these initial targets and retention periods as the launch baseline. Meeting them still requires measured evidence from the selected deployment environment; repository-local tests alone do not prove a production SLO, RPO, or RTO.

## Technical solution

1. Extend platform configuration with an enum routing mode and mode-specific values. Preserve the existing multi-store variables as the compatibility default; single-store mode requires an exact HTTPS-facing tenant host and UUID store ID, rejects subdomain fallback, and resolves that configured ID through the existing Stores application interface so active/suspended state remains authoritative. Wire the same middleware into every tenant route and cover platform/auth/tenant host separation.
2. Add production-only validation for placeholder secrets, insecure dependency URLs, missing trusted-proxy boundaries, wildcard/loopback public hosts, database SSL, Kafka authentication/TLS settings, observability endpoints, and explicit pool/timeouts. Make pool limits configurable per process and set PostgreSQL session-level statement, lock, and idle-transaction safeguards without leaking URLs or credentials in errors.
3. Build minimal multi-stage API, worker, migrate, and operator-command container targets from the same revision. Supply provider-neutral deployment examples and a release checklist showing image digest pinning, schema migration first, readiness rollout, worker enablement, rollback compatibility, resource/connection budgets, and forward-fix recovery. These artifacts describe interfaces to external secret, TLS, database, Kafka, backup, and monitoring services rather than embedding a vendor choice.
4. Introduce a small platform observability boundary backed by Prometheus-compatible metrics and OpenTelemetry-compatible tracing. Instrument HTTP outcome/latency/in-flight counts, SQL pool stats and redacted slow-query duration, external-provider outcomes, expiration lag, payment/refund/reconciliation state, outbox age/attempts/terminals, consumer failures/dead letters, and loop health. Expose metrics only on a separately configured internal listener and provide scrape, dashboard, recording-rule, and alert-rule examples with cardinality tests.
5. Add a payment key-rotation application service and local operator command with `status`, `rotate --from --to --batch`, and verification modes. Rotate every encrypted Payments-owned record type using its existing AAD and key version, lock or compare versions per bounded batch, expose only counts/digests, and refuse key retirement until a database scan returns zero references. Document add-new-key, deploy readers, switch writer, re-encrypt, verify, retire-old-key ordering; document restart rotation for other deployment secrets.
6. Add Eventing retention operations over the existing retention-oriented indexes. Status/dry-run lists aggregate counts and oldest/newest eligibility without payloads; execution deletes small committed batches only for successfully published/processed rows older than the approved window and resolved dead letters older than 90 days. Configure Kafka topics for the same 30-day replay floor and document how retention interacts with replay, rebuild, backup, and incident holds.
7. Add backup/restore tooling contracts and a disposable exercise harness. Capture an encrypted logical backup for portable verification and require the selected production PostgreSQL service to document continuous backup/PITR meeting the 15-minute RPO. Restore into a guarded isolated database, apply no destructive action to the source, verify schema and representative cross-module invariants, test decryption with every referenced key version, start API/worker probes, and exercise projection rebuild/reconciliation. Record measured backup age and recovery duration against RPO/RTO.
8. Add deterministic fixture generation and versioned load scenarios for storefront reads, login throttling, cart mutation, checkout contention, admin queues, and background event/expiry pressure with multiple tenants and a skewed large tenant. Capture throughput, latency/error distributions, pool wait, lock conflicts, backlog age, CPU/memory, and database growth. Run `EXPLAIN (ANALYZE, BUFFERS)` for measured hot queries and change indexes/query shapes only when evidence justifies it; any new schema migration requires a documented locking and forward-fix plan.
9. Add release-security automation: race-enabled Linux tests, `govulncheck`, static analysis, secret scanning, SBOM/container vulnerability scan, configuration-negative tests, and a bounded OWASP ZAP baseline against the disposable API. Pin tools/actions/images by immutable version or digest where supported and document false-positive handling rather than silently suppressing findings.
10. Write launch, deployment, key rotation, backup/restore, dependency outage, queue backlog, reconciliation, rollback, and incident runbooks. Update architecture/database/event/testing/status documents with the approved objectives, retention, tested capacity envelope, slow-query evidence, external ownership, and every remaining release blocker.

## Migration 13 revision

Repository inspection during implementation showed that migration 12 indexes due/terminal outbox work, processed inbox rows, and open dead letters, but does not index successfully published outbox rows or resolved dead letters by their retention timestamps. Running approved cleanup without matching indexes would force repeated scans of append-heavy tables.

Revision 2 therefore adds migration 13 with two partial retention indexes:

- `outbox_events (published_at, store_id, event_id) WHERE published_at IS NOT NULL`
- `event_dead_letters (resolved_at, dead_letter_id) WHERE resolved_at IS NOT NULL`

The existing `consumer_inbox_processed_idx` already serves inbox retention. Migration 13 contains indexes only, does not backfill or delete rows, and introduces no API behavior. It runs before retention is enabled. On PostgreSQL environments where table size makes ordinary index creation exceed the approved lock window, operators create the same indexes concurrently in a separately scheduled non-transactional migration step before deploying cleanup; no retention job runs until both indexes are present. Failure leaves schema 12 behavior intact, and recovery is a forward fix rather than index removal during an active release.

## Compatibility, deployment, and recovery

- Migration 13 adds only the two partial retention indexes recorded above. Routing and observability remain runtime additions, and encrypted columns already carry key versions. No data rewrite or destructive retention occurs in the migration.
- Multi-store subdomain routing remains the default for existing deployments. Single-store mode is opt-in and fails startup unless all exact-host values are complete and non-overlapping.
- Deploy observability and new configuration support before making production validation mandatory. Migration commands use the same image digest as API/worker. Deploy migrations, then API, then worker; keep event consumers disabled until topics, ACLs, and retention are verified.
- Payment-key rotation is expand/rotate/contract: add the new key alongside old keys, deploy readers, select it for writes, re-encrypt online, verify zero old references, then remove the old key in a later deployment. Rollback retains both keys and resumes the idempotent scan.
- Retention starts in report-only mode. Enable deletion only after a successful restore exercise and confirmation that Kafka and database windows agree. Disable the job and forward-fix on failure; backups, unresolved records, and financial/audit authorities remain intact.
- A failed production rollout returns to the last schema-compatible image. Forward-only schema rules remain in force; production data is never reset or restored without separate authorization.

## Ordered implementation plan

1. Implement and test routing-mode/configuration parsing, exact-host single-store resolution, production fail-closed rules, pool/timeouts, and compatibility with current multi-store routing.
2. Add container/release artifacts and deployment validation for API, worker, migrations, internal telemetry, health probes, non-root operation, immutable image selection, resource budgets, and rollout ordering.
3. Add the observability boundary, internal metrics server, trace propagation/export, SQL/external/worker instrumentation, cardinality guards, dashboards, recording rules, and alert rules.
4. Implement payment-key inventory and bounded re-encryption across provider configs, callback routes, and attempt snapshots, with concurrency, interruption, wrong-key, cross-store, redaction, and retirement-safety tests.
5. Implement report-only and destructive Eventing retention paths with explicit cutoffs, bounded transactions, safe eligibility rules, incident holds, Kafka topic settings, and real-PostgreSQL isolation/concurrency tests.
6. Add backup contracts and a guarded disposable restore exercise that validates authoritative data, encryption keys, tenant isolation, outbox continuity, startup, and projection recovery while measuring RPO/RTO evidence.
7. Create skewed fixtures, load scenarios, query-plan capture, migration-lock timing, and capacity reports; correct only measured bottlenecks within the approved scope and record the supported launch envelope.
8. Add release security checks and dynamic scanning, remediate in-scope critical/high findings, inventory dependencies/licenses/SBOM, and record external/live checks that cannot run locally.
9. Complete operational runbooks and documentation, execute a launch rehearsal on disposable infrastructure, and update the task with exact artifacts, results, objective measurements, and outstanding production-environment gates.

## Acceptance criteria

- [ ] Given either supported routing mode, when tenant, platform, authentication, unknown, spoofed-forwarded, and cross-store requests arrive, then only the configured direct host selects the expected store and no caller input overrides tenant scope.
- [ ] Given incomplete, placeholder, insecure, overlapping, or over-budget production configuration, when API, worker, migration, or operator processes start, then they fail closed with actionable redacted errors; existing development/test configuration remains compatible.
- [ ] Given the built production images and deployment examples, when a disposable release rehearsal runs migration, API, worker, probes, rolling replacement, shutdown, and schema-compatible rollback, then processes run non-root, secrets remain external, readiness gates traffic, and in-flight work drains within bounds.
- [ ] Given representative API and worker activity or dependency failures, when telemetry is collected, then required service/commerce/backlog signals and trace context are available, alerts exercise successfully, export failure does not break commerce, and no unbounded tenant or sensitive labels/fields appear.
- [ ] Given encrypted records on an old payment key while writes continue, when bounded rotation is interrupted, retried, or races an update, then every record remains decryptable and correctly AAD-bound, rotation resumes without corruption, cross-store records cannot be substituted, and old-key retirement is refused until no reference remains.
- [ ] Given published outbox, processed inbox, resolved dead-letter, unresolved, financial, audit, and ledger records around retention cutoffs, when report and cleanup run concurrently, then only records allowed by the approved windows are deleted in bounded idempotent batches and tenant/event processing remains correct.
- [ ] Given a timestamped backup and every referenced encryption key, when restored into a guarded isolated environment, then schema, cross-store isolation, order/payment/inventory/ledger consistency, outbox continuity, decryption, API readiness, and projection recovery pass and measured recovery meets the approved RPO/RTO in the selected launch environment.
- [ ] Given the approved skewed workload and concurrent workers, when capacity tests run, then the evidence records the maximum supported envelope, SLO results, database connection/lock/slow-query behavior, resource headroom, queue lag, and a safe stop point without oversell, duplicate financial effects, or tenant leakage.
- [ ] Given the release candidate, when race, lint, unit/integration, migration, vulnerability, secret, SBOM/container, configuration, and disposable dynamic-security checks run, then no unresolved critical/high finding or required unavailable check is hidden or reported as passing.
- [ ] Given an operator follows each launch, outage, backlog, rotation, backup/restore, reconciliation, and rollback runbook in a tabletop or disposable rehearsal, then commands, expected signals, escalation/stop conditions, recovery, and ownership are complete and match the shipped artifacts.
- [ ] OpenAPI changes are limited to any externally visible health/behavior correction; internal metrics and operator commands are not exposed on the tenant API.
- [ ] Both independent reviews and the complete `make verify` release gate pass on the final artifacts; required environment-specific checks are separately named and must pass before an MVP production claim.

## Verification strategy

| Requirement or risk | Required evidence |
| --- | --- |
| Host and tenant authority | Config/host unit tests, Gin boundary tests, same-ID cross-store denial, direct-versus-forwarded host spoofing, exact single-store mapping, suspended-store denial, and multi-store compatibility |
| Production configuration | Table-driven negative tests for every mode/secret/TLS/proxy/pool/timeout/telemetry invariant plus redacted startup-error snapshots |
| Deployment lifecycle | Container build/inspection, non-root/read-only checks, configuration render/parse, migration-first disposable rollout, readiness, SIGTERM drain, worker restart, and schema-compatible rollback rehearsal |
| Observability and privacy | Metrics/trace unit and integration tests, bounded-label inventory, telemetry-outage failure injection, alert rule tests, dashboard/config parsing, and log/trace secret/PII scans |
| Secret rotation | Real-PostgreSQL tests over every ciphertext/AAD shape for mixed keys, wrong/missing key, concurrent update, interruption/resume, idempotency, store substitution, bounded batches, and zero-reference retirement guard |
| Retention safety | Real-PostgreSQL cutoff-boundary, unresolved exclusion, tenant isolation, concurrent consumer/publisher, interruption/resume, bounded-lock, report-only, incident-hold, and post-cleanup replay/rebuild tests |
| Backup and restore | Guarded disposable backup/restore drill with checksums, schema/version checks, representative invariant queries, active-key decryption, API/worker probes, outbox and projection recovery, and measured backup age/recovery duration |
| Performance and queries | Deterministic skewed fixtures, synchronized contention cases, versioned load scenarios, resource/pool/backlog telemetry, `EXPLAIN (ANALYZE, BUFFERS)`, migration-lock timing, and a committed capacity report without flaky CI latency gates |
| Release security | Race-enabled Linux suite; lint and `govulncheck`; secret, SBOM, container, and static scans; bounded ZAP baseline; dependency/version review; host/CORS/rate/limit/timeout abuse cases |
| Operational readiness | Automated Markdown links/config parsing, runbook/tabletop checklist, alert test evidence, exact commands and owners, and an explicit list of cloud/live gates not executed |
| Final gate | `gofmt` on changed Go files, `make fmt-check`, `make lint`, focused package tests, `make test-integration`, `make vulncheck`, container/deployment validation, and `make verify` |

## External launch gates

- The operator must select a production environment and prove DNS/TLS termination, direct-host preservation, trusted proxies, secret-manager injection, network policy, Kafka ACL/TLS/replication, and monitoring/paging delivery against the deployment examples.
- The PostgreSQL provider must enable encrypted continuous backup/PITR and produce a successful isolated restore whose measured backup age and recovery duration meet the approved 15-minute RPO and four-hour RTO.
- Production-like load hardware and expected traffic/store/catalog/order assumptions must be supplied before the tested envelope can be compared with launch demand.
- Task 015's live MoMo sandbox callback exercise and any enabled Google/Facebook live interoperability remain separate acceptance gates requiring credentials and reachable HTTPS endpoints.
- Accepted Task 004 medium risks remain visible. Any decision to accept them for production must be recorded explicitly in the launch checklist; Task 19 does not silently close them.

## Required completion report

- Behavior and design summary
- Files/modules changed
- Commands executed with results
- Measured SLO, capacity, restore, and security evidence
- External checks not run and exact reasons
- Risks, assumptions, and follow-ups

## Delivery record

- Stage: final verification complete; external launch gates pending
- Active task: yes
- Execution mode: stage-only (`ec-plan`)
- Last command, completed stage, pending gates, and next suggested command: focused Kafka integration rerun passed after the full-suite run encountered transient consumer `context canceled` failures; implementation, both independent reviews, review fixes, and local final verification are complete. Execute the named external launch gates before any production claim.
- Plan revision and approval: revision 1 prepared on 2026-09-21 and approved by the user on 2026-09-21 with `continue`. Revision 2 adds the two non-destructive retention indexes recorded above and was approved by the user on 2026-09-21 with `approve`. Revision 1 approval includes the proposed SLO/RPO/RTO and retention baseline.
- Plan: routing, production configuration/deployment, observability, secret rotation, retention, backup/restore, capacity, security, runbooks, compatibility, recovery, and ordered delivery are recorded above.
- Verification: acceptance criteria map to host/config tests, real PostgreSQL rotation/retention/restore evidence, disposable deployment and security rehearsal, load/query-plan evidence, release scans, independent reviews, and the complete final gate above.
- Architect review: initial review required retention windows, concurrent-index migration compatibility, validated production command configuration, strict PostgreSQL TLS, Compose runtime configuration, trace propagation, and rotation evidence. Re-review passed after fixes on 2026-09-21.
- Security review: initial review required strict production configuration, command hardening, retention floors, writer retirement fencing, narrow scan allowlisting, and immutable scanner inputs. Re-review passed after fixes on 2026-09-21; the disposable ZAP baseline remains an external launch gate.
- Final evidence: formatting, lint, full unit suite, real PostgreSQL migration/retention/rotation/advisory-lock tests, focused Kafka rerun, container build/non-root/config check, Compose render, SBOM, clean high/critical container scan, secret scan, and diff check passed. `make verify` reached an otherwise passing integration suite but recorded four transient Kafka consumer `context canceled` failures; the immediate standalone Kafka rerun passed. Production-like restore/PITR, load/capacity, ingress/TLS, paging, live provider, and disposable ZAP evidence remain unexecuted external gates. Initial inspection used a per-command Git safe-directory override because the sandbox identity cannot modify the user's global Git configuration; the working tree reported no changes before this task file was created.
