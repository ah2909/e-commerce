# Task: Stabilize tenant Identity and admin-only store provisioning

## Context

This is roadmap slice 3 and builds on the archived [store-context task](archive/003-store-context.md). The target behavior is defined by [product requirements](../docs/product-requirements.md), [architecture](../docs/architecture.md), [domain model](../docs/domain-model.md), [database design](../docs/database-design.md), [testing strategy](../docs/testing-strategy.md), [ADR 002](../docs/decisions/002-shared-schema-tenancy.md), and [ADR 003](../docs/decisions/003-identity-sessions-and-store-authorization.md).

Revision 4 implemented host-selected store-owned users, passwords, Google/Facebook identities, opaque rotating sessions, guests, memberships, fixed roles, audit, Redis rate limits, onboarding, migration 2, and OpenAPI. Independent architect/security reviews returned changes required. The requirements then changed from public store onboarding to platform-admin-only provisioning.

Revision 5 is the only current plan. It preserves the useful revision 4 implementation, fixes every recorded blocker and current lint finding, and deliberately uses one deployment-managed platform admin token instead of platform-operator accounts, invitations, or activation workflows so the system can ship sooner.

## Outcome

- Tenant APIs resolve a store from a validated subdomain before authenticating a store-owned user or guest.
- The same email or Google/Facebook subject can create independent accounts at different stores, while every credential and session is rejected on another store host.
- Only a request authenticated by the deployment's platform admin token can create a store.
- Platform provisioning atomically creates the store, owner account/password, owner membership, and accurate platform-actor audit record. It does not create or return an owner session.
- The owner logs in normally on the new store subdomain using the initial password supplied by the platform administrator.
- Workforce operations use current store-local membership and deny-by-default permissions; guests never satisfy workforce authorization.
- All revision 4 review findings and the 12 current lint findings are resolved with focused regression evidence.
- The OpenAPI contract, runtime configuration, design/status docs, and older task notes describe the same behavior.

## In scope

- Keep the current store-owned user, password, social identity, opaque user/guest session, host-selection, membership, permission, rate-limit, audit, and OpenAPI foundations.
- Protect store creation with a deployment-managed platform bearer token and register it only on the configured base domain.
- Keep POST /api/v1/onboarding/stores to minimize client churn, but document platform bearer authentication and return store/owner identifiers without owner access/refresh tokens.
- Refactor onboarding preparation/persistence so provisioning creates an owner account without creating a session.
- Represent the store-created audit actor as the platform administrator rather than falsely attributing it to the new owner.
- Fix membership role-default privilege escalation and concrete cross-module infrastructure coupling.
- Fix social transaction/exchange atomic consumption, error classification, callback configuration, redirect validation, and replay behavior.
- Add independent peer plus store-scoped credential limits for refresh and social exchange.
- Add a trusted-proxy client-address boundary for rate limiting without trusting forwarded host for tenancy.
- Normalize malformed membership user IDs to a documented non-500 response.
- Fix every current golangci-lint finding in the affected code and tests.
- Update API, configuration examples, README, development status, ADR 003, database design, archived task 003 supersession note, and this delivery record.

## Out of scope

- Database-backed platform operators, multiple platform roles, platform sessions, tenant invitations, activation links, email verification, forced initial-password change, MFA, password reset/change, and operator UI.
- SaaS subscription billing, custom tenant domains, account linking across stores, custom tenant roles, and general permission editing.
- Catalog, Pricing, Inventory, Customers, Carts, Orders, VietQR, MoMo, Refunds, Shipping, outbox workers, and other downstream roadmap slices.
- Production credential creation, deployment, migration execution, or destructive reset.
- Live Google/Facebook certification; local signed fixtures and documented sandbox/UAT gaps remain.

## Accepted speed-first tradeoff

The platform administrator supplies the initial owner email and password over the protected provisioning request and communicates them out of band. The API does not return an owner session. The platform token is one high-entropy deployment secret, so audit can identify the deployment administrator but not an individual human. Individual platform accounts, invitations, forced password change, and token rotation without restart are deferred and remain visible operational risks.

## Invariants and security constraints

