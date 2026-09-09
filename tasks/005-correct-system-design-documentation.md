# Task: Standardize system design, repository documentation, and development status

## Context

The requirements interview refined and corrected the initial product direction in [product requirements](../docs/product-requirements.md), [architecture](../docs/architecture.md), [domain model](../docs/domain-model.md), [event catalog](../docs/event-catalog.md), [roadmap](../docs/implementation-roadmap.md), [testing strategy](../docs/testing-strategy.md), and the accepted decisions under [`docs/decisions`](../docs/decisions/).

The system serves independent businesses. It starts with Vietnam-focused physical-goods commerce, runs as subdomain-isolated SaaS or as a customer-managed deployment, and must remain maintainable as data volume and traffic grow. The existing Identity and authorization implementation is in progress. This task audits it but does not rewrite or discard it.

The repository initially contained about 496 MB of ignored Go build cache under `.cache/` and about 207 MB of ignored reproducible tools under `.tools/`. Completed task records were mixed with active tasks, the outbox and identity ADRs both used number 003, and no authoritative document reported what was implemented, incomplete, incorrect against the new requirements, or not started. The user later chose to retain `.tools/` because the pinned binaries are used frequently during verification.

## Outcome

The repository has one coherent, navigable design baseline describing the intended product, system boundaries, database model, critical commerce flows, security controls, scaling path, and operational expectations. Documentation and delivery records follow an explicit structure, disposable Go cache is removed, the reusable pinned tool cache is retained intentionally, and an evidence-backed status document distinguishes implemented and verified work, partial or incorrect work, and future work.

## In scope

- Correct the existing product, architecture, domain, event, roadmap, testing, and relevant ADR documents.
- Add `docs/database-design.md` because no current document owns the relational model, tenant key strategy, constraints, indexes, retention, and growth plan.
- Document independent stores, host-selected tenancy, platform-admin-only store provisioning, guest checkout, Vietnam-first commerce, and archive-based retention.
- Document physical products with categories, variants, images, SKU, barcode, weight, dimensions, store pricing, and variant inventory.
- Document ten-minute inventory reservations, VietQR manual confirmation and reconciliation, MoMo automatic signed-webhook confirmation, full refunds, cancellation, simple shipping, and guest order access.
- Document long-term scale mechanisms and explicit evidence-based triggers without introducing premature microservices, sharding, or partitioning.
- Correct document navigation, terminology, ADR numbering, and contradictions exposed by the interview.
- Add `docs/development-status.md` as the single current-state report, derived from code, migrations, contracts, tests, task records, and newly executed verification evidence.
- Add `tasks/README.md`, archive completed task records `000` through `003` under `tasks/archive/`, retain tasks `004` and `005` as incomplete work, and update every repository reference affected by those moves.
- Remove the ignored disposable `.cache/` directory after verification and retain the ignored pinned `.tools/` cache for frequent checks.
- Simplify the root README into a reliable operator/developer entry point that links to authoritative documents rather than duplicating changing design and workflow state.
- Audit the current implementation against the corrected requirements and record concrete alignment, gaps, failed checks, and the next required task without repairing application code.

## Out of scope

- Go code, migrations, OpenAPI behavior, runtime configuration behavior, infrastructure behavior, or test implementation. Reference-only path updates caused by archiving completed tasks are allowed.
- Self-service store onboarding, SaaS subscription billing, custom domains, marketplaces, digital goods, subscriptions, B2B, multiple warehouses, backorders, partial refunds, and automated shipping providers.
- Production deployment, migration execution, benchmarking, capacity certification, or a promise of a particular throughput without workload evidence.
- Rewriting, deleting, or otherwise discarding the uncommitted Identity and authorization work.
- Deleting Git history or completed task records; completed records are archived as approved by the user.
- Removing `.agents/`, `.codex/`, `.github/`, `dev/`, application source, migrations, contracts, tests, or reproducibility files.

