# Delivery tasks

Tasks are the unit of project delivery. Each task retains its requirements, approved plan, stage, review findings, verification evidence, and remaining risks according to the [development workflow](../docs/development-workflow.md).

## Status rules

- Incomplete tasks remain in this directory and identify their current stage and next command.
- Completed tasks move to [archive](archive/) after all applicable acceptance, independent-review, and verification gates pass.
- Archiving preserves delivery evidence; it does not delete or rewrite history.
- A task marked implemented but awaiting review, fixes, or testing is not complete.
- [Development status](../docs/development-status.md) is the current system-wide summary; task records contain detailed evidence.

## Incomplete tasks

| Task | State | Next gate |
| --- | --- | --- |
| [004 Identity and authorization](004-identity-authorization.md) | Revision 5 verified for continued development; two medium findings explicitly skipped and task inactive | None scheduled; reopen with `$ec-fix` only if the risk decision changes |
| [005 Documentation and status standardization](005-correct-system-design-documentation.md) | Reviews passed and user accepted; final verification explicitly skipped, task inactive | Reopen with `$ec-test` only if verification evidence is required |

## Archived tasks

| Task | Completed outcome |
| --- | --- |
| [000 Workflow setup](archive/000-workflow-setup.md) | Project delivery workflow and agent configuration |
| [001 Bootstrap](archive/001-bootstrap.md) | Go service foundation, dependencies, health, migrations, CI, and verification targets |
| [002 Stage commands](archive/002-stage-commands.md) | Reusable stage-only workflow skills |
| [003 Store context](archive/003-store-context.md) | Store aggregate, tenant context, persistence, and isolation baseline |
| [007 Gin and GORM migration](archive/007-adopt-gin-gorm-project-wide.md) | Native Gin transport and GORM persistence adapters with preserved schema, API, tenant, and concurrency behavior |
| [008 Catalog authoring foundation](archive/008-catalog-authoring-foundation.md) | Tenant-safe draft Catalog authoring, migration 3, admin APIs, and concurrency/isolation evidence |
| [009 VND pricing and Catalog publication](archive/009-vnd-pricing-catalog-publication.md) | Tenant-safe VND price lists, price-gated publication, public priced Catalog reads, and atomic outbox persistence |
| [010 Defer coupons until after MVP](archive/010-defer-coupons-until-after-mvp.md) | MVP scope now proceeds from base pricing to Inventory; coupons and discount accounting follow production MVP |
| [011 Single-location Inventory](archive/011-single-location-inventory.md) | Tenant-safe stock, immutable ledgers, audited adjustments, atomic reservations, public availability, and expiry processing |

## Starting work

1. Describe the desired outcome in normal language.
2. Codex resolves an existing task or creates the next numbered task from [TEMPLATE.md](TEMPLATE.md).
3. Review and approve the implementation plan once.
4. Say `continue` to run implementation, reviews, fixes, and verification automatically.

Specify a task path only when you need to override the task inferred from the conversation. Codex must not select an unfinished roadmap item merely because it is next numerically.
