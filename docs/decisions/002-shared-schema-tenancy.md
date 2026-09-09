# ADR 002: Shared-schema tenancy with an evolution path

Status: Accepted for MVP

## Context

The SaaS deployment serves independent stores with initially unknown and likely uneven sizes. Customer-managed deployments use the same application. Database-per-tenant operation would multiply migration, connection, backup, and support work before regulated or very large tenants require it.

## Decision

Use shared PostgreSQL tables with mandatory store_id on every tenant-owned row. Tenant primary/candidate keys and foreign keys include store_id; repositories, cache keys, jobs, and events require it explicitly. SaaS host resolution establishes the store before tenant authentication.

Keep data access behind tenant-aware module repositories so a future tenant-placement layer can route selected stores to another database without changing domain rules. Do not implement placement or sharding before measured need.

## Consequences

- Tenant scope is a security boundary enforced in host context, authentication, authorization, repositories, constraints, caches, jobs, events, and tests.
- Store-owned natural identifiers are unique with store_id, allowing independent businesses to reuse email, slug, SKU, order number, and provider references.
- Global tables are limited to explicit platform concerns such as store routing. The initial deployment-token platform principal has no database table.
- PostgreSQL row-level security may be added only as tested defense in depth; it does not replace application scope.
- A sustained noisy neighbor, contractual data isolation, or regional residency may justify tenant placement. Migration requires write fencing, reconciliation, tested cutover, and recovery rather than ad hoc connection selection.
- Shared-schema backups and operational access must protect all tenants because compromise has a wide blast radius.
