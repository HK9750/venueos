---
name: venueos-backend
description: Plan, implement, review, test, or debug any VenueOS backend feature in this repository, including tenancy, venues, sessions, inventory, holds, checkout, payments, orders, tickets, check-in, promotions, waitlists, reporting, webhooks, realtime delivery, workers, security, or operations.
---

# VenueOS Backend

Use this skill for every VenueOS-specific change. It exists to keep work aligned
with the product specification and the repository's consistency rules without
loading every planning document into context.

## Start Here

1. Read `../../../AGENTS.md`.
2. Read `../../../docs/venueos/README.md` and follow its task-to-document routing.
3. Read only the domain and cross-cutting documents relevant to the task.
4. Check `../../../docs/venueos/15-decisions-and-open-questions.md`; do not reopen
   an accepted decision without new evidence.

## Classify the Work

- Product behavior: consult the feature catalog and relevant domain specification.
- HTTP/API: also consult API contracts and security.
- Inventory/payment/check-in: also consult consistency and state-machine rules.
- Live availability: also consult realtime, polling, and event contracts.
- Asynchronous processing: also consult workers and integrations.
- Schema/query: also consult the data model and migration policy.
- Deployment/incident work: also consult operations and runbooks.

## Execute a Vertical Slice

For a change that mutates business state, account for all of these explicitly:

1. actor, tenant, permissions, and resource ownership;
2. request validation and stable error response;
3. idempotency key and replay behavior;
4. database transaction and concurrency control;
5. audit record, outbox event, and downstream jobs;
6. response contract, polling snapshot, and realtime update if applicable;
7. metrics, logs, tracing, alerts, and recovery procedure;
8. unit, integration, HTTP, race/concurrency, and failure-path tests;
9. migration compatibility and documentation updates.

Keep PostgreSQL authoritative. Do not put external calls inside database
transactions. Do not trust request-supplied tenant IDs. Do not make WebSockets the
only way to observe state. Do not hand-edit generated sources.

## Finish

Run the checks required by `AGENTS.md`, update the relevant feature status and
acceptance criteria, and summarize any remaining assumption or operational risk.

