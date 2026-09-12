# Task: Defer coupons until after MVP

## Context

The accepted [VND pricing and Catalog publication task](009-vnd-pricing-catalog-publication.md) provides authoritative base prices without coupons. The user approved moving coupons and redemption accounting after MVP so delivery can proceed to [single-location inventory](../../docs/implementation-roadmap.md) and the core purchase flow. This task aligns the [product requirements](../../docs/product-requirements.md), [domain model](../../docs/domain-model.md), [database design](../../docs/database-design.md), [implementation roadmap](../../docs/implementation-roadmap.md), and [development status](../../docs/development-status.md).

## Outcome

- MVP checkout uses authoritative VND base prices without coupon evaluation or redemption accounting.
- Coupons remain a planned Pricing capability after the MVP production milestone.
- Single-location Inventory becomes the next required implementation slice.
- The roadmap reports nine remaining MVP slices after accepted Task 009.

## In scope

- Remove coupons and discount evaluation from MVP capability obligations.
- Mark coupon aggregates, redemption tables, and discount fields as post-MVP extensions.
- Separate accepted VND pricing from deferred coupons in roadmap and status documentation.
- Add a post-MVP coupon slice after operational hardening.

## Out of scope

- Coupon, discount, redemption, Inventory, checkout, order, API, event, or migration implementation.
- Renumbering the established MVP slices or rewriting accepted task history.
- Changing Pricing module ownership of future coupon behavior.

## Invariants and security constraints

- Checkout will still recalculate authoritative tenant-scoped VND base prices; caller-provided totals remain untrusted.
- Deferral must not weaken Money, order snapshot, tenant-isolation, inventory, payment, or reconciliation requirements.
- Future coupons remain store-owned, deterministic, bounded, integer-money rules with idempotent redemption accounting.

## Acceptance criteria

- [x] Given the MVP requirements, when coupons are referenced, then they are explicitly identified as post-MVP rather than required for launch.
- [x] Given checkout and order documentation, when authoritative totals are described, then MVP base-price behavior remains clear without requiring discounts.
- [x] Given the roadmap and current status, when next work is selected, then Inventory is next and coupons follow the MVP production milestone.
- [x] Given the completed documentation changes, when local links and diffs are validated, then all checks pass.

## Implementation notes

This is a documentation-only product-scope decision. It changes no existing API, persistence, or runtime behavior and requires no dependency or migration.

## Required completion report

- Behavior and design summary
- Files/modules changed
- Commands executed with results
- Risks, assumptions, and follow-ups

## Delivery record

Follow [the project workflow](../../docs/development-workflow.md). Keep this record concise and update it as work progresses.

- Stage: complete
- Active task: no
- Last command, completed stage, pending gates, and next suggested command: natural-language approval `yes`; documentation implementation, review corrections, both independent review passes, final validation, and archival are complete; start the single-location Inventory task next.
- Plan revision and approval: revision 1 approved by the user on 2026-09-11 after the five affected documents and MVP-count impact were presented.
- Plan: update product requirements, domain model, database design, roadmap, development status, and task navigation; qualify durable architecture/testing references as post-MVP; preserve future Pricing ownership; introduce no code, API, schema, migration, or dependency change; validate diffs and local links.
- Verification: acceptance maps to inspection of coupon/MVP references, local Markdown link validation, and `git diff --check`.
- Focused evidence: `git diff --check` and local Markdown link validation passed. Targeted reference inspection confirmed product requirements, domain behavior, persistence targets, roadmap milestones, status, architecture ownership, and applicable test cases distinguish post-MVP coupons from MVP base pricing.
- Architect review: initially required phase qualifications because coupon ownership and discount-boundary test references could still be read as MVP obligations. Both references now explicitly say post-MVP; re-review passed tracked diff `e22d6523bfcf26a435af51ff3592dbd85c4c3757` plus the untracked task record with no remaining findings.
- Security review: initial and final reviews passed. The final pass covered tracked diff `e22d6523bfcf26a435af51ff3592dbd85c4c3757` plus the untracked task record and confirmed tenant, Money, authoritative pricing, snapshots, payment, inventory, idempotency, and future coupon invariants remain intact.
- Final evidence: repository-wide coupon/discount reference inspection, `git diff --check`, and local Markdown link validation passed. No runtime tests were required because no code, API, schema, migration, dependency, configuration, or workflow behavior changed.