- Store scope comes from the validated direct host, never a body, tenant header, path store ID, Forwarded, or X-Forwarded-Host value.
- The base-domain onboarding route cannot resolve or accept tenant user/guest bearer tokens as platform authority.
- PLATFORM_ADMIN_TOKEN is at least 32 random bytes encoded as unpadded base64url, is supplied by a secret manager/environment, is compared in constant time, and is never logged or persisted.
- A missing or invalid platform token returns a generic unauthorized problem before password hashing or database mutation.
- Every tenant identity, membership, session, token, Redis key, audit record, and repository operation carries explicit store scope.
- Opaque bearer/refresh tokens contain at least 256 random bits and are stored only as SHA-256 digests.
- Access tokens last 15 minutes; rotating sessions have a 30-day absolute lifetime; reuse compromises/revokes the family.
- Passwords are 12–128 bytes and use bounded cancellable Argon2id admission. Missing users and wrong passwords follow equivalent verification behavior.
- Google/Facebook state binds store, provider, browser, purpose, PKCE/nonce, and allow-listed return path.
- Wrong-store, wrong-browser, or wrong-provider attempts do not consume another valid state/exchange record. A matching callback consumes state once before provider exchange.
- Provider rejection and invalid claims are permanent; network/5xx/timeouts are transient, but a consumed authorization code always requires a fresh social start.
- Redirect targets are constructed only from a server-resolved store subdomain, configured base domain, and exact allow-listed path.
- Rate limiting uses an independent client bucket before identity/token buckets. Forwarded client addresses are accepted only from configured trusted proxy CIDRs.
- Trusted proxy headers affect rate identity only and never tenant host selection.
- Role and membership changes deny by default, use tenant-local user ID, preserve one active owner under concurrency, and write audit atomically.
- Migration 2 leaves no default that silently grants owner.
- Required audit failure rolls back provisioning and membership mutations.

## Authorization policy

| Permission | Owner | Admin | Staff |
| --- | --- | --- | --- |
| store.read | yes | yes | yes |
| membership.list | yes | yes | no |
| membership.create | yes | no | no |
| membership.update | yes | no | no |

Payment confirmation/refund permissions belong to later Payments work. The design baseline reserves them for owner/admin by default and explicit staff permission later; this task does not introduce premature payment permissions.

## HTTP contract

### Platform route

- POST /api/v1/onboarding/stores runs only on the configured base domain.
- Security scheme: platformAdminBearer.
- Request: subdomain, store_name, optional Vietnam defaults, owner email, and initial password.
- Success 201: store_id, subdomain, and owner_user_id only; Cache-Control: no-store.
- Failure: 400 invalid input, 401 missing/invalid platform token, 409 provisioning conflict, 429 client rate limit, 503 bounded dependency/hash admission failure.
- Tenant user/guest tokens do not authorize the route.

### Tenant routes

- Password registration/login/session routes remain under /api/v1/auth on a resolved store subdomain.
- Google/Facebook callbacks remain on the configured authentication host and advertise that host in OpenAPI.
- Guest routes remain under /api/v1/guest-sessions on a resolved active store.
- Store and membership routes remain host-scoped without a caller-selected store ID.
- Problems use code, message, details, and request_id without raw dependency or identity-enumeration details.

## Technical solution

### Platform authentication and provisioning

1. Add validated PLATFORM_ADMIN_TOKEN configuration. Decode unpadded base64url and require exactly 32 bytes. Store only the byte slice/digest in memory and redact configuration errors/logs.
2. Add a small platform authentication middleware owned by platform HTTP composition. It accepts Bearer only on the base-domain provisioning route, performs constant-time comparison, and injects a distinct platform-admin principal.
3. Keep the existing onboarding endpoint path but require platform middleware before rate limiting, decoding, password hashing, or service execution.
4. Split Identity preparation into account preparation and optional session issuance. Tenant self-registration creates both; platform provisioning persists only user/password.
5. Return store and owner IDs from provisioning. Do not mint an unused session or expose owner credentials in the response.
6. Model audit actor as type plus identifier. Use platform_admin/deployment for store provisioning and user/<user_id> for membership changes. Keep store_id because the resulting audit record belongs to the new store.

