---
name: "ecommerce-delivery"
description: "Deliver implementation tasks in this multi-store e-commerce project through an approved plan, implementation, independent architect and security reviews, fixes, and testing. Also use when resuming an active project task."
---

# E-commerce delivery

Read the project-root AGENTS.md, the active task, and [the delivery workflow](../../../docs/development-workflow.md). Follow that workflow as the source of truth for stage transitions, approval, review gates, and verification.

An explicit invocation of this skill selects automatic delivery for the resolved task unless the user also gives a narrower stop instruction. If this skill is loaded implicitly, preserve an existing stage-only selection or a narrower user request; do not override an `ec-*` command. Resolve the task and mode using the delivery workflow.

In automatic delivery, clarify material gaps and present a plan including the technical solution. Wait for plan approval only if it has not already been given. Then implement, spawn separate architect and security reviewers in parallel, resolve findings, and verify the final implementation automatically.

Keep the task's delivery record current. Report missing prerequisites or failed required gates as blocked/unverified. Do not silently substitute self-review for the two independent reviewers.

When assigned a reviewer role or a review-only request, perform only that review and return findings; do not start this implementation workflow.
