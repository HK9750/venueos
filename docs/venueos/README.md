# VenueOS Backend Documentation

This directory is the implementation contract for the VenueOS backend. It turns
the product design into domain behavior, architecture boundaries, data ownership,
API semantics, realtime recovery, background processing, security controls,
operational procedures, tests, and a staged delivery plan.

The source product brief is
`/home/hasnain-khan/Downloads/VenueOS_SRS_and_Product_Design.docx`. The documents
here refine that brief into engineering decisions. The earlier consolidated plan
at [`../venueos-backend-implementation-plan.md`](../venueos-backend-implementation-plan.md)
is retained as a high-level reference; this directory is the detailed source of
truth for new implementation.

## How to Use This Set

- Product or scope question: read 01 and 02.
- Repository or architecture change: read 03 and 15.
- Identity, tenant, role, or staff feature: read 04, 08, and 11.
- Venue, seat map, event, or session feature: read 05, 07, and 08.
- Availability, hold, cart, order, payment, or refund work: read 06, 07, 08, 09,
  and 10.
- Ticket, delivery, scan, promotion, waitlist, analytics, or export work: read 06,
  08, and 10.
- Realtime or polling work: read 09 plus the owning domain document.
- Worker, webhook, email/SMS, storage, or provider integration: read 10 and 12.
- Security/privacy work: read 11 and the relevant domain document.
- Deployment, incident, SLO, or capacity work: read 12.
- Test/release review: read 13 and 14.

## Document Map

| Document | Purpose |
|---|---|
| [01-product-and-personas.md](01-product-and-personas.md) | Product boundaries, actors, workflows, terminology, and success measures |
| [02-feature-catalog.md](02-feature-catalog.md) | Complete feature inventory, priorities, dependencies, and acceptance summaries |
| [03-architecture-and-repository.md](03-architecture-and-repository.md) | Target modular-monolith architecture and concrete repository layout |
| [04-identity-tenancy-and-access.md](04-identity-tenancy-and-access.md) | Authentication, organizations, memberships, roles, API keys, and tenant isolation |
| [05-venues-events-and-catalog.md](05-venues-events-and-catalog.md) | Venues, spaces, seat maps, price tiers, events, sessions, publication, and sales channels |
| [06-booking-commerce-and-entry.md](06-booking-commerce-and-entry.md) | Inventory, holds, carts, checkout, orders, payments, refunds, tickets, check-in, promos, and waitlists |
| [07-data-model-and-migrations.md](07-data-model-and-migrations.md) | Tables, ownership, constraints, indexing, retention, and migration rules |
| [08-api-and-error-contracts.md](08-api-and-error-contracts.md) | HTTP conventions, resource routes, schemas, pagination, idempotency, and errors |
| [09-realtime-polling-and-concurrency.md](09-realtime-polling-and-concurrency.md) | Snapshot/poll/WebSocket design, replay, backpressure, and allocation algorithms |
| [10-workers-events-and-integrations.md](10-workers-events-and-integrations.md) | Outbox/inbox, job processing, schedules, provider adapters, retries, and dead letters |
| [11-security-privacy-and-compliance.md](11-security-privacy-and-compliance.md) | Threat model, security controls, privacy lifecycle, PCI boundary, and audit |
| [12-observability-operations-and-slos.md](12-observability-operations-and-slos.md) | Telemetry, dashboards, alerts, capacity, deployment, backup, and incident runbooks |
| [13-testing-and-quality.md](13-testing-and-quality.md) | Test layers, fixtures, concurrency tests, contract tests, quality gates, and release evidence |
| [14-delivery-roadmap.md](14-delivery-roadmap.md) | Milestones, vertical slices, dependencies, exit criteria, and launch process |
| [15-decisions-and-open-questions.md](15-decisions-and-open-questions.md) | Accepted defaults, ADR index, unresolved product questions, and decision deadlines |
| [16-engineering-conventions.md](16-engineering-conventions.md) | Go/package conventions, transactions, errors, local workflow, and change templates |
| [17-requirements-traceability.md](17-requirements-traceability.md) | Requirement-to-design, milestone, verification, and evidence crosswalk |
| [18-implementation-workboard.md](18-implementation-workboard.md) | Ordered implementation epics, first deliverables, status conventions, and dependency gates |

## Requirement Language

`MUST` is required for correctness, security, or launch acceptance. `SHOULD` is the
default unless an ADR records a justified exception. `MAY` is optional. “Phase 1”
means the first production-capable release, not a disposable prototype.

## Documentation Ownership

Every feature PR must update the affected document when behavior, schemas, routes,
events, operational controls, or delivery status changes. Accepted architecture
changes receive an ADR under `docs/venueos/adr/`. API truth is generated from the
OpenAPI source; schema truth is the migration history; these planning documents
explain intent and invariants.