### Module boundaries and migration

1. Preserve the application-level onboarding Repository port as the atomic unit-of-work boundary.
2. Replace concrete Identity/Stores PostgreSQL repository fields with narrow transaction-aware writer interfaces at composition/infrastructure boundaries. Each owning module retains its SQL.
3. Keep migration 2 as the undeployed forward migration already approved for pre-release editing.
4. Add membership role, backfill existing task-3 rows to owner, enforce NOT NULL/check, then drop the default so inserts must state role.
5. Change audit actor columns/constraints in migration 2 to accurately represent platform and tenant actors. Update migration inspection, empty-schema, upgrade, and rollback-on-audit-failure tests.
6. Do not add platform operator tables or a new migration solely for the configured token.

### Social state and provider failures

1. Replace unconditional Redis GETDEL operations with atomic Lua-backed compare-and-delete operations.
2. Callback consumption compares provider and browser digest before deletion; mismatches preserve the valid state.
3. Exchange consumption compares store before deletion; a wrong-store attempt preserves the valid exchange.
4. A matching callback consumes state before using the provider code. Typed provider errors distinguish rejected/invalid claims from timeout/network/5xx.
5. Map permanent failures to a non-retryable authentication problem and transient failures to 503 with an explicit fresh-start requirement because state/code have been consumed.
6. Validate exact configured callback scheme, auth host, no port/query/fragment, and provider-specific served path.
7. Build redirect Location from validated server-owned components and prove all untrusted return paths, subdomains, schemes, ports, and protocol-relative targets are rejected.

### Rate identity and handler correctness

1. Add TRUSTED_PROXY_CIDRS as an optional bounded list. With no trusted proxy match, use RemoteAddr and ignore X-Forwarded-For. With a trusted direct peer, parse a bounded X-Forwarded-For chain from right to left and choose the first untrusted valid address.
2. Use the resolved client address for independent peer rate limits. Keep store plus canonical identity/token digest in secondary keys.
3. Apply peer then credential limits to registration, login, refresh, social exchange, guest creation, and platform provisioning as applicable.
4. Parse membership user ID before repository invocation and return documented 400 or non-enumerating 404 rather than 500.
5. Set the social binding cookie Secure, HttpOnly, SameSite=Lax, scoped to the callback path, and expire it with matching attributes.
6. Guard Argon2 derived-key length before int-to-uint32 conversion; use errors.Is for wrapped Redis sentinels and lowercase error strings.
7. Update tests to use context-aware requests, slog discard handlers, and lint-clean selectors.

## Compatibility and deployment

- The onboarding request fields and path remain stable; authentication becomes mandatory and the response no longer contains owner tokens. Because task 004 is unreleased, this is an intentional pre-release contract correction.
- Existing tenant password/social/guest/store/membership routes remain host-scoped.
- Add the platform token and trusted proxy variables to configuration and .env.example. Production startup fails safely if the platform token is missing/invalid.
- Deploy revised migrations before revised code. Migration 2 is confirmed undeployed outside disposable databases; if that changes, stop and create a forward migration instead of editing deployed history.
- Customer-managed deployments configure the same platform token and may use it for their own administrator.
- Rotate the platform token by updating the deployment secret and restarting instances; overlap rotation is deferred.

## Ordered implementation plan

1. Update this task, target docs, and archived task 003 notes so revision 5 is authoritative and superseded assumptions are explicit.
2. Add platform token/trusted-proxy configuration, platform principal/auth middleware, and exhaustive configuration/auth/address tests.
3. Refactor onboarding and Identity account preparation so platform provisioning is authenticated, sessionless, and returns the corrected response.
4. Introduce narrow transaction writer interfaces, correct audit actor modeling, remove the owner default, and extend migration/rollback/concurrency tests.
5. Implement atomic bound Redis consumption and typed social provider outcomes with wrong-store/browser/provider and consumed-state regression tests.
6. Apply independent peer/credential rate policies and trusted-proxy resolution to all relevant handlers.
7. Fix malformed membership handling, exact callback validation/OpenAPI servers, redirect/cookie handling, and all lint findings.
8. Update OpenAPI, README, configuration examples, ADR/database/status docs, and task records to match actual behavior.
9. Run focused formatting, lint, unit, migration, PostgreSQL, Redis, API, concurrency, and contract checks. Record environment limitations and stop before independent reviews.

