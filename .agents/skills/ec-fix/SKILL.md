---
name: "ec-fix"
description: "Fix supported review findings within an approved e-commerce task and stop before re-review when the user requests fixes only or invokes ec-fix."
---

# ec-fix

Read [the project workflow](../../../docs/development-workflow.md), especially Reusable stage commands and section 4. Resolve the active task and select stage-only execution using the shared rules.

Establish user approval of the applicable plan, then inspect the findings against current code. Fix supported issues within that scope and run meaningful regression tests. If there are no actionable in-scope findings, report that and stop; do not invent improvements. Surface findings requiring a material scope decision before dependent edits.

Record fixes and checks, invalidate affected stale evidence, and leave findings awaiting reviewer confirmation. Stop without spawning reviewers or running final verification. Suggest `$ec-review` to inspect the fixes.
