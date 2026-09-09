# Task: Standardize system design, repository documentation, and development status

## Context

The requirements interview refined and corrected the initial product direction in [product requirements](../docs/product-requirements.md), [architecture](../docs/architecture.md), [domain model](../docs/domain-model.md), [event catalog](../docs/event-catalog.md), [roadmap](../docs/implementation-roadmap.md), [testing strategy](../docs/testing-strategy.md), and the accepted decisions under [`docs/decisions`](../docs/decisions/).

The system serves independent businesses. It starts with Vietnam-focused physical-goods commerce, runs as subdomain-isolated SaaS or as a customer-managed deployment, and must remain maintainable as data volume and traffic grow. The existing Identity and authorization implementation is in progress. This task audits it but does not rewrite or discard it.

The repository currently contains about 496 MB of ignored Go build cache under `.cache/` and about 207 MB of ignored reproducible tools under `.tools/`. Completed task records are mixed with active tasks, the outbox and identity ADRs both use number 003, and no authoritative document reports what is implemented, incomplete, incorrect against the new requirements, or not started.

## Outcome

The repository has one coherent, navigable design baseline describing the intended product, system boundaries, database model, critical commerce flows, security controls, scaling path, and operational expectations. Documentation and delivery records follow an explicit structure, disposable local artifacts are removed, and an evidence-backed status document distinguishes implemented and verified work, partial or incorrect work, and future work.

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
- Remove the ignored, reproducible `.cache/` and `.tools/` directories after verification. They are local build artifacts, are not source, and can be recreated by Go and `make tools`.
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
- [x] The ignored `.cache/` and `.tools/` directories are removed only after commands that depend on them finish; source and uncommitted feature work remain intact.
- [x] The keep/delete decision is explicit: generated caches/tools and superseded duplicate files are removed; workflow configuration, source, tests, contracts, migrations, skills, agent definitions, and task history are retained.
- [x] Changed Markdown links resolve locally, headings and terminology are consistent, and `git diff --check` passes.

## Proposed documentation and technical solution

1. Revise `docs/product-requirements.md` into an authoritative MVP contract: independent tenants; platform-admin provisioning; Vietnam/VND physical commerce; guests; catalog/variant needs; flat/free shipping; cancellation; full refunds; VietQR and MoMo behavior; explicit exclusions and success criteria.
2. Expand `docs/architecture.md` with system context and runtime flows, module ownership, a separate platform control-plane boundary, SaaS and customer-managed deployment topology, failure boundaries, secrets, observability, backup/recovery baseline, and an evidence-based scale path. Keep the API and worker stateless and PostgreSQL authoritative; keep Kafka outside checkout commits.
3. Revise `docs/domain-model.md` with concrete lifecycle rules for catalog, inventory, carts, orders, payments, shipping, guests, provider configuration, reconciliation, refunds, and archival. Define guarded order/payment/reservation state transitions and Vietnam-first money/tax assumptions.
4. Add `docs/database-design.md` as the logical relational source of truth. Use module-owned shared PostgreSQL tables; composite tenant keys and tenant-matching foreign keys; integer money; integer physical-item quantity; UTC timestamps; optimistic versions; immutable ledgers/snapshots; selective JSONB; store-leading indexes; keyset pagination; and query-specific partial indexes. Retain the existing UUID identifier convention and require evidence before changing identifier type.
5. In the database growth section, keep normal transactional tables unpartitioned initially. Define monitoring and migration triggers for partitioning append-heavy audit/outbox/ledger/webhook data, adding read replicas for stale-tolerant reads, isolating exceptionally large tenants, and extracting a module only when ownership, scaling, release, failure, or regulatory evidence justifies it.
6. Correct `docs/event-catalog.md` with reservation, order-expiration, payment-confirmation/failure, reconciliation, cancellation, refund, shipment, and archival events. Specify minimal versioned envelopes, ordering expectations, producer ownership, outbox atomicity, consumer inbox deduplication, and personal-data restrictions.
7. Reorder and clarify `docs/implementation-roadmap.md` so catalog, pricing, inventory, customer/guest cart, checkout, VietQR, MoMo, refunds, shipping, workers, and operational hardening are deliverable vertical slices. Record the current public store-onboarding behavior as a code/API follow-up because the corrected contract requires platform-admin-only provisioning.
8. Extend `docs/testing-strategy.md` to map the corrected invariants to unit, PostgreSQL concurrency, API, provider-contract, event-idempotency, security, recovery, and later load tests. Include cross-store denial for every resource and failure cases for expired/late/duplicate payments.
9. Update relevant ADRs rather than hiding changed decisions: preserve the modular monolith and shared-schema choices, align the identity/host-selection decision with platform-admin provisioning, and rename `docs/decisions/003-outbox-kafka.md` to `docs/decisions/004-transactional-outbox.md` so decision identifiers are unique. Add a new ADR only if a durable decision cannot be accurately represented by an existing one.
10. Add `docs/development-status.md`. Inventory each module, migration, API surface, required test class, and prior reviewer finding. Mark Bootstrap and Store Context according to current evidence; mark Identity/Authorization as implemented but incomplete while its blocking review findings and platform-admin provisioning mismatch remain; mark downstream commerce slices not started unless actual artifacts prove otherwise. Include the evidence date and exact verification results.
11. Standardize task records with `tasks/README.md`: define active and archived states, move completed `tasks/000-workflow-setup.md` through `tasks/003-store-context.md` to `tasks/archive/`, keep incomplete tasks at the task root, and update README, workflow, CI-message, and task links. Preserve their contents and Git history.
12. Simplify `README.md` into a concise project entry point with current setup commands, implementation-status link, document map, and workflow entry. Remove stale bootstrap-era narrative that is already owned by authoritative docs.
13. Run applicable code and integration checks to establish current status; record failures as status evidence rather than changing code or weakening tests. Inspect unresolved architect/security findings from task 004 against the corrected requirements.
14. After verification, resolve and validate the exact absolute `.cache/` and `.tools/` paths under the workspace, then remove only those ignored reproducible directories. Do not remove any untracked source or task artifact.
15. Perform a final consistency pass across docs, task links, README, and the CI YAML path-only update. Clearly label proposed capabilities versus implemented capabilities and list code, migration, OpenAPI, and security work that must be planned separately.