## Verification strategy

| Requirement/risk | Required evidence |
| --- | --- |
| Platform-only provisioning | Unit/API tests for valid token, missing/invalid/tenant token denial, base-domain enforcement, no service call on denial, redacted logs, rate limit, and response without owner session |
| Atomic owner creation | PostgreSQL tests for store/user/password/owner/audit success and rollback on each failure; concurrent duplicate subdomain/email behavior |
| Audit attribution | Schema/domain/repository tests proving platform actor on provisioning and tenant user actor on membership changes |
| Tenant isolation | Same-email/social-subject cross-store coexistence; cross-host access/refresh/logout/social/guest/membership denial |
| Session security | Digest-only storage, TTL, rotation, concurrent refresh, reuse compromise, logout, disabled user, and suspended store |
| Social atomicity | Real Redis tests proving wrong store/browser/provider preserves state/exchange, matching use consumes once, replay fails, and concurrent use has one winner |
| Provider failure semantics | Google/Facebook fixtures for rejected code/claims, 5xx, network error, timeout, consumed state, fresh-start contract, and no misleading retry |
| Callback/redirect safety | Exact config-path validation, auth-host OpenAPI server, allow-listed return path, untrusted scheme/host/port/protocol-relative rejection |
| Rate limiting/proxy | Varied-token peer exhaustion, secondary store/digest buckets, trusted-CIDR parsing, spoofed header denial, chain bounds, IPv4/IPv6, and cross-store blast-radius tests |
| Membership safety | Malformed ID non-500/no repository call, explicit role inserts, deny-by-default matrix, concurrent last-owner preservation, and audit rollback |
| Lint/security findings | make lint passes without blanket suppressions; any narrow suppression explains only a proven false positive in one line |
| Contract and migration | OpenAPI reference/operation assertions; fresh and version-1 migration; repeat/status; schema constraint/index inspection |
| Focused build | make fmt-check, make lint, make test, make test-integration, make vulncheck where dependency evidence is available, and git diff --check |

## Prior evidence and invalidation

- Revision 4 focused unit, PostgreSQL/Redis integration, migration, provider-fixture, concurrency, vet, and diff checks passed on its recorded working tree.
- Revision 4 independent architect review found one high and four medium issues; security review found three blocking medium issues and one low issue.
- Current task 005 status checking later found 12 lint issues, including cookie/redirect security warnings, integer narrowing, wrapped Redis error handling, and test/static-analysis defects.
- Revision 5 invalidates revision 4 onboarding, audit-actor, module-boundary, callback, social consumption, rate-limit, migration-default, OpenAPI, review, and final-test evidence.
- Unaffected revision 4 tests remain useful development coverage but do not replace review and final verification of revision 5.

## Revision 5 independent review

Reviewed on 2026-09-09 against base `76caad7783c8accd52fd031113c10c7d74373223` plus the current working tree. The frozen implementation hashes remained unchanged through both reviews; representative hashes were API composition `C6F260F1`, platform authentication `2C1B615E`, Identity social application `016B6D31`, Redis social store `216EAE07`, migration 00002 `46624BB8`, and OpenAPI `941B5FF0`.

The architect reviewed the plan, architecture/domain/database/testing documents and ADRs, module boundaries, composition, migrations, transactions, providers, rate limits, contracts, and test coverage. Verdict: **changes required**.

The security reviewer independently traced host and tenant authority, platform/user/guest authentication, membership authorization, tenant-scoped persistence, Redis Lua consumption, provider failures, proxy/rate identity, redirects/cookies, audit, migration constraints, OpenAPI, and negative tests. Verdict: **changes required**.

### Consolidated findings

