# Testing and Quality Strategy

## Principles

Tests prove business invariants and failure recovery, not only happy-path line
coverage. Prefer deterministic fakes for domain/application tests and real
PostgreSQL/Redis/provider emulators or sandboxes at integration boundaries. Every
production defect adds the smallest durable regression test at the layer that could
have prevented it.

## Test Layers

| Layer | Purpose | Expected speed/isolation |
|---|---|---|
| Domain unit | State transitions, policies, money, pricing, validation | Milliseconds, no I/O |
| Application unit | Authorization/orchestration, ports, idempotency decisions | Fast deterministic fakes |
| Repository integration | SQL, constraints, locks, transactions, RLS, migrations | Real isolated PostgreSQL |
| Adapter integration | Redis semantics, object store, provider mapping/signatures | Real service/emulator/sandbox |
| HTTP contract | OpenAPI parsing/auth/error/status/serialization | In-process server plus fakes/DB as needed |
| Worker integration | claim/lease/retry/crash/dedupe/outbox/inbox | Real PostgreSQL, controlled clock/provider |
| End-to-end journey | Publish→hold→pay→ticket→scan→refund | Deployed-like stack, few critical cases |
| Concurrency/race | Contention invariants and Go memory races | Repeated/parallel, real PostgreSQL |
| Load/soak | SLO, saturation, leaks, hot sessions, backlog recovery | Representative environment/data |
| Security | Tenant isolation, auth, abuse, fuzz, dependency/container scans | CI plus scheduled review |
| Resilience | Dependency timeout/outage, restart, failover, delayed events | Staging/game-day environment |

## Domain Test Matrix

### Identity and Tenancy

- every role/permission allow and deny path;
- cross-tenant substitution for IDs, filters, cursors, websocket topics, object keys;
- invitation expiry/replay/concurrent acceptance;
- last-owner rule and role-cache invalidation;
- revoked/expired API key/device/support session.

### Catalog

- seat-map validation rule table and immutable publication;
- event/session status transitions and invalid transitions;
- DST ambiguous/nonexistent local time and scheduling conflicts;
- publication blockers/warnings and stale checksum;
- money rounding and price snapshot golden vectors.

### Booking and Commerce

- hold owner, expiry boundary, renewal count, version conflict, release exactly once;
- all-or-nothing multi-seat and final-unit GA contention;
- checkout key replay/mismatch/crash recovery;
- payment success/failure/action/unknown and out-of-order duplicate webhooks;
- payment-after-expiry recovery and reallocation branches;
- full/partial concurrent refunds and provider timeouts;
- session cancellation batch resume and idempotency.

### Ticketing and Entry

- ticket issue/reissue/void/refund state and signing-key rotation;
- invalid/tampered/old-version/wrong-session/expired QR;
- delivery retry, bounce, resend, template escaping;
- concurrent online scans, scan retry, device scope, check-in window, override;
- offline manifest expiry/revocation and conflicting device sync.

### Growth, Reporting, Integrations

- promotion scope/window/rounding/combinability/final-redemption race;
- waitlist dedupe/order/offer expiry/conversion race;
- report totals reconcile to order components and expose freshness;
- export access/streaming/expiry/formula injection;
- webhook signature/retry/disable/replay and SSRF attack corpus.

## Concurrency Harness

Concurrency tests start from a known database state, synchronize goroutines at a
barrier, issue conflicting operations, and assert both responses and final database
invariants. Run cases repeatedly with varied concurrency and transaction scheduling.

Minimum scenarios:

- 100 attempts for one reserved seat: exactly one active/confirmed claim;
- many GA quantities against final capacity: sold/held never exceed capacity;
- expiration vs modification vs confirmation: one legal terminal path;
- duplicate checkout keys/process crashes: one order/provider intent;
- duplicate/out-of-order payment events: monotonic final state;
- simultaneous refunds: pending+succeeded does not exceed captured;
- multiple workers on same job/event: one logical side effect;
- two online scans plus offline sync: one canonical admission, all evidence retained.

Verify no database deadlock remains unhandled, retries are bounded, and locks are
released on cancellation/error.

## Fixtures and Time

- Builders create organization-scoped valid defaults and require explicit override
  for the property under test.
