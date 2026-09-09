---
name: "ecommerce-delivery"
description: "Deliver implementation tasks in this multi-store e-commerce project through an approved plan, implementation, independent architect and security reviews, fixes, and testing. Also use when resuming an active project task."
---

# E-commerce delivery

Read the project-root AGENTS.md, the active task, and [the delivery workflow](../../../docs/development-workflow.md). Follow that workflow as the source of truth for stage transitions, approval, review gates, and verification.

Natural-language implementation requests and explicit invocation of this skill select automatic delivery unless the user gives a narrower stop instruction. An explicit `ec-plan`, `ec-build`, `ec-review`, `ec-fix`, or `ec-test` request selects only that stage for the current turn. After plan approval, plain language such as `continue` resumes automatic delivery.

Resolve an explicit task path first, then an unambiguous task from the conversation or repository record. For a clear new request, create the next numbered task from `tasks/TEMPLATE.md` with a descriptive filename. Ask only when task identity is materially ambiguous; never infer it from modification time or unrelated roadmap entries.

In automatic delivery, clarify material gaps and present a plan including the technical solution. Wait for plan approval only if it has not already been given. Then implement, spawn separate architect and security reviewers in parallel, resolve findings, and verify the final implementation automatically.

Keep the task's delivery record current. Report missing prerequisites or failed required gates as blocked/unverified. Do not silently substitute self-review for the two independent reviewers.

When assigned a reviewer role or a review-only request, perform only that review and return findings; do not start this implementation workflow.