1. **High: a committed platform-admin credential can authenticate a production-mode Make launch.** `Makefile` supplies and exports the repository-known development token, while `internal/platform/config/config.go` accepts it for every `APP_ENV`. An operator who runs `make run-api APP_ENV=production` without an override gets a working known administrator credential, allowing unauthorized store provisioning. Remove the privileged default from general Make execution or reject the known local credential outside development/test, and add production configuration regression tests.
2. **Medium: disabled users can continue rotating refresh tokens.** `internal/identity/application/service.go` checks store activity, but `internal/identity/infrastructure/postgres/repository.go` rotates a locked refresh/session without validating current user status. A disabled user's token family can be kept alive and becomes usable after reactivation. Validate active user status inside the same refresh transaction, reject or revoke inactive families, and add real PostgreSQL disable/refresh/reactivation and concurrency tests.
3. **Medium: social exchange can create a session from a stale Redis user snapshot.** `internal/identity/application/social.go` restores the exchange snapshot and `CreateSessionForUser` persists a session without atomically rechecking current user and store status. Suspension or disablement between callback and exchange can therefore mint bearer material. Revalidate both states at exchange time and condition session creation on current active rows, with disable/suspend-between-callback-and-exchange PostgreSQL tests.
4. **Medium: malformed membership-create user IDs still return 500.** `internal/stores/application/manage_memberships.go` wraps a non-empty UUID parse failure as an unrecognized error, and `internal/stores/transport/http/handler.go` maps it to `internal_error`. Existing malformed-ID coverage exercises PATCH only. Normalize create parsing to the documented validation/non-enumerating error and add POST tests proving a non-500 response with no directory/repository call.
5. **Low: unauthenticated base64url inputs allocate before exact-length rejection.** Platform bearer, social continuation/state, and browser-binding values are decoded before checking their only valid encoded length. Near-header-limit requests can force avoidable allocation before rate limiting. Check the exact 43-character unpadded encoding length before decoding and add oversized-input tests proving early rejection without downstream work.

### Fix dispositions and re-review

1. Removed the platform-admin token default from general Make execution, clarified explicit secret injection, and added a production-mode missing-token regression test.
2. Refresh rotation now locks and validates the current user row in the token transaction. An inactive user causes the session and its refresh family to be revoked without minting a successor; focused PostgreSQL tests cover concurrent denial and denial after reactivation.
3. Social session admission now rechecks the store through the Stores application interface immediately before persistence, while Identity locks and validates the current user row in the session transaction. PostgreSQL coverage proves stale active snapshots cannot create sessions after user disablement or store suspension.
4. Membership creation now maps malformed non-empty user IDs to the documented non-enumerating error before directory or repository access; application and POST transport regressions cover the dependency-free rejection.
5. Platform and social base64url secrets now require the exact 43-character wire length before decoding; oversized middleware and social-continuation regressions prove rejection before downstream handling.

Independent re-review confirmed the platform credential, disabled refresh, malformed membership, and oversized-input fixes. The current-user portion of social session admission is also resolved. The security reviewer found that store suspension is still checked outside the session transaction, so that portion remains open as recorded below.

## Revision 5 fix re-review

Re-reviewed on 2026-09-09 against base `76caad7783c8accd52fd031113c10c7d74373223` plus the current working tree. Both reviewers verified the same frozen implementation hashes before and after review: Makefile `B9E67273086C`, Identity service `66826F56CA67`, social application `A6DA5A3B2D68`, Identity PostgreSQL repository `AF65302946DC`, platform authentication `13480EF24942`, membership manager `D99948800E70`, Stores HTTP handler `DB67FE891ADB`, configuration tests `AE31C8C3B8A1`, and Identity PostgreSQL integration tests `0327F52BE2C1`.

The first reviewer launches were unavailable because both agents hit the same temporary account usage limit before inspection. After the user requested continuation, both independent reviewer slots completed read-only retries against the unchanged hashes.

The architect re-reviewed the task, architecture/domain/database/testing documents and ADRs, composition, contracts, migrations, session paths, module boundaries, and focused tests. Verdict: **changes required**.

The security reviewer independently re-reviewed tenant authority, platform/user/guest authentication, session/refresh/social flows, memberships, audit, provider behavior, Redis consumption, proxies/rate limits, redirects/cookies, persistence isolation, migrations, contracts, and tests. Verdict: **changes required**.

### Re-review findings

