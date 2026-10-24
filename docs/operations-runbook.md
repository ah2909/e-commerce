# Operations runbook

## Signals and ownership

The service owner handles API errors, latency, payment/reconciliation failures, and release rollback. The data owner handles PostgreSQL capacity, backup, restore, locks, and migrations. The messaging owner handles Kafka availability, ACLs, lag, and topic retention. The security owner handles secret rotation and scan findings. An alert is not complete until its receiver and escalation path are recorded in the selected environment.

Use `deploy/prometheus/alerts.yml` and `deploy/grafana/ecommerce-overview.json` as provider-neutral starting points. Metrics use bounded route, method, status, operation, result, and state labels; never add store, user, order, payment, email, phone, token, or provider-reference labels. `/health/live` proves only process life. `/health/ready` covers serving dependencies.

## Dependency outage

For PostgreSQL or Redis readiness failure, stop routing new traffic, preserve logs and metrics, and page the service/data owner. Do not retry checkout outside its existing idempotency contract. For Kafka failure, leave the API serving, confirm outbox age and terminal counts, stop retention, and restore publishing before replaying terminal work. Telemetry collector failure is warning-only because trace export is bounded and non-blocking.

Recovery is complete only when readiness is stable, database pool waits stop increasing, the oldest due outbox age is below 60 seconds, no unexplained terminal rows remain, and a synthetic transaction succeeds.

## Backlog and reconciliation

Inspect without payloads:

```text
/event-reconcile status
/event-reconcile list-outbox <store-id> 100
/event-reconcile list-dead-letters <store-id|unscoped> 100
```

Correlate identifiers with bounded logs. Fix the dependency or contract first. Requeue only a known terminal outbox row and replay only a validated dead letter:

```text
/event-reconcile requeue-outbox <store-id> <event-id> <reason>
/event-reconcile replay-dead-letter <store-id> <dead-letter-id> <reason>
```

Stop if the store or event binding is uncertain, errors accelerate, the same record repeatedly terminalizes, or a money/inventory invariant is in doubt. Payment reconciliation remains an authoritative manual queue; event replay must never synthesize a payment transition.

## Payment encryption-key rotation

Add a new random 32-byte key version while retaining the old key, deploy readers, select the new active writer version, and inspect references:

```text
/payment-key-rotate status
/payment-key-rotate rotate <old-version> <active-new-version> 100
/payment-key-rotate verify-retired <old-version>
```

Rotation authenticates each provider config, attempt snapshot, and callback route with its original AAD, locks bounded rows, and can be restarted. Current API writers hold a PostgreSQL shared advisory lock for their active key version; `verify-retired` requires the exclusive old-version lock and then requires zero references. Deploy this locking-capable release and drain every old API replica before rotating. Do not remove the old key until `verify-retired` returns zero in every environment and a backup containing the old version has expired or retains the key. On failure, keep both keys, stop rotation, and investigate; do not overwrite or log ciphertext/plaintext. Rotate the platform token and provider deployment secrets by replacement plus coordinated restart. The static platform token has no overlap protocol, so drain platform administration during replacement.

## Event retention

Kafka event topics, published outbox rows, and processed inbox rows have an approved 30-day floor. Resolved dead letters retain 90 days. Unresolved/terminal events and all financial, order, inventory-ledger, audit, webhook, reconciliation, and refund records are excluded.

Keep `EVENT_RETENTION_HOLD=true` during incidents, restores, or replay investigations. Generate UTC RFC 3339 cutoffs from database/operator time, run status first, archive the counts, then execute one bounded batch with the exact same cutoffs:

```text
/event-reconcile retention-status <published-before> <processed-before> <resolved-before>
/event-reconcile retention-clean <published-before> <processed-before> <resolved-before> 500 delete-approved-retention
```

The command rejects cutoffs within one hour of database time and deletes only explicit terminal shapes. Repeat status between batches. Stop on unexpected count growth, lock pressure, replica lag, missing retention indexes, or an active incident.

## Backup and restore

The selected PostgreSQL service must provide encrypted continuous backup/PITR with a measured recovery point no older than 15 minutes. The portable verification artifact is created by `ops/backup-postgres.sh`; it requires `pg_dump`, `age`, and `sha256sum` and writes with a restrictive umask.

Restore only into a newly created loopback database whose name ends `_restore_test`:

```text
DATABASE_URL=<source> BACKUP_OUTPUT=<timestamp>.dump.age BACKUP_AGE_RECIPIENT=<recipient> sh ops/backup-postgres.sh
RESTORE_DATABASE_URL=<loopback-restore-url> BACKUP_INPUT=<timestamp>.dump.age BACKUP_AGE_IDENTITY=<identity-file> sh ops/restore-drill.sh
```

After the script, verify the restored schema matches the selected release (currently through migration 00015), representative cross-store denial, order/payment and stock/ledger invariants, outbox continuity, and decryption using every referenced key version. Start API and worker against the disposable restore, check readiness, rebuild Search/Analytics/email-intent projections from retained events or authoritative tables, and record backup timestamp, restore start/end, RPO, RTO, row counts, image digest, and operator. Destroying the disposable target requires separate authorization; the scripts never modify the source.

## Incident and rollback checklist

Declare impact and incident owner; freeze deployments, retention, and key removal; preserve request/correlation/trace IDs without personal data; identify affected stores internally without adding them to metrics; and select the narrowest reversible action. A cross-store disclosure, money mismatch, oversell, signature bypass, or unrecoverable data loss is an immediate security/service page. Recovery requires invariant checks and backlog drain. Document the timeline, commands, evidence, and follow-up owner before closing.
