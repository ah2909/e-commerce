---
name: "ec-review"
description: "Run independent architect and security reviews of an e-commerce task without fixing implementation when the user requests review only or invokes ec-review."
---

# ec-review

Read [the project workflow](../../../docs/development-workflow.md), especially Commands and section 3. Resolve the active task and select stage-only execution for this turn using the shared rules.

Spawn separate architect and security reviewer agents in parallel using the project role definitions or the documented generic-agent fallback. Keep implementation artifacts stable. Review actual files and current evidence, including fixes awaiting re-review. Reviewing existing work does not require approving its implementation plan; disclose missing plan evidence as a limit.

Consolidate both verdicts and concrete findings in the task record, then stop. Do not edit implementation, fix findings, or run final verification. If agents are unavailable, report the review gate blocked rather than substituting self-review. Suggest `$ec-fix` for actionable findings or `$ec-test` when reviews pass.
