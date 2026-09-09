# Development status

## Snapshot

- Assessed: 2026-09-09
- Implemented baseline: commit `a78d970c7e8658faa42ac9c998567d822d6bee4d`, plus the current task 005 documentation-fix working tree
- Target baseline: [product requirements](product-requirements.md), [system architecture](architecture.md), [domain model](domain-model.md), and [database design](database-design.md)
- Current release verdict: **not production-ready**

This is the authoritative current-state summary. Detailed historical evidence remains in [task records](../tasks/README.md). A feature is complete only when its approved acceptance criteria, independent architecture/security reviews, and final verification pass on the same relevant artifacts.

## Status meanings

| Status | Meaning |
| --- | --- |
| Implemented and accepted | Required behavior exists and its task records passing review and final verification |
| Implemented, awaiting review | Required behavior and focused checks exist, but independent review and final verification are pending |
| Implemented, changes required | Substantial code exists, but correctness, security, changed requirements, review, or verification blocks acceptance |
| Partially implemented | Only part of the target capability exists |
| Not started | No meaningful domain implementation exists; infrastructure placeholders do not count |

## Roadmap status

| Slice | Status | Evidence and gap |
| --- | --- | --- |
| Foundation | Implemented and accepted | API/worker entry points, configuration, logging, health checks, Compose dependencies, migration runner, CI, Make verification targets, and guarded integration database are recorded complete in [task 001](../tasks/archive/001-bootstrap.md). Current working-tree checks are recorded below. |
| Tenant context | Implemented, changes required | Store persistence, tenant middleware, and the task 003 isolation baseline exist. Task 004 changed host and session behavior; re-review completed with the two deferred medium findings below. Current routing implements shared-base-domain subdomains, not the target customer-managed exact-host single-store mode. |
| Platform administration | Implemented, changes required | The protected base-domain route requires an explicitly supplied deployment platform token and atomically creates a sessionless owner account, membership, and audit record. Consolidated checks passed, but the store-suspension/session race keeps the shared security boundary unaccepted. |
| Identity and workforce authorization | Implemented, changes required | Store-owned users, passwords, Google/Facebook adapters, opaque sessions, guests, memberships, roles, permission middleware, bound Redis state, trusted-proxy-aware rate limits, audit, host-scoped routes, migration 2, and OpenAPI exist. Consolidated verification passed; database-authoritative credential expiry and atomic suspension/session admission remain explicitly deferred. |
| Catalog | Not started | No Catalog module or catalog schema exists. |
| Pricing and coupons | Not started | No Money value object, Pricing module, price/coupon schema, or contract exists. |
| Inventory | Not started | No stock item, reservation, ledger, expiry worker, or concurrency implementation exists. |
| Customers and carts | Not started | Guest identity exists, but customer profiles, addresses, carts, and cart ownership do not. |
| Checkout and orders | Not started | No checkout transaction, order snapshots, state machine, idempotency record, or guest order grant exists. |
| VietQR payments | Not started | No store provider configuration, QR instruction, audited payment confirmation, or reconciliation implementation exists. |
| MoMo payments | Not started | No provider adapter, signed webhook, attempt, receipt, or automatic confirmation implementation exists. |
| Cancellation and refunds | Not started | No order cancellation or full-refund workflow exists. |
| Shipping and fulfillment | Not started | No flat/free shipping rule, shipment, carrier, or tracking implementation exists. |
| Event workers | Not started | Kafka is available in local Compose and a worker process starts, but no outbox/inbox schema, publisher, consumer, or event contract implementation exists. |
| Operational hardening | Partially implemented | Structured logging, health checks, bounded shutdown/dependency configuration, CI, and local dependency controls exist. Backup/restore, dashboards, alerts, secret rotation, load/security tests, launch SLO/RPO/RTO, and production runbooks are not implemented or proven. |

## Implemented database schema

### Migration 00001

- stores with immutable unique subdomain, active/suspended status, regional defaults, version, and UTC timestamps
- store memberships keyed by store and user

### Migration 00002

- store-owned users, password credentials, social identities, user sessions, and access/refresh token digests
- store-bound guests and rotating guest token/session records
- membership roles
- limited Store/membership audit records with typed platform or tenant actors

The schema intentionally does not include platform operators. It also does not include Catalog, Pricing, Inventory, Customers, Carts, Orders, Payments, Refunds, Shipping, outbox, or inbox tables.

## Current acceptance blockers

Task 004 revision 5 implements the corrections requested by the prior reviews and the latest finding fixes: explicit platform-token injection, disabled-user refresh-family revocation, current-state social session admission, non-enumerating malformed membership handling, and bounded unauthenticated base64url parsing.

