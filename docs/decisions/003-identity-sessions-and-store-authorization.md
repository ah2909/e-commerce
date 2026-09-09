# ADR 003: Store-owned identities and host-selected tenants

Status: Accepted for MVP; provisioning correction adopted 2026-09-08

## Context

A shared SaaS deployment must determine tenant scope before accepting user, guest, or workforce credentials. Stores are independent businesses, and the same person may create unrelated accounts in different stores. Tenant owners must not gain deployment-wide tenant-creation authority.

## Decision

Use opaque server-side access tokens with rotating refresh tokens for store-owned registered users, and separate store-bound opaque sessions for guests. Select store scope from exactly one validated subdomain beneath a deployment-configured base domain before tenant authentication. Do not trust forwarded-host headers, request bodies, or path identifiers as tenant authority.

An account belongs to exactly one store. Email and Google or Facebook identities may be reused independently at another store, but every credential, identity link, session, and token lookup includes store_id. Provider access tokens are transient. Social callbacks use a fixed configured authentication host, bind state and exchange codes to the originating store and browser, and redirect through the server-owned store/subdomain mapping.

Redis consumes social state only when provider and browser bindings match, and consumes exchange codes only when store binding matches. Provider authorization rejection is permanent; network, timeout, and 5xx failures are transient, but either outcome requires a fresh social start after a matching callback consumes its one-time code.

Use a separate platform-administration principal for tenant provisioning. The first release authenticates it with one 32-byte deployment secret encoded as unpadded base64url. Only that principal may create a store. Provisioning creates the store, first store-owned user/password, owner membership, and platform-actor audit record atomically, but creates no owner session. The owner then signs in on the store subdomain. It is not a public self-service route, and a tenant owner/admin does not satisfy platform authorization.

A tenant user may be a shopper without workforce access. Fixed owner, admin, and staff memberships are store-local; current membership is resolved on each tenant request and explicit permissions deny by default. Guests cannot satisfy workforce permissions.

## Consequences

- PostgreSQL is consulted for access-token and tenant-context validation.
- Redis availability is required for authentication rate limits and transient social-login state, but Redis never becomes identity authority.
- Authentication throttling applies an independent resolved-client bucket before store-scoped credential buckets. Forwarded addresses are trusted only from configured proxy CIDRs and never select tenant scope.
- A person creates an independent account in each store and no session crosses a store host.
- Guest sessions support shopping; guest order access uses an additional order-specific opaque grant.
- Google and Facebook configuration is optional. Each enabled provider requires an exact HTTPS callback on the configured authentication host.
- Customer-managed deployments configure their trusted host boundary but retain store scoping.
- The static deployment token is a speed-first compromise: it identifies the deployment administrator rather than an individual human and rotates through secret replacement plus process restart. Individual platform accounts, invitations, and overlapping rotation are deferred.