1. **Medium: authentication and rotation expiry decisions use API-process time instead of PostgreSQL time.** Session and token timestamps originate from the application clock, and user/guest authentication and rotation queries compare expiry against caller-supplied time despite the database design requiring database-authoritative expiry decisions. Clock skew between horizontally scaled API instances can extend or shorten credential lifetimes and make nodes disagree about validity. Use PostgreSQL transaction time for issuance and expiry predicates, return authoritative expirations to the application, and add real PostgreSQL tests with deliberately skewed application clocks for user and guest authentication, refresh, and persisted TTL intervals.
2. **Medium: store suspension is not atomic with session issuance.** `CreateSessionForUser` completes a Stores activity query before starting the separate Identity transaction that locks the user and inserts session/token rows. Suspension can commit between those operations, allowing a bearer pair to be issued after suspension and become usable after reactivation. Introduce a narrow cross-module application unit of work that locks/revalidates store and user state and inserts the session in one PostgreSQL transaction, apply the admission boundary consistently to store-bound session creation/rotation, and add a deterministic real-PostgreSQL interleaving test. Alternatively, a weaker suspension contract requires an explicit product/design decision and matching documentation/tests.

### Re-review dispositions and residual uncertainty

- Production platform credential: resolved by removing the Make default and retaining fail-closed configuration validation.
- Disabled-user refresh: resolved by transactional current-user locking and refresh-family revocation, with concurrent PostgreSQL coverage.
- Stale social user snapshot: resolved for current user status; unresolved for the store-suspension transaction race above.
- Malformed membership creation: resolved with documented non-enumerating rejection before dependencies.
- Oversized unauthenticated base64url inputs: resolved with exact-length rejection before decode.
- No additional cross-store access, privilege escalation, injection, secret/PII logging, Redis replay, provider-validation, redirect, cookie, proxy, rate-limit, audit, or persistence-isolation defect was confirmed.
- Reviewers did not rerun tests. Live provider interoperability, real ingress behavior, production-size migration locking, secret rotation, race-enabled execution, and operational load/abuse behavior remain unproven.

### User-directed deferral

On 2026-09-09, the user explicitly chose to skip both remaining medium findings for the initial MVP. No implementation change was made. The accepted risks are inconsistent credential lifetimes when API/database clocks drift and a narrow suspension/session-issuance race that can leave credentials usable after later store reactivation.

The findings remain documented and the independent review verdicts remain **changes required**; this workflow does not treat risk acceptance as a passed review. Final verification may still run and report useful evidence, but task 004 cannot be marked fully accepted under the current completion policy unless the findings are fixed and re-reviewed or the governing requirements/workflow are explicitly revised.

### Review coverage notes

- The reviewers found positive evidence for direct-host tenant authority, explicit store scoping, distinct platform and tenant principals, sessionless atomic provisioning, typed audit, deny-by-default membership authorization, bound atomic Redis consumption, and safe server-owned redirects/cookies.
- Real PostgreSQL rollback coverage injects audit failure after all preceding provisioning writes, but does not independently inject every subordinate failure point.
- Real Redis concurrency directly covers transaction consumption; exchange concurrency relies on the same Lua primitive but lacks its own concurrent-winner test.
- Live Google/Facebook interoperability, real ingress behavior, production-size migration locking, secret rotation, race-enabled checks, and operational abuse/load testing remain uncertain.

## Final verification attempt

Executed on 2026-09-09 against base `76caad7783c8accd52fd031113c10c7d74373223` plus the current working tree.

| Command | Result |
| --- | --- |
| `make fmt-check` | Failed before formatting checks: the clean Windows tool bootstrap emitted Unix-style `GOBIN="..." go install ...`, which `cmd.exe` rejected with `'GOBIN' is not recognized` |
| `make lint` | Failed before lint analysis for the same golangci-lint bootstrap defect |
| `make test` | Passed across all packages; native Windows Make configuration did not enable the race detector |
| `make test-integration` | Passed across all packages with disposable PostgreSQL, Redis, and Kafka; migration status was already version 2 |
| `make vulncheck` | Failed before vulnerability analysis for the same govulncheck bootstrap defect |

