# VenueOS Repository Instructions

This repository is the backend foundation for VenueOS, a multi-tenant platform for
venues, sessions, reserved seating, general-admission inventory, checkout,
payments, ticketing, entry scanning, refunds, promotions, waitlists, reporting,
and third-party integrations.

These instructions apply to the entire repository. A more deeply nested
`AGENTS.md` may add stricter instructions for its subtree but must not weaken the
security, tenancy, consistency, or audit requirements below.

## Read Before Changing Code

1. Read [`docs/venueos/README.md`](docs/venueos/README.md).
2. Read the documents listed there for the domain being changed.
3. Read [`docs/venueos/15-decisions-and-open-questions.md`](docs/venueos/15-decisions-and-open-questions.md)
   before making an architectural choice.
4. Use the repository skill at [`.agents/skills/venueos-backend/SKILL.md`](.agents/skills/venueos-backend/SKILL.md)
   for VenueOS planning, implementation, review, and debugging work.

When documentation and code disagree, do not silently choose one. Verify current
tests and migrations, state the conflict, then update both the implementation and
the applicable documentation as part of the same change.

## Architecture Boundaries

- Keep a modular monolith. Domains communicate through explicit application
  interfaces and domain events, not by reaching into each other's repositories.
- Keep deployable entry points separate: API, worker, and migration commands.
- PostgreSQL is the authority for durable business state. Redis is only for
  expiring coordination, short-lived caches, rate limits, WebSocket tickets, and
  fan-out. A Redis loss must not lose a booking, payment, ticket, or audit record.
- External provider calls must go through adapters. Domain and application layers
  must not import Stripe, S3, email, SMS, OIDC, or Redis clients directly.
- Prefer the standard library and existing dependencies. Add a dependency only
  when its operational and maintenance cost is justified in the change.
- Start with PostgreSQL-backed outbox jobs. Add a broker only after measurements
  show that the database design cannot meet throughput or latency requirements.

## Non-Negotiable Business Invariants

- Every tenant-owned row has an `organization_id`; every tenant operation derives
  organization scope from authenticated membership, never only from request data.
- Cross-tenant access must fail even when a caller guesses a valid resource ID.
- Money is stored as integer minor units with an ISO-4217 currency code. Never use
  floating point for prices, fees, tax, discounts, refunds, or settlements.
- Store timestamps in UTC and expose RFC 3339 timestamps. Venue time zones are
  IANA identifiers used only for display and local scheduling rules.
- A successful state-changing transaction records the business mutation, audit
  entry, outbox event, and idempotency result atomically when those concepts apply.
- Never call a remote provider while holding a database transaction or row lock.
- Reserved-seat allocation locks requested seat rows in stable ID order. General
  admission allocation uses an atomic conditional capacity update.
- A hold has an immutable inventory snapshot, an explicit expiry, a state, and a
  version. Confirmation is allowed only once and only from a valid, unexpired hold.
- Payment webhooks are authenticated, inbox-deduplicated, and processed as the
  source of eventual payment truth. Client redirects are not payment proof.
- Refund and check-in commands are idempotent. Refund totals cannot exceed captured
  value; a ticket cannot be admitted twice unless an explicit audited override is
  authorized.
- WebSocket messages are a performance feature, not a consistency authority.
  Every live view has a snapshot endpoint and a polling/replay recovery path.
- Every list endpoint uses bounded cursor pagination with deterministic ordering.
- Sensitive values and raw payment data must never appear in logs, traces, audit
  metadata, API errors, or repository fixtures.

## Implementation Workflow

Implement one vertical slice at a time:

1. Confirm the feature and acceptance criteria in the relevant domain document.
2. Write or amend an ADR when the work changes an architectural decision.
3. Update the OpenAPI contract and error model first for externally visible APIs.
4. Add backward-compatible migrations and SQL queries.
5. Regenerate generated sources; never hand-edit generated files.
6. Implement domain rules, application use cases, and infrastructure adapters.
7. Add unit, repository, HTTP contract, concurrency, and worker tests as applicable.
8. Add metrics, structured logs, traces, audit data, and operator runbook notes.
9. Update the feature matrix, documentation, and delivery status in the same PR.

All outbound calls require context propagation, explicit timeouts, and classified
retry behavior. Retries require exponential backoff with jitter and a finite
attempt count. Only retry operations known to be idempotent.

## Required Verification

Run the narrowest relevant checks during development and the full available suite
before handoff:

```bash
go test ./...
go test -race ./...
go vet ./...
make lint
make vulncheck
make build
make generate
make test-integration
```

If a target is unavailable in the current environment, report that fact and run
the closest non-destructive equivalent. After `make generate`, inspect the working
tree and reject unexplained generated changes. Integration tests requiring
PostgreSQL, Redis, or object storage must use isolated databases/buckets and
deterministic fixtures.

## Definition of Done

A feature is not done until its contract, authorization, validation, idempotency,
concurrency behavior, audit trail, events/jobs, observability, failure recovery,
tests, migration/rollback story, and operator documentation are complete. A TODO
is not a substitute for handling an expected failure mode.
