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

## Required high-risk cases

- Tenant scope comes from authenticated context, every tenant query includes `store_id`, and cross-store access fails closed.
- Authorization denies by default and distinguishes platform administration from store membership.
- Money uses integer minor units and rejects invalid currency, overflow, tampered prices, and invalid discount boundaries.
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

CI may continue running the full suite. A skipped, unavailable, or failed required check must be reported accurately, but unrelated expensive checks are not required for every small change.
