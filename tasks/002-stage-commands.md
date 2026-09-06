# Task: Reusable commands for individual delivery stages

Historical record: the optional workflow validator, its tests, and its CI job were later removed at the user's request. Results below describe validation performed before that removal. The six skills remain available.

## Context and outcome

Extend [the delivery workflow](../docs/development-workflow.md) with five project-local skills: `ec-plan`, `ec-build`, `ec-review`, `ec-fix`, and `ec-test`. Each performs one stage and stops. Preserve `$ecommerce-delivery` for explicitly selected automatic delivery.

## Scope and constraints

- Add stage skills and shared task selection, approval, mode, evidence, and stop rules.
- Update project instructions, README, task template, and skill validation.
- Keep independent architect/security reviews and existing completion gates.
- No service bootstrap, deployment, global configuration, or legacy slash-command installation.

## Acceptance criteria

- [x] All five commands exist as valid project-local skills.
- [x] Each command defines its stop boundary and reports pending gates; independently inspected.
- [x] Build/fix require the applicable approved plan; read-only review/test do not imply approval.
- [x] Stage-only mode survives plan approval and implicit loading of the automatic skill.
- [x] Ambiguous tasks are clarified before dependent work.
- [x] Architect/security reviews and relevant configuration validation pass.

## Delivery record

- Active task: no
- Execution mode: automatic for implementing this approved configuration change.
- Stage: complete
- Plan revision and approval: user replied `ok approve` to the five-command proposal on 2026-09-06.
- Plan: add thin skills backed by shared stage rules; extend existing validation across six skills; add negative validation cases; run separate reviews and final checks.
- Verification: official skill validator for each skill, project link/metadata/TOML checks, isolated negative tests, and independent behavioral review of stage boundaries.
- Architect review: independent `stage_architect` agent passed with no findings; inspected all changed files and walked through approval, mode, ambiguous-task, missing-dependency, and re-review scenarios.
- Security review: independent `stage_security` agent passed with no findings; independently ran the project validator and inspected approval evidence, mutation boundaries, stale-review handling, and CI permissions.
- Final evidence: configuration checks, all six official skill validations, seven unittest cases, and CI YAML parsing passed.

## Verification and limits

Commands used `C:\Users\caoan\AppData\Local\Programs\Python\Python312\python.exe`:

- `-B scripts/check_workflow.py`: passed required-file, TOML, metadata, and local-link validation for all six skills.
- `-B -m unittest discover -s scripts -p 'test_*.py' -v`: seven tests passed, including rejection of each missing stage skill, malformed metadata/TOML, broken skill links, and a writable reviewer.
- `-B C:\Users\caoan\.codex\skills\.system\skill-creator\scripts\quick_validate.py <skill-directory>`: passed separately for `ec-plan`, `ec-build`, `ec-review`, `ec-fix`, `ec-test`, and `ecommerce-delivery`.
- PyYAML `safe_load` of `.github/workflows/ci.yml`: passed; hosted CI was not run.

Reviewed SHA-256 identifiers: workflow `CD383A4C58B2C75E732FB4FD034D2142470566EAFB456875982F207AC3987948`, validator `9AE8CC03A8978EFCA2A12F1220EAA9953116F308ECCABF54092FB08ADF7587A4`, and CI `3C0F29C571DD3D564940B3D69617B9157D28EB26581878DD865EEAA9D623FD3A`. This completion-record update is editorial and does not change the reviewed implementation.

Stage behavior was verified through independent instruction/scenario inspection, not an end-to-end runtime development task. These commands guide the agent; they are not a deterministic authorization engine. Skill discovery may require a session restart.

`make fmt-check`, `make lint`, `make test`, `make test-integration`, and `make vulncheck` were not rerun: there is still no Makefile or Go module, and this change affects workflow configuration only. Backend verification remains pending bootstrap. Git is not initialized, so no Git diff or hosted CI result is claimed.
