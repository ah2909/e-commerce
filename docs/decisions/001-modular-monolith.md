# ADR 001: Modular monolith first

Status: Accepted

## Context

The product model and workload are still evolving. Checkout needs strong consistency across order, pricing, inventory, payment initialization, audit, and outbox records. Premature network boundaries would add partial failure and operating cost before independent scale or ownership needs are known.

## Decision

Deploy one repository as a stateless API process and one or more stateless worker process types. Organize business capabilities as explicit modules with exclusive table and domain-rule ownership.

Cross-module synchronous interactions use consumer-owned application interfaces. Asynchronous side effects use versioned events through the transactional outbox. A small number of documented workflows, such as checkout and tenant provisioning, may coordinate one PostgreSQL transaction without permitting arbitrary cross-module table access.

## Consequences

- One database may contain tables owned by several modules, but direct cross-module queries and mutations are prohibited.
- API and worker instances can scale horizontally without splitting code ownership.
- Module extraction remains possible but requires an ADR covering ownership, API/event contracts, data migration, consistency, failure handling, observability, and rollback.
- Extraction is justified only by measured scaling, team ownership, release cadence, failure-isolation, or regulatory needs.
- Shared libraries remain limited to stable primitives and cannot become an unowned business layer.