## Verification strategy

- Map every interview answer to at least one authoritative section and search for contradictory terms such as self-service onboarding, multi-store user sharing, multi-warehouse, partial refunds, and automatic VietQR confirmation.
- Check every relative Markdown link in README, docs, tasks, workflow, and CI messages resolves to the new locations; detect duplicate ADR identifiers.
- Parse the path-adjusted GitHub Actions YAML with an available YAML parser or `actionlint` when available.
- Run `make fmt-check`, `make lint`, `make test`, `make test-integration`, and `make vulncheck` before removing `.tools/`, because the development-status audit requires current code evidence. Report dependency or environment failures exactly and do not repair code in this task.
- Run `git diff --check`, inspect the complete diff, confirm no source/uncommitted feature file was removed, and confirm `.cache/` and `.tools/` no longer exist after all checks finish.

## Compatibility and follow-up impact

- The platform-admin-only provisioning requirement conflicts with the in-progress public onboarding endpoint and invalidates that part of task 004's approved revision 4 plan and its related review/test evidence. This task documents the correction only; a separate approved implementation revision must change the API, authorization, migrations if necessary, and tests.
- Task 004 remains incomplete because its independent reviews require changes. Archiving completed tasks must not imply that task 004 is correct or complete.
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

- Stage: implementation complete; independent reviews pending
- Active task: yes
- Execution mode: stage-only
- Last command, completed stage, pending gates, and next suggested command: `$ec-build`; documentation, status audit, task archival, focused checks, and approved local cleanup completed; independent architect/security reviews and final documentation verification are pending; use `$ec-review tasks/005-correct-system-design-documentation.md`.
- Plan revision and approval: the user approved revision 2 and invoked `$ec-build tasks/005-correct-system-design-documentation.md` on 2026-09-08.
- Plan: correct and standardize the design baseline, add database and development-status documents, archive completed tasks, remove only reproducible local cache/tool directories, and audit rather than repair current implementation.
- Verification: requirements traceability, full current code checks for status evidence, task/document links, YAML parsing, contradiction and duplicate-decision search, diff inspection, and exact cleanup confirmation.
- Implementation evidence: corrected the existing design set; added database/status sources of truth; renumbered the outbox ADR; archived completed tasks; updated README/workflow/CI references; marked task 004 as requiring revision 5; removed only the verified ignored `.cache/` and `.tools/` directories after checks.
- Focused verification: `make fmt-check`, `make test`, tagged `make test-integration`, `make vulncheck`, local Markdown links, ADR uniqueness, and `git diff --check` passed. `make lint` ran outside the restricted sandbox and failed with 12 findings in the unfinished task 004 implementation; the status document records them. GitHub Actions YAML parsing was unavailable because no parser/actionlint is installed; the YAML change is one quoted message path.
- Environment evidence: disposable Compose PostgreSQL, Redis, and Kafka were started for integration tests and stopped afterward. Native Windows Make configuration omitted the race detector. Initial sandbox-only lint/vulnerability attempts could not access the normal workspace/Go cache, so the meaningful reruns used approved elevated execution.
- Architect review: pending and not part of `$ec-build`.
- Security review: pending and not part of `$ec-build`.
- Final evidence: pending independent reviews and `$ec-test`.
