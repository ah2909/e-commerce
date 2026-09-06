# Project delivery workflow

This workflow applies only to this e-commerce project. It uses the architecture, invariants, and testing strategy already documented here. The task is the unit of delivery; do not expand into adjacent roadmap items.

## Sequence and approval

Clarify -> plan and technical solution -> user approves plan -> implement -> architect and security reviews in parallel -> fix and re-review -> final testing -> evidence report.

The user approves the plan once. Check the conversation before asking; approval persists across turns and context resets. A task checkbox or an agent's own statement is not evidence of user approval. In automatic delivery, implementation, reviews, in-scope fixes, and testing continue automatically after approval. Stage-only execution follows the command boundaries below.

Ask again only when a material requirement is unresolved, a proposed change exceeds the approved scope, or an irreversible schema/public API decision is not covered by the approved plan and project docs. Present the concrete decision and its impact first. Tool permissions still apply. This workflow does not grant publishing, merging, production migration, deployment, or messaging permission.

Read-only investigation and preparation of the task/plan may happen before approval. Review-only requests stay review-only and do not start implementation or require a new plan approval. Small changes can use a few sentences for the plan and review reports; keep the same gates.

## Reusable stage commands

These project-local skills are reusable prompt commands. Supply a task path after the command, for example `$ec-plan tasks/001-bootstrap.md`. Subsequent commands may omit the path when the active task is unambiguous.

| Command | Work performed | Stop boundary |
| --- | --- | --- |
| `$ec-plan` | Clarify requirements and prepare the plan, technical solution, and test strategy. | Present the plan; do not implement, review, or run the final test suite. |
| `$ec-build` | Implement the approved plan, including tests/contracts, and run focused development checks. | Report the implementation and pending reviews; do not spawn reviewers or enter final verification. |
| `$ec-review` | Spawn independent architect and security reviewers in parallel on the existing artifacts, including fixes from an earlier review. | Report verdicts and findings; do not fix code or run the final test suite. |
| `$ec-fix` | Fix supported findings within the approved plan and run relevant regression tests. | Report fixes and checks; leave findings awaiting reviewer confirmation and do not start re-review or final verification. |
| `$ec-test` | Run applicable final verification and report actual evidence. | Report results; do not fix implementation, tests, or configuration and do not start reviews. |
| `$ecommerce-delivery` | Explicitly select automatic delivery and resume from the first unmet applicable gate. | Continue through approved implementation, reviews, fixes, and verification until complete or blocked. |

### Shared command rules

An `ec-*` command selects stage-only execution for the resolved task. Record the execution mode, last command, completed stage, pending gates, and next suggested command in that task's delivery record. Merely loading a skill, approving a plan, or saying `continue` does not switch stage-only execution to automatic delivery. An explicit `$ecommerce-delivery` invocation or an explicit user request for automatic delivery changes the mode for that task. Other tasks retain their own modes; automatic delivery remains the default for tasks without a selection.

Resolve the task from an explicit path first, then the unambiguous active task in the conversation, then a single task explicitly recorded as active. Do not select the first unfinished roadmap item or use modification time as authorization. If the task is missing or ambiguous, ask for its path before dependent actions. A new outcome provided with `$ec-plan` may create a task from `tasks/TEMPLATE.md`; other commands do not invent a task or plan. If several stage commands are supplied without a clear sequence/stop instruction, clarify the intended stage rather than silently chaining them.

`$ec-build` and `$ec-fix` require actual user approval of the applicable plan revision. Their names alone do not approve an unapproved plan. Reuse approval established in the conversation; if a new session cannot establish it from available conversation evidence, ask the user to confirm the specific recorded plan before edits. Plan revisions that materially change scope require approval of the revision. Plan, review, and test commands may inspect existing work without implementation approval; they do not grant permission to change it.

Stage commands may read supporting files and update the active task's evidence record. `$ec-plan` may edit the task/plan document only. `$ec-review` keeps implementation artifacts stable; only the parent writes the task report after both read-only reviewers return. `$ec-test` may create ordinary test outputs and use disposable local test dependencies where already authorized, but must report missing tooling/dependencies rather than implementing bootstrap or repairing configuration. No command authorizes production data changes or external publishing.

