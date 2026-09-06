# ADR 001: Modular monolith first

Status: Accepted

## Decision

Deploy the application initially as an API process and worker process from one repository, with explicit domain modules and ownership boundaries.

## Rationale

This minimizes operational and consistency complexity while the product model is evolving. Module contracts preserve a future extraction path.

## Consequences

- One database may contain schemas/tables for multiple modules, but table ownership remains exclusive.
- Cross-module transactions are allowed only for documented critical workflows such as checkout.
- Engineers must prevent convenience queries from eroding boundaries.
