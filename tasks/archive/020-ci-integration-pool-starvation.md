# Task 020: Prevent CI integration-test pool starvation

## Context

- [Development workflow](../../docs/development-workflow.md)
- [Testing strategy](../../docs/testing-strategy.md)
- [Failed CI run](https://github.com/ah2909/e-commerce/actions/runs/36874801869)

## Outcome

Orders concurrency integration tests complete on small CI runners, and lock observation fails within its deadline even when the connection pool is exhausted.

## In scope

- Set an explicit connection budget in the Orders integration-test pool for the blocker, concurrent operations, and lock observer.
- Bound lock-observation queries with a context deadline.
- Ensure the failing refund-race test releases its blocker on failure and bounds its concurrent operations.
- Verify against disposable PostgreSQL with a low CPU count.

## Out of scope

- Production pool configuration, commerce behavior, schema, and public APIs.

## Invariants and security constraints

- Preserve atomicity, idempotency, stock, rollback, and cross-store assertions.
- Retain test database mutation guards; use disposable dependencies only.

## Acceptance criteria

- [x] The reported refund concurrency test passes with a two-CPU runtime.
- [x] Orders integration tests pass with the explicit test pool budget.
- [x] Lock observation cannot hang indefinitely acquiring a connection.
- [x] Formatting, linting, independent reviews, and relevant integration checks pass.

## Delivery record

- Stage: complete.
- Active task: no.
- Plan revision and approval: revision 1 approved by the user on 2026-10-03.
- Plan: update `orderPool` in `repository_integration_test.go` to provide at least five connections independently of CPU count; update `waitForBlockedQueries` in `vietqr_integration_test.go` to share a bounded context across connection acquisition/query/polling; add bounded context and deferred blocker rollback in the failing refund race in `cancellation_integration_test.go`. No dependencies, migration, or API changes.
- Verification: run the targeted test with the explicit five-connection budget, then Orders integration coverage, formatting, linting, and `make test-integration` with disposable dependencies. Run Linux race checks under two-CPU affinity where available; `GOMAXPROCS` alone does not change pgx's CPU-based default. Record actual commands and environment limits.
- Focused evidence: `go test -tags=integration ./internal/orders/infrastructure/postgres -run '^TestVietQRFullRefundRestocksAndCompletesAtomically$' -count=1 -timeout=90s` passed with test database environment; `go test -tags=integration ./internal/orders/infrastructure/postgres -count=1 -timeout=180s` passed in 36.329s. The initial focused run without `ALLOW_TEST_DATABASE_MUTATION=true` correctly failed its database guard. A second targeted run with the Go process pinned to CPUs 0 and 1 passed in 2.008s.
- Architect review: pass on `f9e3cda` plus the three Orders test files and task record; pool capacity, timeout propagation, cleanup, assertions, and callers inspected; no findings.
- Security review: pass on the same scope; mutation guard, permission denial, cross-store, tampering, rollback, replay, and side-effect assertions inspected; no findings.
- Final evidence: `make dev-deps` passed; `make migrate-test-up` passed (schema already at 13); `make fmt-check` passed; `make lint` passed with zero issues; `make test-integration` passed across all packages. `git diff --check` passed. The failed hosted run 36874801869 timed out after 600 seconds in the refund race because pgx's CPU-dependent pool default provided only four connections; the test requires five while observing locks. [Hosted quality-gates run 37096151338](https://github.com/ah2909/e-commerce/actions/runs/37096151338) passed its Linux `make verify` rerun on commit `8e1fb14` in 12m37s; its first attempt passed Orders but failed an Identity onboarding request with 503 under race-enabled load. [Release security evidence run 37096151363](https://github.com/ah2909/e-commerce/actions/runs/37096151363) passed. The intermittent onboarding failure remains a separate CI stability concern.
