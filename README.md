# VenueOS Backend

VenueOS is a multi-tenant Go backend for venue and event configuration, assigned
seating and general-admission inventory, live availability, holds, checkout,
payments, ticket delivery, entry scanning, refunds, promotions, waitlists,
reporting, and integrations.

The project starts from a production-oriented Go service foundation using Go
1.27.1, PostgreSQL, `net/http`, pgx, SQLC, Goose, OpenAPI, `log/slog`, Prometheus,
and OpenTelemetry. It will evolve as a modular monolith with separate API, worker,
and migration processes.

## Implementation Status

The runtime template and first platform contracts are operational. VenueOS now has
its stable API error envelope plus UUIDv7 identifiers, checked minor-unit money,
injectable UTC clocks, application error codes, and bounded validation details.
The initial organization, audit, outbox, inbox, idempotency, and durable job schema
is implemented with an append-only audit trail, bounded transaction retry, atomic
platform record repositories, and a lease-safe worker with bounded retry,
dead-letter recovery, tracing, and Prometheus queue metrics. The PostgreSQL outbox
relay now has bounded claim leases, owner-checked acknowledgements, retry/backoff,
and dead-letter transitions; concrete consumer/provider wiring remains domain-specific.
Provider-neutral inbound-message processing now has hash-checked deduplication,
expiring leases, retry, timeout handling, and dead-letter transitions for future
payment/provider adapters.
Provider-neutral ticket credentials now use a bounded `vos-ticket.v1` QR format
with Ed25519 signatures, active/verify-only/revoked key states, validity windows,
and no customer or price data. Durable entitlement/ticket, device, scan-attempt,
and admission tables now exist; current ticket state remains server-authoritative.
The pricing domain also has a deterministic minor-unit quote calculator with
explicit adjustment inputs and a snapshot digest; jurisdictional tax/fee rules
remain caller-supplied pending the launch policy decision.
Provider-neutral order and payment transition tables now reject illegal
rollback/terminal moves while treating same-state webhook delivery as an
idempotent replay. Durable tenant-scoped orders, immutable order lines, and
payment-attempt constraints now persist checkout snapshots atomically with cart
checkout, audit, and outbox records; provider execution and confirmation remain
next.
Cart and order repositories preserve the exact quote bytes alongside constrained
JSONB, preventing database key-order normalization from breaking digest checks.
The cart slice now persists one tenant/session/hold-scoped active cart with an
owner-token predicate, immutable quote JSON plus SHA-256 digest, optimistic
versioning, and atomic audit/outbox mutation records. Customer/contact fields,
promotion policy, and checkout remain intentionally separate.
The payment domain now exposes a provider-neutral adapter port, strict intent and
refund request validation, sanitized webhook event metadata, and bounded failure
classification ready for the durable inbox. Durable payment-attempt rows now
store only bounded provider references, statuses, failure facts, and sanitized
metadata; Stripe account/capture wiring remains decision-gated.
The online-entry boundary also has a pure server-side decision function for
ticket/session/version/window/status checks plus a serializable online scan and
admission transaction. It locks the active device and ticket, appends immutable
scan evidence, and admits a ticket at most once. Tenant-scoped device enrollment
now returns a one-time credential and supports audited versioned lifecycle changes;
device session/gate assignment is also audited and versioned. Credential-authenticated
device authorization, durable public ticket-key resolution, and `POST
/v1/device/scans` are wired into the API process. Idempotent entitlement-keyed
ticket issuance now persists the ticket and signs the credential, and the strict
`ticket.issue` payload plus tenant-bound worker adapter are ready for composition.
Verified provider-neutral webhook receipt also checks hashes and queues durable
inbox messages through a reference-backed payload store. Offline manifests,
worker/signer composition, private-key registration/rotation, and delivery remain
next.
The bounded `internal/realtime` hub provides in-process post-commit fan-out with
per-subscriber queues and an explicit resync signal for slow consumers; durable
PostgreSQL replay remains the recovery authority while Redis/WebSocket transport
is still pending.
Refund invariants now enforce currency/positive-amount checks and the captured
value ceiling, with idempotent refund lifecycle transitions. Durable refund
requests now lock the order and captured payment attempt, sum pending/succeeded
refunds, and write an audited/outboxed request. Bounded `refund.execute`
orchestration marks processing before calling an injected provider adapter outside
the transaction, uses the stable idempotency key, and finalizes verified outcomes;
policy, concrete provider wiring, and reconciliation remain explicit
application-layer work.
The authenticated staff refund-request endpoint now persists this durable request
with tenant/permission checks and safe same-key replay semantics.
The staff order detail endpoint also exposes tenant-scoped immutable totals and
line facts without returning owner-token material.
Provider-neutral typed
principal and verified organization authorization contexts are available for the
first protected domain slice, and the venue/space catalog now has tenant-scoped
migrations, repositories, immutable assigned-seat-map and event revisions,
tenant-linked session scheduling, session price-tier foundations, routes, and
sales-channel foundations with audit/outbox behavior. The
session allocation rows enforce tenant linkage, unique scopes, and serialized hard
capacity limits, and publication validation reports expose actionable catalog
blockers, with publish now failing closed when that report is invalid. PostgreSQL
now materializes session GA pools and assigned seats, supports atomic GA and
reserved-seat holds/releases, database-time expiry, version-checked renewal and
hold modification, repeatable-read availability snapshot/replay readers, and
recurring expiry scheduling with atomic audit/outbox/realtime records. Provider-neutral
OIDC verifier contracts and tenant-scoped API-key issuance/authentication/revocation
and immediate rotation are also implemented; production OIDC wiring remains
provider-dependent. Protected organization and invitation routes now enforce one
bearer credential and attach the verified API-key tenant authorization to request
context; OIDC session loading remains the next adapter boundary.
Authenticated tenant-scoped staff snapshot/replay routes are wired; API-key
metadata listing, show-once create, versioned rotate, and versioned revocation
are exposed without returning secret material after issuance; the permission-
gated tenant audit explorer provides filter-bound cursor pagination and
response-side credential redaction; the staff entry dashboard
provides bounded result/gate/minute/ticket-type totals with explicit freshness;
public storefront tenant resolution remains intentionally open.
The
current example
`user` API proves the HTTP, OpenAPI, SQLC, database, test, and generation pipeline;
it is transitional and will be replaced by the organization/access vertical slice.