At each stop, distinguish stage completion from overall task completion and list unmet gates. A failed test remains failed; a fix remains awaiting re-review until the affected reviewer inspects it. Reuse review/test evidence only when it still covers the current artifacts. Never mark the task complete until all applicable acceptance, review, and verification gates pass. If `continue` is ambiguous at a stage boundary, ask which command to run.

To plan again after implementation, `$ec-plan` revises the task's plan and stops; it does not discard existing edits or prior evidence. Identify which approval, reviews, or tests the revision invalidates. `$ec-review` may review existing unapproved work but must report the missing approved plan as a limit, not fabricate approval or require implementation to start. `$ec-fix` with no actionable in-scope findings reports that fact and stops. `$ec-test` with outstanding reviews or findings still reports useful test results and leaves those gates pending.

## 1. Clarify and plan

Read `AGENTS.md`, the active task, relevant `docs/`, and existing code patterns. Inspect the working tree and identify pre-existing changes. If Git is unavailable, record the initial affected files and compare their content explicitly; never invent a base commit.

Ask focused questions only about material gaps that inspection cannot resolve. State reasonable low-impact assumptions. For new tasks, use `tasks/TEMPLATE.md`; keep the delivery record in the active task instead of creating a report per phase.

Present the observable outcome, scope exclusions, affected modules, proposed technical solution, new dependencies with reasons, and ordered implementation steps. Identify applicable tenant, commerce, compatibility, migration, and failure/retry concerns. Map acceptance criteria to tests and name the verification commands. Surface unresolved decisions before asking for plan approval.

For migrations, include deployment order, locking/backfill implications, and a recovery or forward-fix strategy. Record an ADR only for a durable architecture decision. Scale detail to the change.

## 2. Implement the solution

Implement the approved slice with its tests, contracts, and necessary operational documentation. Run focused tests during development for fast feedback; final testing is a gate, not the first time tests are written or run.

Keep one agent responsible for edits and integration. Preserve unrelated changes. The parent remains responsible for checking the complete result, even when review work is delegated.

## 3. Independent reviews

After the implementation and focused tests are ready, spawn two separate reviewer agents in parallel: `architect_reviewer` and `security_reviewer`. Their instructions are in `.codex/agents/architect_reviewer.toml` and `.codex/agents/security_reviewer.toml`. If the runtime cannot select named agents, pass the applicable `developer_instructions` explicitly to two generic agents.

Give each reviewer the approved task/plan, relevant project docs, the change scope and base when available, and executed test evidence. Include new and untracked files and relevant callers, schemas, and tests. Do not seed either reviewer with the other reviewer's conclusions. Reviewers must independently inspect the actual artifacts rather than trusting the implementation summary.

Keep implementation files stable during the parallel reviews. The parent may inspect test coverage and prepare final verification while reviewers work. Identify the reviewed version using a commit plus working-tree diff or file hashes; approval of an earlier version does not cover later substantive changes.

Reviewers are read-only: no file edits, new agents, changes to permissions, external mutations, or workflow restarts. The role instruction remains binding even if a runtime does not enforce the custom agent's read-only sandbox. If independent agents are unavailable, report the review gate as blocked; a self-review is useful but does not replace it.

Each reviewer returns:

- Scope/version inspected and checks actually performed.
- Findings with severity, file/line, a concrete failure or exploit scenario, impact, and a suggested correction or test.
- A verdict: pass, changes required, or blocked, plus residual uncertainty.

Critical/high findings, violations of project invariants or acceptance criteria, and missing evidence necessary to establish correctness block completion. Confirmed defects introduced by the task must be fixed. Style preferences and speculative improvements are advisory; do not expand scope to implement them. Record false positives with evidence and obtain the relevant reviewer's agreement. Material issues outside the approved scope need a concrete user decision and remain visible.

## 4. Fix and re-review

