# Engineering Conventions

## Go Version and Package Design

Use the Go version pinned by `go.mod` and CI. Keep packages cohesive and small
enough to name by capability, not generic containers such as `common`, `helpers`,
or `utils`. Export only what another package needs. Package names are short singular
nouns; files are grouped by behavior (`create_hold.go`, `postgres_repository.go`),
not by artificial type buckets.

- Constructors return concrete implementations behind interfaces owned by the
  consuming application package.
- Define an interface at the point of use, only when there are multiple adapters or
  a meaningful test boundary. Avoid interfaces mirroring every struct.
- Pass `context.Context` first to I/O and use-case methods; never store it in a
  struct or pass nil.
- Avoid global mutable state. Build dependencies explicitly in each command's
  composition root.
- Prefer value objects for organization ID, money, status, and versions when they
  remove ambiguity, but do not wrap every primitive without behavioral benefit.
- Keep domain/application code free of HTTP, SQL driver, Redis, and provider types.
- Avoid goroutines started by request/domain code unless lifecycle, bound, error,
  cancellation, and shutdown ownership are explicit.

## Composition Roots

`cmd/api`, `cmd/worker`, and `cmd/migrate` contain minimal startup logic. A bootstrap
package loads/validates configuration, initializes telemetry, opens bounded clients,
constructs adapters/use cases, starts listeners/worker groups, and closes in reverse
order. Startup failure returns a clear redacted error and non-zero exit.

Workers register handlers explicitly by job type/schema version. HTTP routes are
generated/wired explicitly. No service locator or hidden package `init` registration.

## Errors

Domain errors are typed/sentinel codes carrying safe structured context. Wrap with
`fmt.Errorf("operation: %w", err)` to preserve cause. Infrastructure maps driver/
provider errors to classified application errors at the boundary. Transport maps
known codes to the API envelope in one central place.

- Do not compare error strings.
- Include enough operation context internally, but never wrap secrets/PII.
- `context.Canceled` and deadline errors retain their identity.
- Expected domain conflicts log at appropriate low level, not as server failures.
- Unexpected errors are logged once at the request/job boundary with trace context.
- Panics are recovered only at process/request/job boundaries, recorded, and treated
  as defects; domain logic does not use panic for validation.

## Transactions and Repositories

Application use cases own transaction boundaries through a small transaction port.
Repositories participate through transaction-scoped handles without beginning or
committing behind the caller's back.

Transaction checklist:

1. check context/deadline before beginning;
2. load resources with tenant predicate;
3. lock only necessary rows in documented stable order;
4. validate current version/state under lock;
5. apply domain transition;
6. persist mutation, audit, outbox, job, and idempotency result;
7. commit quickly;
8. invoke remote providers only afterward.

Repository methods use explicit projections/columns and generated SQL where
configured. No `SELECT *` in production queries. Lists require deterministic order
and hard maximum. Database errors are classified using constraint names/SQLSTATE,
not message substrings.

## Time, IDs, Money, and Randomness

- Inject a clock into domain/application code; infrastructure supplies real UTC.
- Use database time when competing transactions must agree on expiry.
- Generate opaque sortable UUIDs consistently; never encode tenant/business secrets
  in IDs.
- Use cryptographically secure randomness for credentials/tokens; tests inject a
  deterministic generator.
- Money operations require matching currency, checked overflow, explicit rounding,
  and component reconciliation. Display formatting belongs outside domain math.

## HTTP Handlers and Middleware

Recommended order from outside inward:

```text
trusted proxy/host -> request ID -> panic recovery -> access log/metrics/tracing
-> body/timeout/rate limits -> authentication -> organization/authorization
-> generated request validation -> handler/use case
```

Exact order is tested because recovery must still let logs/metrics observe 500s and
timeouts/auth must behave consistently. Middleware does cross-cutting mechanics,
not resource-specific authorization or domain transitions.

Handlers:

- parse generated request type and preconditions;
- derive principal/tenant from verified context;
- map into application command/query;
- invoke exactly one orchestrating use case where practical;
- map result/error centrally;
- never issue SQL or call providers directly.

## SQL and Schema Naming

- Tables/columns/indexes/constraints use lower `snake_case` and descriptive prefixes.
- Name constraints so code can map conflicts (`uq_orders_org_number`,
  `ck_ga_capacity_nonnegative`).
- Foreign key delete action is explicit; default to restrict for durable business
  records and cascade only for true owned children.
- Index names express table and leading columns/purpose.
- Migrations contain comments for unusual locks/backfills and use the repository's
  ordered naming convention.
- SQL files are grouped by owning domain and query names state intent.

## Caching

Cache only data that can be reconstructed and whose staleness is accepted by the
contract. Cache keys include environment, tenant, resource, version/audience/locale
as needed. TTL has jitter to prevent synchronized expiry. Invalidations are an
optimization; authoritative reads/transitions still validate PostgreSQL state.

Never cache raw credentials, full payment/webhook bodies, private ticket/order data
in a shared public namespace, or an authorization decision beyond its safe bounded
TTL. Redis errors degrade gracefully except security operations explicitly designed
to fail closed (for example one-time socket-ticket consumption).

## Feature Flags

Every flag has stable name, description, owner, default, scope, creation/expiry date,
rollout/rollback metric, and cleanup issue. Evaluate server-side for security/
financial behavior. Do not let a client flag grant authorization. Both paths remain
tested while flag exists. Schema must stay compatible with immediate rollback.

## Local Workflow

Common commands in the current template:

```bash
make help
make db-up
make migrate-up
make generate
make run-api
make run-worker
make test
make test-race
make test-integration
make lint
make vulncheck
make build
make verify
```

Do not run `make generate` casually without reviewing generated diffs. Local
environment files contain only non-production credentials and remain ignored.
Integration data is disposable; tests never point at a shared/production database.

## Pull Request Shape

Prefer one vertical behavior per change. PR description records:

- problem/user outcome and feature catalog ID;
- contract/schema/event changes;
- authorization/tenant/data classification;
- state/transaction/locks/idempotency;
- async/provider/retry behavior;
- telemetry and runbook impact;
- tests and evidence, including contention/failure cases;
- migration, deploy, flag, rollback, and compatibility plan;
- documentation/ADR updates and known follow-ups.

Generated files, dependency changes, migrations, and unrelated formatting are kept
separate where practical. Do not conceal a behavior change inside a refactor.

## Feature Implementation Worksheet

Copy this into an issue before implementing a new feature:

```text
Feature ID/name:
Actor and user outcome:
In scope / explicitly out of scope:
Permissions and tenant/resource ownership:
Inputs, limits, validation, and stable errors:
State machine and forbidden transitions:
Schema, constraints, indexes, retention:
Transaction boundary and lock order:
Idempotency/dedupe/version behavior:
Audit entry and domain/outbox events:
Jobs/providers, timeout/retry/dead-letter:
HTTP/polling/realtime contracts:
Logs, metrics, traces, dashboard, alerts, runbook:
Unit/integration/contract/concurrency/security/load tests:
Migration/backfill/deploy/flag/rollback:
Documentation and ADR:
Acceptance evidence:
```

