---
name: "ec-build"
description: "Implement an approved e-commerce task plan with focused tests, then stop before reviews when the user requests implementation only or invokes ec-build."
---

# ec-build

Read [the project workflow](../../../docs/development-workflow.md), especially Reusable stage commands and section 2. Resolve the active task and select stage-only execution using the shared rules.

Establish actual user approval of the applicable plan before edits; invoking this command alone does not approve a plan. Implement that plan with its tests, contracts, and focused development checks. Preserve unrelated edits.

Report changes, check results, and pending gates, then stop before independent review or final verification. Do not spawn reviewers or silently continue through the automatic workflow. Suggest `$ec-review` when the implementation is ready.
