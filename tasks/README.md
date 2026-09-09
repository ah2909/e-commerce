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
| [004 Identity and authorization](004-identity-authorization.md) | Revision 5 verified for continued development; two medium findings deferred and task inactive | Reopen with `$ec-fix` before production |
| [005 Documentation and status standardization](005-correct-system-design-documentation.md) | Reviews passed and user accepted; final verification explicitly skipped, task inactive | Reopen with `$ec-test` only if verification evidence is required |

## Archived tasks

| Task | Completed outcome |
| --- | --- |
| [000 Workflow setup](archive/000-workflow-setup.md) | Project delivery workflow and agent configuration |
| [001 Bootstrap](archive/001-bootstrap.md) | Go service foundation, dependencies, health, migrations, CI, and verification targets |
| [002 Stage commands](archive/002-stage-commands.md) | Reusable stage-only workflow skills |
| [003 Store context](archive/003-store-context.md) | Store aggregate, tenant context, persistence, and isolation baseline |

## Starting work

Create a task from [TEMPLATE.md](TEMPLATE.md), clarify material requirements, and obtain approval for its plan before implementation. Do not select an unfinished roadmap item merely because it is next numerically.