Start with:

- [`AGENTS.md`](AGENTS.md) for mandatory engineering rules;
- [`docs/venueos/README.md`](docs/venueos/README.md) for the complete design index;
- [`docs/venueos/18-implementation-workboard.md`](docs/venueos/18-implementation-workboard.md)
  for the ordered implementation backlog and current status.

## Quick Start

Requirements: Go 1.27.1+, Docker with Compose, and Make.

```sh
cp .env.example .env
set -a; . ./.env; set +a
make db-up
make migrate-up
make run-api
```

The transitional template endpoint can be exercised with:

```sh
curl -i http://localhost:8080/v1/users \
  -H 'Content-Type: application/json' \
  -d '{"email":"ada@example.com","name":"Ada Lovelace"}'
```

Useful operational endpoints:

- `GET /healthz`: process liveness; never checks dependencies.
- `GET /readyz`: readiness; verifies PostgreSQL connectivity.
- `GET http://127.0.0.1:9090/metrics`: management-listener metrics.
- `GET http://127.0.0.1:9090/debug/pprof/`: profiling when explicitly enabled.

## Development Commands

```sh
make generate
make test
make test-race
make test-integration
make lint
make vulncheck
make build
make verify
```

Generated OpenAPI and SQLC files are committed. After generation, inspect all
changes and never hand-edit generated sources.

Build individual images from the shared Dockerfile:

```sh
docker build --build-arg COMMAND=api -t venueos:api .
docker build --build-arg COMMAND=worker -t venueos:worker .
docker build --build-arg COMMAND=migrate -t venueos:migrate .
```

## Current Layout

```text
api/                         OpenAPI source and generator configuration
cmd/api/                     HTTP API composition root
cmd/worker/                  Background worker composition root
cmd/migrate/                 Explicit migration command
internal/config/             Typed process configuration
internal/access/             Tenant authorization, API keys, and OIDC verifier port
internal/database/           PostgreSQL pool construction
internal/httpapi/            HTTP adapter and generated contract
internal/logging/            Structured logging policy
internal/observability/      Metrics, tracing, diagnostics
internal/platform/jobqueue/ Durable job contract shared by workers and adapters
internal/user/               Transitional template vertical slice
migrations/                  Versioned Goose migrations
pkg/requestid/               Reusable request-ID primitives
tools/                       Isolated pinned developer tools
docs/venueos/                VenueOS implementation contract
```

The target domain layout is documented in
[`03-architecture-and-repository.md`](docs/venueos/03-architecture-and-repository.md).

## Architecture Rules

- PostgreSQL is authoritative for durable business state.
- Redis is ephemeral support for caching, rate limits, socket tickets, and fan-out.
- Domain/application packages do not import HTTP, SQL drivers, Redis, or providers.
- Every tenant resource and query is organization-scoped.
- Financial values use integer minor units and explicit currency.
- State mutations atomically include audit/outbox/idempotency records where relevant.
- Remote provider calls never occur while database transactions or row locks remain open.
- WebSockets accelerate delivery; snapshots and polling provide recovery.
- Migrations run explicitly and remain backward compatible during rolling releases.

See `AGENTS.md` for the complete non-negotiable invariants and definition of done.

## Configuration

Safe local defaults are documented in `.env.example`. Production configuration
must inject secrets externally and satisfy the stricter validation already enforced
by each command. The local PostgreSQL database and service identity are named
`venueos`.

## License

No license has been selected. Choose and add the correct project license before
publishing the repository.
