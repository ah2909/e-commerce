---
name: "ecommerce-delivery"
description: "Run the full e-commerce delivery workflow only when explicitly invoked or requested by the user: approved plan, implementation, independent reviews, fixes, and testing."
---

# E-commerce delivery

Read the project-root AGENTS.md, the active task, and [the delivery workflow](../../../docs/development-workflow.md). Follow that workflow as the source of truth for stage transitions, approval, review gates, and verification.

Only explicit invocation of this skill or an explicit request for the full delivery workflow selects automatic delivery for the specified task. Ordinary implementation requests, an active task, prior approval, and `continue` do not select it. An explicit `ec-plan`, `ec-build`, `ec-review`, `ec-fix`, or `ec-test` request selects only that stage and stops at its boundary.

Resolve an explicit task path first, then an unambiguous task from the conversation or repository record. Keep one canonical task in the repository owning the primary outcome: backend in e-commerce, frontend in e-commerce-ui. Record sibling dependencies there without duplicating the task. For a clear new backend request, add a descriptive entry to the owning repository's `TASKS.md` and keep its plan and delivery evidence there; do not recreate `tasks/` or require a template. Follow the frontend repository's own task conventions for frontend work. Ask only when task identity is materially ambiguous; never infer it from modification time or unrelated roadmap entries.

In automatic delivery, clarify material gaps and present a plan including the technical solution. Wait for plan approval only if it has not already been given. Then implement, spawn separate architect and security reviewers in parallel, resolve findings, and verify the final implementation automatically.

Keep the task's delivery record current. Report missing prerequisites or failed required gates as blocked/unverified. Do not silently substitute self-review for the two independent reviewers.

When assigned a reviewer role or a review-only request, perform only that review and return findings; do not start this implementation workflow.