Supplementary read-only `gofmt -l .` and `git diff --check` checks passed. Disposable dependencies were stopped after testing, and neither `.tools` nor `.cache` remained. The failed commands are failed, not unverified or passing; no implementation or bootstrap repair was made in the test-only stage.

### Windows tool-bootstrap fix awaiting re-review

The follow-up fix exports `GOBIN` through Make itself and uses shell-independent `go install` recipes, so the same pinned bootstrap works under Windows `cmd.exe` and POSIX shells. A lint finding in the new malformed-membership HTTP test was also corrected by using `slog.DiscardHandler`.

Focused evidence on 2026-09-09:

- Clean `make tools` bootstrap passed with golangci-lint `v2.13.2` and govulncheck `v1.7.0`; the first sandboxed attempt was blocked from `proxy.golang.org`, and the approved network-enabled retry passed.
- `make fmt-check` passed. The tool emitted the known restricted-sandbox working-directory warning without failing.
- The first sandboxed `make lint` reached the tool but could not load the restricted working directory. An approved unrestricted run found one test-only `slog` issue; after correction, `make lint` passed with zero issues.
- The first sandboxed `make vulncheck` reached the tool but could not read the host Go cache. The approved unrestricted retry passed with no called-code vulnerabilities.
- `go test ./internal/stores/transport/http` and `git diff --check -- Makefile internal/stores/transport/http/handler_test.go` passed after the final affected change.
- Per the user's instruction, the ignored pinned `.tools` cache remains available for subsequent verification.

The previous failed final-verification result remains historical evidence. These changes require review, and the final gate must be rerun against the resulting artifacts; the focused commands above are not a replacement final gate.

### Windows tool-bootstrap independent re-review

Independently re-reviewed on 2026-09-09 against base `76caad7783c8accd52fd031113c10c7d74373223` plus the current working tree. Both reviewers verified unchanged SHA-256 hashes: Makefile `96A086FF030A2A5830C26C6F89D218C65219E50F2136966B48676CD34B6605A1` and Stores HTTP test `CDCA5695133C991C3BCBC41AF761F147120F5DDA468BAF359253B02FDDBFA211`.

- Architect verdict for the bootstrap/test cleanup: **pass**. Make-exported `GOBIN`, versioned target paths, pinned plain `go install` recipes, native Windows expansion, POSIX expansion, CI compatibility, ignored tool artifacts, and unchanged membership-test assertions were inspected with no concrete defect.
- Security verdict for the bootstrap/test cleanup: **pass**. Tool pins and embedded binary metadata matched, generated executables remain ignored, clean CI rebuilds them, verification failures propagate, and the logger-only test change does not weaken non-enumerating or zero-dependency-call assertions.
- No new architecture, security, tenant-isolation, transaction, migration, API, or coverage finding was introduced by this fix.
- Both overall task verdicts remain **changes required** solely because database-authoritative expiry and atomic store suspension/session issuance remain explicitly deferred under the current completion policy.
- The reviewers did not rerun tools or tests and recommended a fresh full verification run; the subsequent user-approved consolidation decision is recorded below. Linux behavior was inspected through GNU Make expansion and CI configuration but was not freshly executed on Ubuntu.

### Consolidated MVP verification decision

On 2026-09-09, the user approved reusing the successful unchanged checks instead of repeating the full integration suite after the reviewed Makefile and logger-only test changes.

| Required check | Consolidated current evidence |
| --- | --- |
| `make fmt-check` | Passed after the portable bootstrap fix |
| `make lint` | Passed with zero issues after the final test-only logger change |
| `make test` | Passed before the tooling-only Makefile change; the only later Go change was exercised by the focused Stores HTTP package test, which passed |
| `make test-integration` | Passed before the tooling-only Makefile and logger changes; no runtime or integration behavior changed afterward |
| `make vulncheck` | Passed after the portable bootstrap fix with no called-code vulnerabilities |

This establishes the user-approved final verification evidence for the MVP artifact without claiming that all five commands were rerun consecutively on one invocation. It does not override the two explicitly deferred medium review findings, so task 004 is conditionally usable for continued MVP development but is not fully accepted under the current project workflow.

## Required completion report