## Invariants and security constraints

- Every tenant-owned record, query, uniqueness rule, cache key, job, and event is scoped by server-resolved `store_id`.
- A store is an independent business; identities, customers, catalog, inventory, orders, payments, provider configuration, and audit history are not shared across stores.
- Store scope comes from a validated subdomain in SaaS mode or trusted deployment configuration in customer-managed mode, never from caller-supplied payload fields or forwarded host headers.
- Only an authenticated platform administrator can provision a store; store owners and tenant staff cannot create tenants.
- Money uses integer minor units and an ISO currency code; Vietnam-first defaults do not permit floating-point money.
- Inventory reservation and release are atomic, auditable, and idempotent; stock cannot be oversold.
- VietQR confirmation and financial reconciliation are explicit audited staff actions. MoMo callbacks are signature-validated and idempotent.
- Payment provider credentials are encrypted at rest, never returned after entry, never placed in logs/events, and decryptable only by the payment adapter path.
- Orders persist product, price, discount, address, shipping, and tax snapshots. Archiving source records cannot change history.
- Checkout commits against PostgreSQL without depending on Kafka or an external payment provider.

## Acceptance criteria

- [x] The documented MVP consistently reflects all requirements agreed in the interview and has no self-service-onboarding contradiction.
- [x] The architecture assigns every capability and table to one module and describes allowed synchronous interfaces and asynchronous events.
- [x] The database design defines tenant keys, composite foreign keys, constraints, query-driven indexes, keyset pagination, transaction boundaries, archival, and high-volume table growth policies.
- [x] The checkout, ten-minute reservation, VietQR, MoMo, cancellation, expiration, reconciliation, and full-refund state transitions are unambiguous, including late and duplicate events.
- [x] Guest checkout and opaque guest order access do not expose an order through guessable identifiers or cross-store credentials.
- [x] SaaS and customer-managed deployment modes share the same application architecture while their tenant-resolution and operating responsibilities are explicit.
- [x] Scaling guidance separates immediate design practices from measurable triggers for replicas, partitioning, tenant placement, and service extraction.
- [x] Security, audit, backup, observability, failure/retry, and Vietnam-first data policies are represented in the architecture and verification strategy.
- [x] Existing implementation behavior that conflicts with the corrected design is listed as follow-up work rather than described as complete.
- [x] `docs/development-status.md` reports each roadmap slice as implemented and verified, implemented but incomplete/incorrect, or not started, with evidence and no unsupported completion claims.
- [x] Completed tasks `000` through `003` are under `tasks/archive/`, incomplete tasks remain discoverable, and all moved-path references resolve.
- [x] The ignored disposable `.cache/` directory is removed after commands that depend on it finish; source and feature work remain intact.
- [x] The keep/delete decision is explicit: `.tools/` is retained as a frequently used pinned tool cache; superseded duplicate files are removed; workflow configuration, source, tests, contracts, migrations, skills, agent definitions, and task history are retained.
- [x] Changed Markdown links resolve locally, headings and terminology are consistent, and `git diff --check` passes.

## Proposed documentation and technical solution