The implementing agent consolidates the findings, fixes supported issues, and adds meaningful regression tests. Ask the affected reviewer to verify the fix and inspect the resulting changes; use both reviewers when changes affect both concerns. Substantive changes after a pass invalidate the affected review. Editorial updates to the delivery record alone do not.

Continue while making progress. If a required external prerequisite is unavailable or repeated attempts cannot resolve a finding, report the blocker and decision needed instead of declaring success or repeatedly attempting the same approach. Never weaken tests, hide findings, or silently waive a gate to finish.

## 5. Final testing

Use `docs/testing-strategy.md`. Tenant resources require cross-store denial tests. Transaction, uniqueness, and concurrency claims require real PostgreSQL integration tests. Select the relevant money, inventory, order snapshot, payment/webhook, and event retry cases from that strategy; do not implement future modules merely to add tests.

For backend changes, run `make fmt-check`, `make lint`, `make test`, `make test-integration`, and `make vulncheck`, or `make verify` once it runs all five and propagates failures. Record the exact commands, results, and environment limits. Rerun affected checks after any code fix and ensure the final implementation is covered by both reviews. Do not repeat a successful full suite without a change or unresolved concern.

For documentation/workflow-only changes, inspect changed instructions and local links and parse changed configuration with an available format-specific tool. Validate changed GitHub Actions YAML with a YAML parser or actionlint when available. No project-specific workflow validation script is required. Report unavailable required validation as unverified.

## 6. Completion evidence

Update the active task with the actual approval, final review results and dispositions, acceptance/test evidence, and outstanding risks. Report behavior, design choices, changed modules/files, exact verification outcomes, and remaining limitations. Mark complete only when acceptance criteria and all applicable review/test gates pass.

Passing a development task establishes evidence for that slice. Production release readiness also depends on the applicable operational work in the roadmap, including deployment configuration, backups/restore, monitoring, and load testing.

## Project configuration and CI

The reusable skill is `.agents/skills/ecommerce-delivery/SKILL.md`. The project `.codex/config.toml` enables agents and limits concurrent reviewer threads to two. Reviewers inherit the selected parent model and reasoning effort. Global configuration is unchanged.

Start Codex in this project and invoke `$ecommerce-delivery`, or ask it to implement an active task using the project workflow. If the skill does not appear, restart the session. Project `.codex/` settings require a trusted project and a runtime supporting custom agents; the explicit-instructions fallback above covers runtimes that only expose generic agent spawning.

GitHub Actions is the assumed CI provider. `.github/workflows/ci.yml` runs on pushes, pull requests, and manual dispatch. Its `Backend verification` job requires bootstrap artifacts, installs Go from `go.mod`, installs pinned tools through `make tools`, runs the five verification targets, and starts/stops disposable dependencies around integration tests.

The backend job intentionally fails with a bootstrap explanation while `go.mod` or `Makefile` is missing. There are no placeholder passing tests or conditional skips for missing backend files. `tasks/001-bootstrap.md` owns the implementation of the Make targets, Compose dependencies, and test harness. This workflow setup does not implement that service task.

The job uses a GitHub-hosted disposable runner, read-only repository permissions, checkout without persisted credentials, pinned action commits, and a bounded runtime. PR code runs under `pull_request`, without deployment secrets. CI runs backend checks; the local agents perform the independent reviews. CI does not authenticate or enforce the AI review verdicts.

After initializing and publishing a Git repository, enable Actions and configure branch protection/rulesets to require `Backend verification` and human review as appropriate. Those hosted settings are not configured by local files. If an existing ruleset requires the retired `Workflow configuration` check, remove that requirement. Keep action pins and tool versions maintained. Never add production credentials to this verification workflow.

## Configuration references

- [Repository skill discovery](https://learn.chatgpt.com/docs/build-skills)
- [Custom agents and inherited settings](https://learn.chatgpt.com/docs/agent-configuration/subagents)
- [Project configuration and trust](https://learn.chatgpt.com/docs/config-file/config-basic)
- [GitHub Actions workflow syntax](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax)
