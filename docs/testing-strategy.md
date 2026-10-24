# Testing strategy

## Purpose

Tests protect behavior whose failure would be expensive: tenant isolation, authorization, money, inventory, order state, idempotency, persistence, and external integrations. Tests do not need to mirror the source tree, and a source file does not require a matching test file.

## What to test

| Change | Expected coverage |
| --- | --- |
| Domain rule, permission, state transition, money, or idempotency | Focused unit or application tests for success, denial, and important boundaries |
| Tenant-owned behavior | Same-store success plus cross-store denial |
| PostgreSQL constraint, transaction, lock, migration, or concurrency | Integration test against real PostgreSQL |
| Redis atomicity, expiry, or store-scoped key | Integration test against real Redis |
| HTTP contract, authentication, validation, or problem response | Handler or API contract test at the boundary |
| Provider signature, mapping, timeout, or retry classification | Contract test with local fixtures and controlled failures |
| Trivial wiring, constants, framework behavior, or code already covered through a stable public boundary | No dedicated test required |

Prefer a small number of behavior-oriented tests over one test file per implementation file. Tests may be grouped by package or use case when that makes the protected behavior easier to understand.

## File organization and reading code

Go discovers tests by the `_test.go` suffix within a package; it does not require a matching test file for each source file. See the [Go testing guide](https://go.dev/doc/code#Testing).

- Keep unit tests beside their package, grouped by behavior or use case: `checkout_test.go`, `permissions_test.go`, or one `domain_test.go` for a small cohesive domain package.
- Use multiple files when scenarios have different setup or the combined file becomes difficult to navigate. A file-count target is not a reason to create a large test file.
- Keep real-infrastructure tests behind the existing `integration` build tag and name them by workflow, such as `checkout_integration_test.go` or `refund_integration_test.go`.
- Keep end-to-end tests for a few critical API journeys. Do not repeat every domain edge case at HTTP, service, repository, and API layers; each layer should cover its own failure modes.
- Prefer external `package name_test` for public-boundary tests when practical. Use the package itself when testing an internal invariant is necessary; moving these tests into another directory changes package access.
- Reuse genuinely shared infrastructure setup in `internal/testsupport`; keep business fixtures near the behavior they serve.

For this repository, the first useful consolidation candidates are small related domain suites such as Stores' store, role, and membership tests. The Orders PostgreSQL suites already group distinct checkout, payment, refund, and fulfillment workflows; combining them would make navigation harder. Consolidate tests when touching the relevant behavior, preserving distinct denial, rollback, replay, and race cases.

For a quieter VS Code Explorer, add `"files.exclude": { "**/*_test.go": true }` in workspace settings and remove that entry when browsing tests. This changes only the view; `go test` and CI still discover the tests. Search production Go with `rg --files -g '*.go' -g '!**/*_test.go'`.

## Required high-risk cases

- Tenant scope comes from authenticated context, every tenant query includes `store_id`, and cross-store access fails closed.
- Authorization denies by default and distinguishes platform administration from store membership.
- Money uses integer minor units and rejects invalid currency, overflow, and tampered prices; discount-boundary cases apply when post-MVP discounts are introduced.
- Inventory reservation, confirmation, release, and adjustment are atomic, auditable, idempotent, and safe under contention.
- Checkout idempotency produces one order, order snapshots remain historical, and invalid state transitions fail.
- Payment and webhook processing verifies signatures/raw input, rejects mismatches, and produces one business effect under duplicates or races.
- Database constraints and migrations establish the claimed tenant keys, uniqueness, foreign keys, and forward compatibility.
- External calls have timeouts and distinguish retryable from permanent failures.

Only apply cases relevant to the changed behavior. Do not implement future modules merely to satisfy this list.

## Test design

- Test observable behavior through stable package or API boundaries, not private helpers.
- Use table-driven tests when several meaningful business cases share the same setup.
- Use fixed clocks and deterministic identifiers when time or identity affects results.
- Use real infrastructure only for behavior that depends on that infrastructure.
- Synchronize concurrency tests at the contested point; do not depend on sleeps.
- Never place real credentials, tokens, secrets, or unnecessary personal data in tests or snapshots.
- Remove or combine tests that assert the same behavior without adding a distinct failure case.

## Verification by change risk

During development, run the smallest command that covers the changed behavior.

- Documentation-only: inspect the diff, validate local links, and parse changed configuration.
- Typical backend change: run formatting, linting, and tests for the affected package or module.
- Persistence, Redis, migration, or concurrency change: also run the relevant integration tests with real dependencies.
- Dependency, authentication, cryptography, or release change: also run `make vulncheck`.
- Broad cross-module change or release candidate: run `make verify`.

Operational release candidates also build and inspect the container, parse deployment/monitoring configuration, scan secrets/SBOM/container vulnerabilities, run a bounded dynamic baseline against a disposable API, and execute the versioned load scenario against synthetic data. Backup/restore, migration-lock, and capacity evidence comes from a disposable production-like environment and is recorded as unavailable rather than passed when that environment is absent. See [capacity and security evidence](capacity-and-security.md) and [production deployment](production-deployment.md).

CI may continue running the full suite. A skipped, unavailable, or failed required check must be reported accurately, but unrelated expensive checks are not required for every small change.
