---
name: "ec-plan"
description: "Plan one task in this e-commerce project and stop before implementation when the user requests planning only or invokes ec-plan."
---

# ec-plan

Read [the project workflow](../../../docs/development-workflow.md), especially Commands and section 1. Resolve the active task and select stage-only execution for this turn using the shared rules.

Clarify material gaps and prepare the plan, technical solution, and verification strategy in the task document. Preserve existing implementation and identify any prior approval/evidence invalidated by a revision.

Present the plan and stop. Do not implement, spawn reviewers, or run final verification. Report pending gates; after approval, suggest plain `continue` for automatic delivery or `$ec-build` for build-only execution.
