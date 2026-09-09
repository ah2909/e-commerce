# Instructions for Codex

## Mission

Build a maintainable multi-store e-commerce backend. Optimize for correctness, tenant isolation, testability, and incremental delivery before scale or abstraction.

## Working method

- Read the relevant files in `docs/` and the active task before editing.
- Inspect existing patterns before introducing new ones.
- For non-trivial work, state a short plan and identify affected modules.
- Keep changes limited to the active task. Do not implement adjacent roadmap items.
- Ask before making an irreversible schema or public API decision not covered by the docs.
- Never discard unrelated user changes.

## Project delivery workflow

- Follow `docs/development-workflow.md` for implementation tasks in this project.
- Use the normal workflow: clarify material requirements, present one plan, wait for one approval, then automatically implement, review, fix, and test.
- Resolve or create the task record automatically when the request is clear. Do not ask the user to choose routine task numbers, paths, or workflow stages.
- After plan approval, plain language such as `continue` resumes automatic delivery unless the user explicitly requests a narrower stage or asks to stop.
- The project skills `ec-plan`, `ec-build`, `ec-review`, `ec-fix`, and `ec-test` remain optional advanced controls that run one stage and stop at its boundary.
- Spawn separate architect and security reviewer agents in parallel during the review stage, automatically after implementation only in automatic delivery. Use the project reviewer definitions under `.codex/agents/`, or pass their instructions explicitly when custom agent selection is unavailable.
- Reviewers report findings only; they must not edit files, spawn agents, or restart the delivery workflow. The implementing agent owns fixes and final verification.
- Unresolved blocking findings or failed/unavailable required checks prevent declaring the implementation complete.

## Code comments

- Keep comments to one line and explain only non-obvious reasons.
- Do not add explanatory docstrings, multi-line comment blocks, or TODO/FIXME comments unless requested.

## Architecture rules

- Use a modular monolith. A module owns its domain logic and persistence.
- Modules may not query or mutate another module's tables directly.
- Cross-module synchronous interactions use explicit application interfaces.
- Asynchronous side effects use domain/integration events through the outbox.
- Kafka must not be required to commit the initial checkout transaction.
- Keep HTTP handlers thin: parse, authorize, invoke a use case, and map the response.
- Domain rules must not depend on HTTP, SQL, Kafka, or vendor SDK types.
- Prefer explicit Go code over reflection-heavy frameworks and premature generic abstractions.

## Tenant-safety rules

- Every tenant-owned aggregate and table includes `store_id`.
- Every tenant-owned query receives `store_id` explicitly.
- Unique constraints for tenant-owned identifiers include `store_id`.
- Never infer tenant scope from request payload fields. Resolve it from authenticated context.
- Never provide an unscoped repository method for tenant-owned data.
- Add a cross-store denial test for every new tenant-owned resource.

## Commerce invariants

- Represent money as integer minor units plus ISO currency. Never use floating point.
- Persist order-line snapshots; historical orders must not depend on current product data.
- Inventory reservations and adjustments must be atomic and auditable.
- Payment and webhook operations must be idempotent.
- Validate order state transitions centrally.
- Store timestamps in UTC and expose RFC 3339 timestamps.
- External calls require timeouts and must distinguish retryable from permanent failures.

## Go conventions

- Use the Go version declared in `go.mod`.
- Keep packages cohesive and avoid cyclic module dependencies.
- Accept `context.Context` as the first argument for I/O-bound operations.
- Wrap errors with useful context; preserve errors for `errors.Is` and `errors.As`.
- Do not log secrets, credentials, payment tokens, or unnecessary personal data.
- Use structured logging and propagate correlation/request IDs.
- Keep interfaces at consumer boundaries; do not create interfaces for every struct.

## Database and API changes

- Use forward-only, versioned migrations.
- Consider locking, table size, rollback strategy, and backward compatibility.
- REST endpoints live under `/api/v1`.
- Validate input at the API boundary and enforce invariants again in the domain.
- Use consistent problem responses with `code`, `message`, `details`, and `request_id`.
- Update the OpenAPI contract when externally visible behavior changes.

## Verification

Before declaring completion, select checks by risk according to `docs/testing-strategy.md`:

- Documentation-only changes require diff, local-link, and changed-configuration validation.
- Backend changes require formatting, linting, and focused tests for affected behavior.
- Run integration tests only when persistence, Redis, migrations, or concurrency behavior is affected.
- Run vulnerability checks for dependency, authentication, cryptography, security-sensitive, and release changes.
- Run `make verify` for broad cross-module changes and release candidates.

If a command cannot be run, report exactly which command and why. Do not claim tests passed unless they were executed successfully.

## Completion report

Summarize:

- Behavior implemented
- Important design choices
- Files or modules changed
- Commands run and their outcomes
- Remaining risks or follow-up work