- Behavior and design summary
- Files/modules changed
- Commands executed with exact results
- Review finding dispositions and re-review outcomes
- Remaining security, deployment, provider-UAT, and operational risks

## Delivery record

- Stage: MVP verification evidence consolidated; deferred review risks retained
- Active task: no; implementation work stopped by explicit user decision
- Execution mode: stage-only
- Last command: ec-review
- Completed stage: revision 5 implementation, reviews and first fixes, explicit user risk deferral, Windows tool-bootstrap repair/re-review, and user-approved consolidation of final verification evidence
- Pending gates: database-time and atomic store-suspension findings remain unresolved and prevent full workflow acceptance; no further task-004 work is currently requested
- Next suggested command: ec-fix tasks/004-identity-authorization.md only if the deferred risks are reopened
- Plan revision and approval: revision 4 was approved and implemented but is superseded; the user approved revision 5 and invoked ec-build on 2026-09-08
- Initial base: commit 76caad7783c8accd52fd031113c10c7d74373223 plus the existing uncommitted revision 4 and task 005 working tree
- Revision 5 decision: use one configured deployment admin token and existing onboarding path; create owner credentials without owner session; defer platform accounts/invitations for speed
- Revision 5 implementation: added deployment-token platform authentication, trusted-proxy client addressing, sessionless atomic provisioning, typed audit actors, explicit membership roles, narrow transaction writers, bound atomic Redis consumption, classified provider failures, independent peer/credential limits, safe callback redirects/cookies, corrected migration/OpenAPI/configuration, and aligned current-state documentation
- Focused evidence: `make test`, `make lint` (0 issues), `make fmt-check`, and `git diff --check` passed on 2026-09-09. A guarded clean test reset applied migrations 00001/00002; `make test-integration` passed; repeat migration was a no-op at version 2; focused populated-upgrade, onboarding, Stores, API, and Redis integration tests passed after the final affected changes. Initial direct lint/integration attempts failed because the restricted sandbox could not download/resolve the tool path and because a direct `go test` invocation lacked Make-exported test variables; reruns with the pinned workspace tool and guarded local test configuration passed.
- Architect review: revision 5 changes required on 2026-09-09 with four blocking medium findings: production-safe platform-token configuration, disabled-user refresh, stale social-exchange session creation, and malformed membership-create input
- Security review: revision 5 changes required on 2026-09-09 with one high production-token finding, one medium disabled-refresh finding, and low findings for malformed membership creation and oversized base64url inputs; consolidated severities and dispositions are recorded above
- Fix-stage evidence: focused unit tests passed for Identity application/PostgreSQL compilation, platform authentication/configuration, and Stores application/HTTP behavior. Migration status remained at version 2. Focused real PostgreSQL tests passed for active refresh concurrency, disabled-user concurrent refresh/revocation/reactivation, and stale social-session admission after user disablement or store suspension. `gofmt -l` on all affected Go files and `git diff --check` passed; `make -pn APP_ENV=production` showed an empty `PLATFORM_ADMIN_TOKEN`, confirming no Make default. The first sandboxed unit-test attempt could not use the host Go cache; rerunning with the workspace cache passed. The first disabled-refresh regression expected ordinary unauthenticated after reactivation but observed the safe replay classification; the corrected invariant test passed and confirmed no successor token row. Disposable dependencies were stopped and the generated workspace Go cache was removed after testing.
- Architect re-review: changes required on 2026-09-09; all prior architecture findings were confirmed resolved, but database-authoritative credential expiry remains a medium blocker.
- Security re-review: changes required on 2026-09-09; four prior findings and current-user admission were confirmed resolved, but atomic store suspension versus session issuance remains a medium blocker.
- User risk decision: on 2026-09-09 the user explicitly deferred both remaining medium findings for the initial MVP; neither finding is represented as fixed or reviewer-approved
- Bootstrap re-review: architect and security reviewers independently passed the Makefile and test-only fixes on 2026-09-09 with no new findings; their overall task verdicts remain changes required solely because of the two deferred medium risks
- Final evidence: user-approved consolidated verification passed on 2026-09-09 using the successful full unit/integration results plus post-fix formatting, lint, vulnerability, and focused-package results; the two deferred review findings still prevent full workflow acceptance
