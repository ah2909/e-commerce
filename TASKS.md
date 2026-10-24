# Remaining backend work

Reviewed 2026-10-24 against repository code, migrations, operational docs, and the previous task records. This file owns the remaining backend backlog and replaces the task directory, development-status snapshot, and implementation roadmap. Frontend work stays in the sibling repository.

Commerce APIs, event workers, and local operational tooling exist. Live provider and deployment evidence is still incomplete; the backend is not certified production-ready. Goose migrations currently run through 00015 (store appearance in 00014 and optional address district in 00015). Historical test results below are carried forward from prior records, not rerun during this documentation cleanup.

## Before production

### 019 ? Operational launch evidence

Local implementation and both reviews were recorded complete on 2026-09-21. Final local evidence included formatting, lint, unit tests, PostgreSQL tests, container checks, Compose rendering, SBOM, container scan, and secret scan. The recorded `make verify` run had four Kafka consumer `context canceled` failures; a focused Kafka rerun passed. This does not establish a clean full release gate on the current tree.

- [ ] Run a clean release-candidate `make verify` and required image/security checks; record exact results and resolve failures.
- [ ] Select the production environment and owners; prove DNS/TLS, direct-host preservation, trusted proxies, secret injection, network policy, Kafka TLS/SASL/ACLs and retention, and monitoring/paging delivery.
- [ ] Rehearse migration-first rollout, readiness, graceful drain, and schema-compatible rollback with the selected image and current schema.
- [ ] Prove encrypted PostgreSQL continuous backup/PITR and an isolated restore, including tenant denial, commerce invariants, all referenced encryption keys, API/worker readiness, and projection recovery. Measure RPO <= 15 minutes and RTO <= four hours.
- [ ] Supply a versioned synthetic load scenario and skewed fixtures; none is currently checked in. Measure capacity, query plans, pool/lock pressure, worker backlog, resource headroom, and migration locking against forecast demand. Targets: 99.5% monthly availability, non-provider API p95 < 500 ms, checkout p95 < one second.
- [ ] Run the bounded disposable ZAP baseline and complete live-provider checks for every enabled provider.
- [ ] Record a launch decision covering the deferred Identity risks below; continued-development acceptance does not approve production risk.

Use [production deployment](docs/production-deployment.md), [operations](docs/operations-runbook.md), and [capacity/security evidence](docs/capacity-and-security.md) for procedures, stop conditions, and required artifacts. Event retention floors remain 30 days for Kafka/published outbox/processed inbox and 90 days for resolved dead letters; unresolved and financial/audit records are excluded.

### 015 ? Live MoMo sandbox acceptance

Implementation, both reviews, and automated `make verify` were recorded passing on 2026-09-15. The live payment and signed sandbox callback have never been recorded passing.

- [ ] Supply the required `MOMO_SANDBOX_*` process configuration, sandbox merchant/test-wallet credentials, an isolated pending test order with an active reservation, and a MoMo-reachable HTTPS callback.
- [ ] Run `make test-momo-sandbox`, complete the test-wallet payment, and verify the signed callback, API/database payment and order state, reservation/ledger effects, and duplicate-IPN replay without duplicate effects.

The exact inputs and runner are documented in [README](README.md#database-operations). Keep credentials outside Git.

### Enabled Google/Facebook acceptance

- [ ] Exercise real provider login/callback/exchange against the selected HTTPS ingress when either social provider is enabled; local fixtures do not establish live interoperability.

## Deferred risks ? inactive unless reopened

### 004 ? Identity and authorization

The user explicitly deferred two medium review findings on 2026-09-10 for continued development. Neither is recorded fixed or reviewer-approved; no fix stage is scheduled.

- [ ] Use database-authoritative credential expiry to remove API-clock skew from expiry decisions.
- [ ] Make store suspension versus session issuance atomic.

If reopened, retain tenant denial and deterministic concurrency regression evidence, obtain the affected independent re-reviews, and run risk-appropriate verification before marking either resolved.

## After the MVP production gate

- [ ] Coupons and redemption accounting: store-owned fixed/percentage discounts, deterministic eligibility and integer rounding, validity/usage limits, idempotent redemption, immutable order discount snapshots, and tenant/concurrency tests. Do not start this as part of launch hardening.

## Maintaining this file

Keep only remaining work and unresolved risk decisions. Add plans, approval references, stage, findings, and exact verification evidence under the relevant entry when the optional [delivery workflow](docs/development-workflow.md) is invoked. Use existing identifiers for carried-forward work; add a descriptive heading for new work. Remove completed entries after preserving any continuing operational constraint in its owning design document or runbook. Do not recreate `tasks/` or duplicate frontend task records.