Independent architecture and security re-review completed with changes required. On 2026-09-09, the user explicitly deferred two medium findings for the initial MVP: API-clock-based credential expiry can diverge under clock skew, and store suspension is not atomic with session issuance. On the same date, the user elected to skip further task 004 work for now. The findings remain unresolved and prevent full task acceptance or production use. Residual validation also remains for live Google/Facebook interoperability, real ingress proxy configuration, customer-managed exact-host routing, production-size migration locking, race-enabled CI, and production secret rotation.

## Alignment that is already present

- Host-selected store context and immutable tenant subdomains are represented in current code and migration 1.
- User, credential, session, token, guest, membership, and audit persistence uses store-aware keys in migration 2.
- Same email and social provider subject are designed to coexist across stores.
- Access/refresh tokens are opaque and stored as digests; session rotation and reuse handling have focused tests.
- User and guest principals are distinct, and permissions deny by default.
- Membership targeting uses tenant-local user ID rather than unverified email.
- PostgreSQL, Redis, configuration, provider fixtures, API contract, and concurrency tests exist for substantial Identity behavior.
- No downstream commerce module falsely appears implemented.

These positives do not waive the blockers above.

## Public API status

[api/openapi.json](../api/openapi.json) describes the current Identity, guest, store, membership, health, and onboarding endpoints. It is an implementation contract for unfinished task 004, not the complete MVP API.

`POST /api/v1/onboarding/stores` is base-domain-only, requires `platformAdminBearer`, and returns store and owner identifiers without issuing an owner session.

Catalog, pricing, inventory, customer, cart, checkout, order, payment, refund, shipping, and event-administration endpoints are absent because those slices are not implemented.

## Verification evidence

Task 005 ran current checks on 2026-09-08 against Go 1.26.6 on Windows:

| Command/check | Result |
| --- | --- |
| `make fmt-check` | Passed; golangci-lint emitted a sandbox working-directory warning but returned success |
| `make lint` | Failed with 12 findings in unfinished Identity/platform code |
| `make test` | Passed for all packages; native Windows Make configuration does not enable the race detector |
| `make test-integration` | Passed after starting disposable PostgreSQL, Redis, and Kafka; migrations were already at version 2 |
| `make vulncheck` | Passed outside the restricted sandbox; no called-code vulnerabilities found |
| Local Markdown link validation | Passed |
| ADR identifier uniqueness | Passed with ADRs 001–004 |
| `git diff --check` | Passed |
| GitHub Actions YAML parse | Unavailable because no YAML parser or actionlint is installed; only a message path changed |

Task 004 revision 5 addresses the recorded lint findings. The task 005 table above remains historical system-wide evidence and is not presented as the current result.

Task 004 build-stage evidence on 2026-09-09: `make test` passed, `make lint` reported zero issues, `make fmt-check` passed, and `git diff --check` passed. A clean guarded test reset applied migrations 00001 and revised 00002, the full `make test-integration` suite passed, repeat migration was a no-op at version 2, and focused post-change tests passed for populated version-1 upgrade, sessionless onboarding/audit rollback, Store authorization, API isolation, and atomic Redis social consumption. This is development evidence, not the independent-review or final-verification gate.

Previous task 004 focused evidence also reports fresh disposable migrations, concurrency/provider fixtures, and repeated integration runs. Its independent reviewers returned changes required. Neither previous nor current passing checks replace final checks after task 004 is fixed, independently re-reviewed, and verified.

Task 004 fix-stage evidence on 2026-09-09: focused unit packages passed; migration status was already version 2; focused real PostgreSQL tests passed for active refresh concurrency, disabled-user concurrent refresh/revocation/reactivation, and stale social-session denial after user disablement or store suspension. Independent re-review confirmed the prior fixes but identified the two deferred medium findings above. This is not the final verification gate.

Task 004 consolidated MVP verification on 2026-09-09: full unit and integration suites passed before a tooling-only Makefile change and logger-only test cleanup; afterward, formatting, zero-issue lint, vulnerability scanning with no called-code vulnerabilities, and the affected Stores HTTP package test passed. Both reviewers approved the later changes, and the user approved reusing the unaffected full-suite evidence instead of repeating integration tests. Verification is accepted for continued MVP development, while the two deferred findings still prevent full workflow acceptance.

## Required next work

1. Fix and re-review task 005 documentation standardization, then run its final documentation checks.
2. Begin Catalog with task 004 explicitly treated as unaccepted and deferred.
3. Before production, reopen task 004 to address database-authoritative expiry and atomic suspension/session admission.

No production deployment should proceed until at least the required MVP slices and operational hardening pass their gates.
