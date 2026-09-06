# Testing Strategy

## Test layers

- Domain unit tests: invariants, value objects, calculations, and state transitions.
- Application tests: use-case orchestration with controlled ports.
- Repository integration tests: real PostgreSQL behavior, constraints, transactions, and concurrency.
- API tests: authentication, authorization, validation, contract, and error mapping.
- Event contract tests: envelopes, schema compatibility, and consumer idempotency.
- Focused end-to-end tests: critical customer and admin journeys.

## Mandatory safety tests

- Store A cannot read, update, or delete Store B resources.
- Store-scoped slug/SKU/order-number values may overlap across different stores.
- Concurrent buyers cannot oversell the last unit.
- Repeating checkout with one idempotency key does not create duplicate orders.
- Repeating a payment webhook does not create duplicate effects.
- Product, price, and address changes do not alter an existing order.
- An outbox retry and consumer retry are safe.
- Invalid order transitions are rejected under concurrency.

## Test design

- Prefer table-driven tests for business-rule variations.
- Verify behavior and invariants, not private implementation details.
- Use fixed clocks and deterministic identifier generators where time/IDs matter.
- Keep external providers behind ports and test adapters against provider fixtures/contracts.
- Integration tests must create isolated data and clean up reliably.
- Do not mock PostgreSQL semantics for transaction or concurrency behavior.

## Quality gates

The default verification target should include formatting, static analysis, race-enabled unit tests where practical, integration tests, and vulnerability scanning. CI may split these jobs while `make verify` remains the developer entry point.
