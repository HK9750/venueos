# Architecture and Repository Design

## Architecture Style

VenueOS begins as a modular monolith with three deployable processes built from one
Go module:

- `api`: synchronous public, staff, device, health, and realtime interfaces;
- `worker`: outbox publication, durable jobs, expiration, delivery, webhooks,
  reconciliation, exports, and maintenance;
- `migrate`: explicit database schema migration command used during deployment.

This keeps transactions and operational ownership clear while avoiding premature
distributed-system overhead. Module boundaries are designed so a domain can later
be extracted only when measured scaling, isolation, ownership, or release cadence
requires it.

## Runtime Topology

```text
Browser / Staff UI / Scanner / Integration
                  |
          HTTPS + WebSocket
                  |
             API replicas
           /       |       \
   PostgreSQL    Redis     Object storage
       |            |             |
       +------ Worker replicas ---+
                   |
       payment / email / SMS / webhooks
```

PostgreSQL owns durable truth. Redis supports TTL coordination, bounded caches,
rate limits, one-time socket tickets, and cross-replica pub/sub. Object storage
holds immutable ticket/render/export artifacts. Workers interact with providers
outside database transactions and persist every result.

## Dependency Direction

```text
transport -> application -> domain
                 ^            ^
                 |            |
        infrastructure adapters
```

- **Domain** contains entities, value objects, state transitions, policies, and
  domain errors. It imports no HTTP, SQL, Redis, provider, or generated transport
  package.
- **Application** contains use cases, authorization orchestration, transaction
  boundaries, ports, command/query DTOs, and event construction.
- **Infrastructure** implements repositories, transactions, locks, providers,
  storage, clocks, ID generation, outbox, and cache/fan-out ports.
- **Transport** parses/authenticates/validates requests, invokes use cases, maps
  results to versioned contracts, and contains no business decision.

Domains may depend on small shared value packages and application ports, but one
domain must not import another domain's SQL repository or mutate its tables. Cross-
domain effects use an application-level coordinator or a durable event/job.

## Target Repository Layout

```text
.
├── AGENTS.md
├── api/
│   └── openapi.yaml                 # canonical external HTTP contract
├── cmd/
│   ├── api/main.go
│   ├── worker/main.go
│   └── migrate/main.go
├── db/
│   ├── migrations/                  # ordered forward/backward schema changes
│   └── queries/                     # sqlc input grouped by domain
├── deployments/
│   ├── docker/
│   ├── compose/
│   └── kubernetes/                  # added when deployment platform is fixed
├── docs/
│   └── venueos/
│       ├── adr/
│       └── runbooks/
├── internal/
│   ├── platform/
│   │   ├── authn/ authz/ audit/ config/ database/ errors/
│   │   ├── httpx/ idempotency/ jobs/ logging/ money/
│   │   ├── observability/ outbox/ redisx/ storage/ timeutil/
│   │   └── validation/
│   ├── identity/
│   ├── catalog/
│   ├── booking/
│   ├── commerce/
│   ├── ticketing/
│   ├── entry/
│   ├── growth/
│   ├── reporting/
│   └── integrations/
├── pkg/                              # only genuinely reusable public packages
├── test/
│   ├── contract/ integration/ e2e/ load/ security/ fixtures/
├── tools/                            # pinned development tools
└── web/                              # email/template assets if owned here
```

Each domain follows this internal shape when needed:

```text
internal/<domain>/
├── domain/          # entities, policies, errors, state transitions
├── application/     # commands, queries, ports, authorization
├── infrastructure/  # PostgreSQL/Redis/provider adapters
└── transport/http/  # generated interface implementation and mapping
```

Do not create layers with no behavior. A small domain may start with fewer files,
but dependencies must still point inward.

## Domain Ownership

| Module | Owns | Does not own |
|---|---|---|
| identity | organizations, users, memberships, invitations, API/device credentials, provider connections metadata | business catalog or orders |
| catalog | venues, spaces, seat-map versions, events, sessions, tiers, channels, publication | live holds or sold counters |
| booking | session inventory, holds, hold items, availability versions | final commercial records |
| commerce | carts, orders, line price snapshots, payment attempts/events, refunds, reconciliation | seat-map configuration or QR signing |
| ticketing | entitlements, ticket versions, render artifacts, delivery requests | payment capture or scan decisions |
| entry | gates, devices, manifests, scan attempts, admissions | ticket issuance or catalog publishing |
| growth | promotions, redemptions, waitlists, offers | core base price or order capture |
| reporting | read models, export requests/artifacts | mutation authority |
| integrations | outbound webhook endpoints/deliveries and provider adapter coordination | domain state machines |
| platform | cross-cutting primitives and implementation mechanisms | domain-specific policies |

## Command and Query Flow

For a write request:

1. middleware assigns request/trace IDs, authenticates, limits body/rate, and
   resolves organization membership;
2. transport validates syntax and builds an application command;
3. use case checks permission and loads resource under organization scope;
4. idempotency claim is acquired when required;
5. transaction locks/version-checks affected rows in stable order;
6. domain transition validates invariants;
7. repositories persist mutation, audit, outbox, and idempotency result;
8. commit occurs before any external provider call;
9. response is returned; workers process durable side effects.

For a read request, repositories use tenant-filtered queries, deterministic order,
bounded limits, explicit projections, and replica/caching only when staleness is
allowed by the endpoint contract.

## Configuration

- Load typed configuration once at startup from environment/secrets provider.
- Validate required fields and mutually dependent settings before listeners start.
- Separate API, worker, and migrate config so each process receives least privilege.
- Configuration includes environment, public/admin addresses, database pool and
  timeouts, Redis, provider endpoints, object storage, OIDC issuers/audiences,
  signing keys, CORS/hosts, rate limits, worker concurrency, and telemetry.
- Secrets are references or injected values, never committed defaults or printed
  in startup logs.
- Feature flags default safely, are tenant/environment scoped when necessary, and
  have owner, expiry, rollout metric, and removal task.

## Failure Model

- Request cancellation propagates through repositories and provider clients.
- Database serialization/deadlock errors are retried only around a small,
  side-effect-free transaction closure with bounded jitter.
- External timeouts become retryable jobs when possible; callers receive stable
  pending/failure states rather than an ambiguous success.
- Process crashes are safe because work is committed before acknowledgement and
  jobs are leased with retry visibility.
- Redis failure degrades caches/live updates/rate-limit precision but does not
  change durable truth; snapshot/polling remains available.
- Object storage/provider failure delays artifacts or messages and is visible in
  job state; it does not roll back a confirmed order.

## Evolution Rules

Extract a service only after documenting ownership, API/event contract, consistency
change, backfill, failure modes, observability, rollback, and operational owner.
Prefer partitioning, indexes, read models, pooling, batching, and worker tuning
before adding a network boundary. Use an ADR for every material architecture change.