1. Revise `docs/product-requirements.md` into an authoritative MVP contract: independent tenants; platform-admin provisioning; Vietnam/VND physical commerce; guests; catalog/variant needs; flat/free shipping; cancellation; full refunds; VietQR and MoMo behavior; explicit exclusions and success criteria.
2. Expand `docs/architecture.md` with system context and runtime flows, module ownership, a separate platform control-plane boundary, SaaS and customer-managed deployment topology, failure boundaries, secrets, observability, backup/recovery baseline, and an evidence-based scale path. Keep the API and worker stateless and PostgreSQL authoritative; keep Kafka outside checkout commits.
3. Revise `docs/domain-model.md` with concrete lifecycle rules for catalog, inventory, carts, orders, payments, shipping, guests, provider configuration, reconciliation, refunds, and archival. Define guarded order/payment/reservation state transitions and Vietnam-first money/tax assumptions.
4. Add `docs/database-design.md` as the logical relational source of truth. Use module-owned shared PostgreSQL tables; composite tenant keys and tenant-matching foreign keys; integer money; integer physical-item quantity; UTC timestamps; optimistic versions; immutable ledgers/snapshots; selective JSONB; store-leading indexes; keyset pagination; and query-specific partial indexes. Retain the existing UUID identifier convention and require evidence before changing identifier type.
5. In the database growth section, keep normal transactional tables unpartitioned initially. Define monitoring and migration triggers for partitioning append-heavy audit/outbox/ledger/webhook data, adding read replicas for stale-tolerant reads, isolating exceptionally large tenants, and extracting a module only when ownership, scaling, release, failure, or regulatory evidence justifies it.
6. Correct `docs/event-catalog.md` with reservation, order-expiration, payment-confirmation/failure, reconciliation, cancellation, refund, shipment, and archival events. Specify minimal versioned envelopes, ordering expectations, producer ownership, outbox atomicity, consumer inbox deduplication, and personal-data restrictions.
7. Reorder and clarify `docs/implementation-roadmap.md` so catalog, pricing, inventory, customer/guest cart, checkout, VietQR, MoMo, refunds, shipping, workers, and operational hardening are deliverable vertical slices. Record the provisioning mismatch found in task 004 revision 4; task 004 revision 5 subsequently replaced the public route with platform authentication.
8. Extend `docs/testing-strategy.md` to map the corrected invariants to unit, PostgreSQL concurrency, API, provider-contract, event-idempotency, security, recovery, and later load tests. Include cross-store denial for every resource and failure cases for expired/late/duplicate payments.
9. Update relevant ADRs rather than hiding changed decisions: preserve the modular monolith and shared-schema choices, align the identity/host-selection decision with platform-admin provisioning, and rename `docs/decisions/003-outbox-kafka.md` to `docs/decisions/004-transactional-outbox.md` so decision identifiers are unique. Add a new ADR only if a durable decision cannot be accurately represented by an existing one.
10. Add `docs/development-status.md`. Inventory each module, migration, API surface, required test class, and prior reviewer finding. Mark Bootstrap and Store Context according to current evidence; mark Identity/Authorization as implemented but incomplete while its blocking review findings and platform-admin provisioning mismatch remain; mark downstream commerce slices not started unless actual artifacts prove otherwise. Include the evidence date and exact verification results.
11. Standardize task records with `tasks/README.md`: define active and archived states, move completed `tasks/000-workflow-setup.md` through `tasks/003-store-context.md` to `tasks/archive/`, keep incomplete tasks at the task root, and update README, workflow, CI-message, and task links. Preserve their contents and Git history.
12. Simplify `README.md` into a concise project entry point with current setup commands, implementation-status link, document map, and workflow entry. Remove stale bootstrap-era narrative that is already owned by authoritative docs.
13. Run applicable code and integration checks to establish current status; record failures as status evidence rather than changing code or weakening tests. Inspect unresolved architect/security findings from task 004 against the corrected requirements.
14. After verification, resolve and validate the exact absolute `.cache/` path under the workspace, remove only that ignored disposable cache, and retain `.tools/` for frequent verification. Do not remove any untracked source or task artifact.
15. Perform a final consistency pass across docs, task links, README, and the CI YAML path-only update. Clearly label proposed capabilities versus implemented capabilities and list code, migration, OpenAPI, and security work that must be planned separately.

## Verification strategy

