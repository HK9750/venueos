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

The runtime template is operational, while VenueOS business domains are planned but
not yet implemented. The current example `user` API proves the HTTP, OpenAPI, SQLC,
database, test, and generation pipeline; it is transitional and will be replaced by
the organization/access vertical slice.

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
internal/database/           PostgreSQL pool construction
internal/httpapi/            HTTP adapter and generated contract
internal/logging/            Structured logging policy
internal/observability/      Metrics, tracing, diagnostics
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

