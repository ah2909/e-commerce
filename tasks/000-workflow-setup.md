# Task: Configure the project delivery workflow

Historical record: the optional workflow validator, its tests, and its CI job were later removed at the user's request. Verification results below describe the original setup; current instructions are in `docs/development-workflow.md`.

## Outcome and scope

Configure this project for plan approval followed by automatic implementation, parallel independent architect/security reviews, fixes, and testing. Include executable GitHub Actions configuration. Service bootstrap and hosted repository settings remain separate tasks.

The user requested this setup and confirmed the single plan approval checkpoint, separate parallel reviewers, and inclusion of executable CI in the conversation. These choices configure future task approvals; they do not approve the bootstrap implementation plan.

## Delivery record

- Stage: complete for workflow configuration, 2026-09-06.
- Workflow: [delivery procedure](../docs/development-workflow.md).
- Changed areas: project instructions, reusable skill, project agent settings/roles, README, task template, bootstrap CI contract, workflow validator, and GitHub Actions.
- Design: one implementing agent owns edits; two independent reviewers inspect stable artifacts; substantive fixes require affected re-review; required failures remain blocking.
- Architect review: independent `architect_review` agent, pass, no findings.
- Security review: independent `security_review` agent, pass, no findings.
- Reviews covered all 11 configuration/implementation files; this record was added afterward as an evidence-only update.

## Verification evidence

The Python executable used was `C:\Users\caoan\AppData\Local\Programs\Python\Python312\python.exe` (Python 3.12.8). Each Python command below was invoked with that executable because the sandbox could not access it directly.

| Command or invocation | Result |
| --- | --- |
| `scripts/check_workflow.py` | Passed TOML parsing, skill metadata, required files, and local Markdown links. |
| `C:\Users\caoan\.codex\skills\.system\skill-creator\scripts\quick_validate.py .agents/skills/ecommerce-delivery` | Passed: `Skill is valid!` |
| `C:\Users\caoan\AppData\Local\Temp\ecommerce-workflow-config\validate_behavior.py` | Passed valid configuration and five isolated negative scenarios; GitHub Actions YAML parsed successfully. |
| `make fmt-check` | Failed: target does not exist; no Makefile yet. |
| `make lint` | Failed: target does not exist; no Makefile yet. |
| `make test` | Failed: target does not exist; no Makefile yet. |
| `make test-integration` | Failed: target does not exist; no Makefile yet. |
| `make vulncheck` | Failed: target does not exist; no Makefile yet. |
| `git ls-remote https://github.com/actions/checkout.git refs/tags/v6.0.2` | Resolved the pinned commit `de0fac2e4500dabe0009e67214ff5f5447ce83dd`. |
| `git ls-remote https://github.com/actions/setup-go.git refs/tags/v6.3.0` | Resolved the pinned commit `4b73464bb391d4059bd26b0524d20df3927bd417`. |
| `codex --version` | `codex-cli 0.153.0`; custom-role auto-loading was not independently exercised. |

Negative scenarios verified rejection of invalid TOML, broken local links, missing skill metadata, an unsafe/incomplete reviewer definition, and a missing CI file. They ran in temporary copies without changing project files.

Both reviewers inspected these SHA-256 versions:

- Delivery procedure: `733A476EB5F406F2F0F407B059E98A8CBAA56E32F8D0C48C5E62A699434EECEC`
- Validator: `FF44CD36F6B95F5C6CC5D0A9741770EFC64895B59963DE1EE2F0A65EF13C19F7`
- CI workflow: `805E97A60898FFD87F80D4CD7438930CD49DE31B7FD8B03669BA5BF0E9F8415E`

## Remaining limitations

Backend verification is unavailable until [bootstrap](001-bootstrap.md) supplies the Go module, Make targets, dependency setup, and tests. The backend CI job deliberately fails on missing prerequisites. These backend failures do not block this documentation/configuration-only task, but they block claims that the service is verified.

The workspace is not initialized as a Git repository. Hosted CI, branch protection, deployment, and production readiness have not been verified or configured. Custom agent schema was checked against official documentation; this session exercised two generic reviewer agents using the project role instructions.