- Map every interview answer to at least one authoritative section and search for contradictory terms such as self-service onboarding, multi-store user sharing, multi-warehouse, partial refunds, and automatic VietQR confirmation.
- Check every relative Markdown link in README, docs, tasks, workflow, and CI messages resolves to the new locations; detect duplicate ADR identifiers.
- Parse the path-adjusted GitHub Actions YAML with an available YAML parser or `actionlint` when available.
- Run `make fmt-check`, `make lint`, `make test`, `make test-integration`, and `make vulncheck` before cleanup, because the development-status audit requires current code evidence. Report dependency or environment failures exactly and do not repair code in this task.
- Run `git diff --check`, inspect the complete diff, confirm no source or feature file was removed, confirm `.cache/` no longer exists, and confirm `.tools/` remains ignored and contains only reproducible pinned tools.

## Compatibility and follow-up impact

- Task 004 revision 4's public onboarding behavior conflicted with platform-admin-only provisioning. Revision 5 superseded that behavior with the current protected base-domain route, deployment-token authorization, and sessionless owner provisioning.
- Task 004 remains incomplete because two medium review findings are deferred. The user elected to skip further task 004 work for now; it remains inactive and must not be represented as accepted or production-ready.
- Future catalog, inventory, order, payment, shipping, event, and operational tasks must use the corrected documents as their requirements baseline.
- No production schema or public API decision is applied by this task.

## Required completion report

- Behavior and design summary
- Documentation files changed
- Archived and removed files, including whether they are reproducible or retained in Git history
- Commands executed with results
- Contradictions corrected and implementation gaps recorded
- Risks, assumptions, and follow-up work

## Delivery record

Follow [the project workflow](../docs/development-workflow.md). Keep this record concise and update it as work progresses.