- IDs and clocks are injectable; domain tests never depend on wall-clock sleep.
- Database tests use transaction/schema/database isolation compatible with parallel
  execution and clean up reliably.
- Provider fixtures contain synthetic data only and are scrubbed of secrets.
- Golden files are reviewed as contracts, not blindly regenerated.
- Seed a deterministic demo organization for local exploration, separate from tests.

## Migrations and Queries

CI must:

1. apply all migrations to an empty database;
2. apply from the oldest supported production snapshot;
3. run down/up only where rollback is claimed safe;
4. validate constraints/indexes/RLS and generated query compilation;
5. test application compatibility during expand/contract windows;
6. run representative `EXPLAIN` checks or performance regression suite for critical
   availability/order/job queries.

Migration/backfill tests cover interruption/resume and bounded batches. SQL tests
assert tenant predicates, deterministic order, maximum limits, and expected lock
behavior.

## API and Event Contracts

- Lint and validate OpenAPI; generate code and fail if repository diff appears.
- Detect breaking API/schema changes against the released baseline.
- Test every operation's security requirements, body limits, unknown fields,
  validation, errors, idempotency, pagination, and conditional headers.
- Maintain event/webhook fixtures per schema version; consumers test duplicates,
  older versions, unknown additive fields, and ordering gaps.
- Verify no PII/secrets appear in errors, logs, spans, events, or audit snapshots.

## Fuzz and Property Tests

Fuzz JSON/error parsing, cursors, QR payloads, webhook signature headers, normalized
emails/codes/slugs, money component allocation, CSV escaping, and seat-map import.
Property tests assert:

- sum of allocated monetary components equals declared total under rounding;
- inventory counters stay non-negative and within capacity;
- state machines never reach forbidden transitions;
- encode/decode round trips preserve valid contracts;
- idempotent execution repeated N times has the same logical result as once.

Store and minimize crashing inputs as regression corpus.

## Performance Tests

Use representative data: small and very large organizations, large seat maps, hot
sessions, long order/audit histories, and realistic active socket/device counts.

Scenarios:

- browse and conditional availability polling;
- onsale burst with high seat conflict;
- steady checkout plus provider latency;
- payment webhook burst after provider recovery;
- entry peak with many devices;
- mass session cancellation/refund queue;
- webhook/email endpoint outage then backlog recovery;
- WebSocket connect/reconnect/slow-consumer storm;
- large report/export under normal booking load.

Capture latency histograms, error/conflict rates, DB queries/locks/pool waits, CPU,
memory/GC, Redis, queue lag, provider calls, and invariant outcomes. A test passes
only if both SLO and correctness hold.

## Resilience Tests

Inject database connection loss/slow query/failover, Redis restart/unavailability,
object-store timeout, provider 429/5xx/timeout/malformed response, process kill after
each durable boundary, lost/duplicate/delayed events, clock skew for clients/devices,
disk/full queue pressure, and graceful termination during sockets/jobs. Verify
degradation, retry limits, alerting, recovery, and no silent state corruption.

## CI Quality Gates

Required on pull request:

```text
format/import check
go test ./...
go test -race ./... (or split suite with all concurrency-critical packages)
go vet ./...
static lint
govulncheck/dependency/license policy
secret scan
OpenAPI/event lint + generated-code clean check
migration/query integration tests
container build and image scan
```

Run longer E2E, load, fuzz, resilience, backup-restore, and provider sandbox tests on
merge/nightly/release as appropriate. Flaky tests are defects: quarantine only with
owner, evidence, and short removal deadline; never normalize rerun-until-green.

## Coverage and Review

Coverage is a signal, not the goal. Set a repository floor after baseline and require
new domain/application code to cover meaningful branches, especially denied and
failure paths. Review checklist covers authorization, tenant scope, state machine,
money/time, transaction/locks, idempotency, events/audit, provider boundaries,
redaction, observability, migrations, and documentation.

## Release Evidence

Each release records commit/image digest, migration set, generated API/event schema
version, test/build/security results, known risks, flags/defaults, provider sandbox
evidence, load profile, rollback compatibility, backup/restore status, and approvers.
Critical manual launch checks are scripted and captured where possible.

