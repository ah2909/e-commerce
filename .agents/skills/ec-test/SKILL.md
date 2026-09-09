---
name: "ec-test"
description: "Run final verification for an e-commerce task and report results without repairing code when the user requests testing only or invokes ec-test."
---

# ec-test

Read [the project workflow](../../../docs/development-workflow.md), especially Commands and section 5. Resolve the active task and select stage-only execution for this turn using the shared rules.

Run applicable verification against the current artifacts and record exact commands and outcomes. Test execution does not approve implementation or replace missing reviews. Ordinary test outputs and already-authorized disposable local dependencies are allowed; do not edit implementation, tests, configuration, or add missing bootstrap tooling to make checks pass.

Report results and unmet gates, then stop without fixes or reviews. Missing checks are unverified, not passing. Mark the overall task complete only if acceptance criteria and both independent reviews also cover the current artifacts with no blocking findings.
