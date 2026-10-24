# Capacity, query, and security evidence

## Unmeasured launch envelope

No launch hardware, traffic forecast, store count, catalog size, or tenant-skew distribution has been supplied. Repository checks therefore establish bounded behavior but do not establish production throughput, the 99.5% SLO, the 15-minute RPO, or the four-hour RTO. Configured limits are not a measured safe capacity envelope: database connections are capped at 200 per process, event batches at 500, retention batches at 1,000 per eligible table, and payment rotation batches at 500. No versioned load scenario is currently checked in; supply one before claiming a tested workload envelope.

Before launch, create deterministic synthetic tenants with one tenant holding at least 80% of products/orders and no real personal or credential data. Exercise storefront reads, login throttling, cart mutation, checkout contention on last stock, admin reconciliation lists, and concurrent event/expiry pressure. Increase load in steps and stop immediately on tenant leakage, oversell, duplicate financial effects, p95 breach sustained for five minutes, error rate above 1%, pool use above 90%, increasing pool waits, lock timeout growth, oldest due outbox above 60 seconds, or CPU/memory above 80% for ten minutes.

Record image digest, schema, fixture cardinalities, replica/resource/pool settings, request mix, throughput, p50/p95/p99, errors, pool waits, locks, CPU/memory, database growth, outbox/dead-letter age, expiry lag, and correctness queries. The maximum supported envelope is the last step below every stop threshold with at least 20% resource headroom.

## Slow-query and migration evidence

Enable redacted slow-query duration logging at `SLOW_QUERY_THRESHOLD`; SQL text and parameters are intentionally absent. During the representative run, identify query shapes from code and database statistics, then capture `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` in the disposable environment with sanitized bind values. Store plans and migration timing in the release evidence bundle, not in logs containing customer data.

For migration 13, measure both index builds on a production-sized restored copy while API and worker traffic run. Record table/index bytes, total duration, lock waits, blocked statements, replica lag, and rollback/forward-fix decision. If ordinary creation exceeds the change window, create the identical indexes concurrently as an approved non-transactional pre-step and do not enable cleanup until both are valid.

## Security evidence

The release workflow builds the runtime image, emits a CycloneDX SBOM, scans high/critical container findings, and scans repository history/content for secrets. `make verify` covers format, static analysis, unit/integration/race behavior where supported, and called-code vulnerability analysis. `make zap-baseline` is bounded to two minutes and must target only a disposable API.

Review findings by CVE/rule, affected component, reachability, exploitability, owner, and deadline. A false positive needs written evidence and an expiry; do not silently suppress it. `.gitleaks.toml` contains only exact deterministic payment-test strings and the intentionally public development platform token, which production configuration rejects. Critical/high reachable findings, a failed configuration-negative test, a secret finding, or any unavailable required check blocks the release. Host spoofing, exact CORS origins, credential throttling, HTTP header/body/time limits, production TLS requirements, log/trace redaction, and non-root/read-only container settings are explicit abuse cases for the release rehearsal.

The external evidence bundle must also include ingress host behavior, TLS scan, Kafka ACLs and retention, database PITR/restore results, monitoring/paging delivery, and any enabled Google/Facebook/MoMo live interoperability. These checks require selected infrastructure or credentials and cannot be inferred from repository-local tests.
