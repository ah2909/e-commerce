# Delivery tasks

These are backend-owned tasks. Frontend-owned records live only in [e-commerce-ui/tasks](../../e-commerce-ui/tasks/README.md). A mixed task has one canonical record in the repository owning the primary outcome, with sibling dependencies recorded there. Task numbers are local to each repository. Tasks are the unit of project delivery. Each task retains its requirements, approved plan, stage, review findings, verification evidence, and remaining risks when the optional [development workflow](../docs/development-workflow.md) is explicitly selected.

## Status rules

- Incomplete tasks remain in this directory and identify their current stage and next command.
- Completed tasks move to [archive](archive/) after all applicable acceptance, independent-review, and verification gates pass.
- Archiving preserves delivery evidence; it does not delete or rewrite history.
- A task marked implemented but awaiting review, fixes, or testing is not complete.
- [Development status](../docs/development-status.md) is the current system-wide summary; task records contain detailed evidence.

## Incomplete tasks

| Task | State | Next gate |
| --- | --- | --- |
| [019 Operational hardening](019-operational-hardening.md) | Local final verification complete; external launch gates pending | User selects the external launch checks |
| [015 MoMo payments](015-momo-payments.md) | Reviews and automated verification passed; live sandbox UAT blocked on external prerequisites | `make test-momo-sandbox` |
| [004 Identity and authorization](004-identity-authorization.md) | Revision 5 verified for continued development; two medium findings explicitly skipped and task inactive | None scheduled; reopen with `$ec-fix` only if the risk decision changes |
| [005 Documentation and status standardization](005-correct-system-design-documentation.md) | Reviews passed and user accepted; final verification explicitly skipped, task inactive | Reopen with `$ec-test` only if verification evidence is required |

## Archived tasks

| Task | Completed outcome |
| --- | --- |
| [020 CI integration pool starvation](archive/020-ci-integration-pool-starvation.md) | Orders integration pool capacity and bounded lock observation; hosted Backend verification passed |
| [000 Workflow setup](archive/000-workflow-setup.md) | Project delivery workflow and agent configuration |
| [001 Bootstrap](archive/001-bootstrap.md) | Go service foundation, dependencies, health, migrations, CI, and verification targets |
| [002 Stage commands](archive/002-stage-commands.md) | Reusable stage-only workflow skills |
| [003 Store context](archive/003-store-context.md) | Store aggregate, tenant context, persistence, and isolation baseline |
| [007 Gin and GORM migration](archive/007-adopt-gin-gorm-project-wide.md) | Native Gin transport and GORM persistence adapters with preserved schema, API, tenant, and concurrency behavior |
| [008 Catalog authoring foundation](archive/008-catalog-authoring-foundation.md) | Tenant-safe draft Catalog authoring, migration 3, admin APIs, and concurrency/isolation evidence |
| [009 VND pricing and Catalog publication](archive/009-vnd-pricing-catalog-publication.md) | Tenant-safe VND price lists, price-gated publication, public priced Catalog reads, and atomic outbox persistence |
| [010 Defer coupons until after MVP](archive/010-defer-coupons-until-after-mvp.md) | MVP scope now proceeds from base pricing to Inventory; coupons and discount accounting follow production MVP |
| [011 Single-location Inventory](archive/011-single-location-inventory.md) | Tenant-safe stock, immutable ledgers, audited adjustments, atomic reservations, public availability, and expiry processing |
| [012 Customers and carts](archive/012-customers-and-carts.md) | Tenant-safe customer profiles and addresses, user/guest carts, current estimates, optimistic mutation, and bounded expiry |
| [013 Checkout and orders](archive/013-checkout-orders.md) | Atomic checkout, historical order snapshots, tenant idempotency, and guest grants |
| [014 VietQR payments](archive/014-vietqr-payments.md) | Encrypted tenant VietQR configuration, local QR instructions, atomic audited confirmation, explicit staff permissions, and reconciliation |
| [016 Cancellation and full refunds](archive/016-cancellation-full-refunds.md) | Tenant-safe unpaid cancellation, full MoMo/VietQR refunds, atomic restock, and concurrency/isolation evidence |
| [017 Simple fulfillment](archive/017-simple-fulfillment.md) | Tenant-safe one-shipment fulfillment, permissioned admin workflow, shopper tracking projection, and atomic lifecycle evidence |
| [018 Event workers and derived integrations](archive/018-event-workers-derived-integrations.md) | Leased Kafka publication, strict consumer contracts, transactional derived projections, redacted recovery tooling, and reliability evidence |

## Starting work

1. Describe the desired outcome; ordinary requests do not activate the delivery workflow.
2. To use the workflow manually, invoke `$ec-plan`, then approve the plan and invoke `$ec-build`.
3. Invoke `$ec-review`, `$ec-fix`, and `$ec-test` when you want those stages. Each stops at its boundary.
4. Use `$ecommerce-delivery` only to explicitly select the full workflow. `continue` does not switch manual stages to automatic delivery.

Specify a task path only when you need to override the task inferred from the conversation. Codex must not select an unfinished roadmap item merely because it is next numerically.
