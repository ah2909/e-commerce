# ADR 002: Shared tables with store scoping

Status: Accepted for MVP

## Decision

Use shared PostgreSQL tables and mandatory `store_id` fields for tenant-owned data.

## Rationale

This is operationally simple and supports efficient framework-wide changes while the number and size of stores are initially unknown.

## Consequences

- Tenant scope is a security boundary enforced in context, repositories, constraints, and tests.
- Large or regulated tenants may require a future placement/isolation strategy.
- Unscoped tenant repositories are prohibited.