- Stage: user accepted; final verification explicitly skipped
- Active task: no; work stopped by explicit user decision
- Execution mode: stage-only
- Last command, completed stage, pending gates, and next suggested command: `$ec-review`; architect and security reviewers passed commit `a78d970c7e8658faa42ac9c998567d822d6bee4d` plus working-tree binary diff hash `c490970f21ad3f70ef589fa5ab3cdd0c7d307e47` with no findings; on 2026-09-09 the user accepted the result and explicitly declined further testing; no next command is requested.
- Plan revision and approval: the user approved revision 2 and invoked `$ec-build tasks/005-correct-system-design-documentation.md` on 2026-09-08.
- Plan: correct and standardize the design baseline, add database and development-status documents, archive completed tasks, remove disposable local cache while retaining frequently used pinned tools, and audit rather than repair current implementation.
- Verification: requirements traceability, full current code checks for status evidence, task/document links, YAML parsing, contradiction and duplicate-decision search, diff inspection, and exact cleanup confirmation.
- Implementation evidence: corrected the existing design set; added database/status sources of truth; renumbered the outbox ADR; archived completed tasks; updated README/workflow/CI references; recorded task 004 revision 5 and its deferred findings; removed `.cache/` after checks and retained the reproducible `.tools/` cache by user direction.
- Focused verification: `make fmt-check`, `make test`, tagged `make test-integration`, `make vulncheck`, local Markdown links, ADR uniqueness, and `git diff --check` passed. `make lint` ran outside the restricted sandbox and failed with 12 findings in the unfinished task 004 implementation; the status document records them. GitHub Actions YAML parsing was unavailable because no parser/actionlint is installed; the YAML change is one quoted message path.
- Environment evidence: disposable Compose PostgreSQL, Redis, and Kafka were started for integration tests and stopped afterward. Native Windows Make configuration omitted the race detector. Initial sandbox-only lint/vulnerability attempts could not access the normal workspace/Go cache, so the meaningful reruns used approved elevated execution.
- Architect review: changes required. Read-only inspection covered the approved task, complete documentation baseline and ADRs, task records, diff and changed-file inventory, repository navigation, API wiring/OpenAPI, migrations, module layout, and current ignored-artifact state. Local Markdown targets, ADR identifier uniqueness, and `git diff --check` passed; final suites were not run and YAML parsing remains unverified.
- Architect findings: high — payment confirmation versus reservation/order expiry lacks one authoritative race and transaction design, permitting oversell, revival of expired state, or a succeeded payment attached to an expired order (`docs/database-design.md`, `docs/domain-model.md`, `docs/event-catalog.md`, and `docs/architecture.md`). Medium — Shipping versus Stores ownership of shipping policy conflicts, and outbox/inbox persistence has no explicit module owner. Medium — customer-managed single-store and multi-store host-to-`store_id` resolution is not explicit enough to implement without tenant-routing ambiguity. Medium — development status, task navigation, and onboarding compatibility text are stale or contradictory relative to task 004's completed re-review and the protected current route. Medium — the checked cleanup claim is false for the reviewed workspace because `.tools/` was later recreated and retained during task 004 verification.
- Security review: changes required. Read-only inspection covered tenant authority, host/proxy and platform-admin boundaries, guest credentials, authorization, tenant persistence keys, secrets and personal data, audit, payment/webhook design, backup/recovery expectations, CI, OpenAPI, tests, and the complete diff. `git diff --check` passed; test suites were not rerun.
- Security findings: medium — the authoritative status uses the undefined and softer “Implemented; conditionally usable” label for platform administration and Identity despite two unresolved blocking security findings, rather than the defined “Implemented, changes required” status. Low — the status snapshot still identifies an uncommitted tree and task 005 still presents platform-admin provisioning correction as future work even though the reviewed commit contains the protected route.
- Fix disposition: payment confirmation now has one database-time, fixed-lock-order transaction that commits payment/order/reservation/ledger/audit/deduplication/outbox state together or records reconciliation only. Shipping solely owns shipping rules; Eventing owns outbox/inbox mechanics and persistence. Customer-managed exact-host and subdomain routing are explicit; current code's routing limitation is recorded in development status and carried into the roadmap. Status and task navigation now mark task 004 as inactive, unaccepted, and deferred. `.tools/` is intentionally retained. The security low finding was skipped as directed; overlapping version/provisioning inconsistencies were corrected where required by the architect's medium finding.
- Fix checks: all local Markdown targets across 23 files resolved; ADR identifiers `001`–`004` remained unique; targeted stale-status, duplicate-ownership, asynchronous-confirmation, and cleanup-language searches found no unresolved active claim; `.cache/` was absent; `.tools/v2.13.2-v1.7.0` remained ignored with only the pinned `golangci-lint` and `govulncheck` binaries; `git diff --check` passed; the ten changed files were limited to `docs/` and `tasks/`.
- Architect re-review: changes required. The prior payment/expiry race, ownership, customer-managed routing, status/navigation, and cache/tool findings were confirmed resolved or substantially resolved. Medium — `consumer_inbox` uses global uniqueness `(consumer_name,event_id)` instead of tenant-scoped `(store_id,consumer_name,event_id)`, so a colliding or replayed Store B event can conflict with Store A's inbox row and suppress Store B's legitimate effect. Low — the development-status next-work list still says to fix task 005 after fixes were implemented. Checks confirmed the supplied diff hash, documentation-only scope, local links, ADR uniqueness, ignored tool contents, absent `.cache`, current routing gap, and `git diff --check`; final suites were not run.
- Security re-review: changes required. The prior medium status-classification finding was confirmed resolved, the user-directed low finding remains accepted, and the routing, ownership, provisioning, and task 004 status corrections were coherent. High — payment confirmation transitions reservations and records ledger entries without explicitly locking and decrementing authoritative `inventory_stock_items`; with `on_hand=1` and one active reservation, confirmation can remove the active reservation while leaving `on_hand=1`, making the sold unit available again. The transaction design and PostgreSQL tests must cover deterministic stock-row locks, guarded decrement, ledger totals, availability, emitted facts, and races with expiry, cancellation, and adjustment. No other new security defect was confirmed; tests were not rerun.
- Second fix disposition: confirmation, expiry, cancellation, and adjustment now use a documented global lock order including affected stock rows. Successful confirmation guardedly decrements physical `on_hand`, closes reservations, appends matching ledger deltas, and commits payment/order/audit/deduplication/outbox effects atomically; failed invariants roll back, and losing races record reconciliation without stock changes. Eventing outbox and inbox records, lookups, claims, producer identities, and uniqueness now include validated `store_id`; identical event IDs in different stores remain independent.
- Second fix checks: targeted searches found no active global inbox key, globally keyed outbox primary key, omitted stock decrement, or old confirmation lock-set wording. Required tenant keys, guarded `on_hand` consumption, global lock order, cross-store identical-event tests, and stock/ledger/concurrency test requirements are present. Local Markdown targets across 23 files resolved; ADR identifiers `001`–`004` remained unique; `.cache/` was absent and `.tools/` retained; `git diff --check` and the docs/tasks-only scope check passed. The first link-check attempt had a PowerShell predicate syntax error and supplied no evidence; the corrected rerun passed.
- Third architect re-review: changes required. Tenant-scoped Eventing identities and atomic stock consumption were confirmed resolved. Medium — the expiry worker is documented to claim reservation rows first with `FOR UPDATE SKIP LOCKED`, while all competing workflows must lock order, payment, reservations, then stock. Expiry can therefore hold a reservation while waiting for an order that confirmation holds as it waits for the reservation, causing PostgreSQL to abort a deadlocked workflow. Candidate discovery must not retain reservation locks before the shared order-first transaction; a real PostgreSQL test must prove both expiry/confirmation interleavings complete without deadlock and emit only the winning facts.
- Third security re-review: changes required on the same medium lock-order contradiction. The reviewer confirmed guarded `on_hand` consumption, reservation closure, ledger consistency, tenant-scoped outbox/inbox identities, and independent same-event IDs across stores. No other security regression was found; tests were not rerun.
- Third fix disposition: expiry performs a bounded non-locking scan for distinct tenant/order candidates, then claims one order row with `FOR UPDATE SKIP LOCKED` before locking payment, ordered reservations, and ordered stock rows. It rechecks database time and state only after the common lock sequence; duplicate discovery is harmless, confirmation makes expiry a no-op, and expiry makes later received funds reconcile.
- Third fix checks: targeted searches found no active reservation-first claim wording. Architecture, database, domain, and testing documents require non-locking discovery, order-first claiming, the shared lock sequence, both deterministic race winners, and rejection of deadlock or indefinite retry. Local Markdown targets across 23 files resolved; `git diff --check` and the docs/tasks-only scope check passed; `.cache/` remained absent and `.tools/` retained.
- Fourth architect re-review: pass with no findings. The reviewer confirmed non-locking expiry discovery, the shared order-first lock sequence, database-time rechecks, deterministic race outcomes, guarded stock consumption, and tenant-scoped Eventing identities. Read-only checks covered all ten changed documentation/task files, relevant code/schema/contracts, local links, ADR uniqueness, targeted contradictions, cache/tool state, and `git diff --check`; final tests were not run.
- Fourth security re-review: pass with no findings. The reviewer confirmed expiry cannot hold a reservation while waiting for its order, duplicate workers are safe, both race winners and winner-only facts are specified, and prior stock/Eventing fixes remain intact. These are target requirements for unimplemented commerce/Eventing modules and still require real PostgreSQL and tenant-isolation tests when built.
- Review residuals: no additional task 005 tenant-isolation or secret-handling defect was confirmed. Live provider interoperability, real ingress behavior, production-size migration locking, restore/key recovery, race-enabled CI evidence, secret rotation, and operational abuse/load testing remain unproven as already documented future evidence.
- User acceptance: on 2026-09-09 the user approved the documentation result and explicitly directed that no further tests be run.
- Final evidence: independent review gate passed; `$ec-test` was not run by explicit user decision, so the task is accepted by the user but not represented as fully workflow-verified.
