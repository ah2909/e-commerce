# Implementation Roadmap

Deliver vertical slices; each slice includes schema, domain/application behavior, API contract, authorization, observability, and tests.

1. Bootstrap: Go module, configuration, logging, health checks, CI, local dependencies.
2. Store context: store aggregate, authenticated tenant resolution, membership, isolation test harness.
3. Identity and authorization: sessions/tokens, store roles, deny-by-default permissions.
4. Catalog basics: draft product and variant creation, listing, publication rules.
5. Pricing: store price lists, deterministic price resolution, currency rules.
6. Inventory: stock ledger, atomic reservations, expiry/release, concurrency tests.
7. Customers: store-scoped profile and address management.
8. Cart: add/update/remove lines and calculate estimated totals.
9. Checkout/order: authoritative pricing, reservation, snapshots, state machine, outbox.
10. Payments: provider port, one adapter, idempotent attempts and signed webhooks.
11. Event workers: outbox publisher, Kafka topics, inbox/deduplication, email example.
12. Shipping and simple coupons.
13. Operational hardening: dashboards, alerts, backup/restore test, load and security testing.

## Extraction rule

Do not split a module into a microservice because it looks independent. Consider extraction only after measurable needs such as separate scaling, team ownership, release cadence, failure isolation, or regulatory boundaries justify network and data-consistency costs.
