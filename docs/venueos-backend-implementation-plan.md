# VenueOS Backend Implementation Plan

> This is the original consolidated plan. The maintained detailed implementation
> contract is the [VenueOS documentation set](venueos/README.md), which includes the
> feature catalog, domain state machines, data/API/realtime/worker designs, security,
> operations, tests, roadmap, decisions, and repository instructions.

## 1. Goal and delivery baseline

VenueOS will be implemented as a modular Go monolith using this repository as the starting point. The first production workflow is: publish a venue event, expose live reserved-seat or general-admission availability, create a time-limited hold, take payment, confirm the order, issue tickets, and check tickets in without overselling or duplicating side effects.

The initial deployment consists of three binaries from this repository:

- `api`: REST, WebSocket connections, authentication enforcement, storefront and operator requests.
- `worker`: hold expiry, outbox dispatch, payment reconciliation, ticket generation, notifications, exports, webhook delivery, and cleanup jobs.
- `migrate`: explicit forward database migrations run before compatible application rollout.

PostgreSQL is authoritative. Redis may accelerate cache, rate limiting, live fanout, one-time WebSocket tickets, and presence, but losing Redis must not lose an order, payment, hold decision, ticket, audit record, or domain event. S3-compatible storage owns images, exports, and generated pass files. Stripe is the first payment adapter.

The initial release assumes one deployment region, UTC persistence, one currency per event, Stripe Connect accounts for tenant payment collection, fixed platform roles, and responsive web/PWA clients. The API remains provider-neutral at the identity and payment interfaces.

## 2. Repository evolution

Keep the current dependency direction and add vertical domain modules:

```text
cmd/
  api/                 API and WebSocket composition root
  worker/              Queue selection and worker composition root
  migrate/             Embedded Goose migrations
internal/
  platform/
    auth/              OIDC verification and realtime ticket issuance
    cache/             Redis client, rate limits, locks, and presence
    database/          pgx pool, transactions, tenant guard, health
    http/              middleware, RFC 9457 errors, pagination, idempotency
    jobs/              durable job leases, retries, dead letters
    outbox/            transactional event records and dispatch
    realtime/          connection hub, subscriptions, replay, Redis fanout
    storage/           S3-compatible object storage
    telemetry/         logs, metrics, traces, redaction
  identity/            platform users and identity-provider links
  organization/        tenants, memberships, roles, invitations
  venue/               venues, spaces, seat-map versions, seats
  event/               events, sessions, ticket types, publication
  inventory/           availability, seat allocations, holds, expiry
  order/               carts, orders, order items, totals, exchanges
  payment/             provider adapter, attempts, webhooks, reconciliation
  ticket/              issuance, token rotation, transfer, delivery
  checkin/             admission policy, online/offline check-in
  promotion/           promo codes, usage, waitlists, offers
  audit/               append-oriented privileged activity
  notification/        email and outgoing webhook delivery
  reporting/           sales, settlement, capacity, exports
api/                    OpenAPI sources grouped by domain
migrations/             backward-compatible schema migrations
```

Each domain owns models, policies, application services, repository ports, PostgreSQL adapters, SQLC queries, events, handlers, and tests. Cross-domain calls go through narrow application interfaces. Domains do not import HTTP, Redis, Stripe, environment configuration, or concrete repositories.

The generated HTTP contract remains based on `net/http`; adding chi is unnecessary while Go's method-aware `ServeMux` meets routing needs. OpenAPI source remains authoritative and generated output remains committed and checked for drift.

## 3. Platform contracts

### 3.1 Authentication and tenant authorization

- Accept OIDC access tokens from a configured issuer and audience. Validate signature through cached JWKS, issuer, audience, expiry, and subject.
- Store a local `users` record keyed to `(identity_provider, provider_subject)`. The identity provider owns passwords and MFA; VenueOS stores no password verifier.
- Resolve tenant context from the route or selected organization header only after checking active membership. Never authorize using an unverified tenant identifier from the client.
- Mint a short-lived internal principal containing user ID, organization ID, role/capabilities, membership version, and authentication time for the current request only.
- Cache membership decisions briefly in Redis and include membership version. Revocation updates the database and invalidates the cache; authorization falls back to PostgreSQL when Redis is unavailable.
- Enforce capabilities in application services, not only middleware. Initial roles are owner, organization admin, event manager, finance admin, box office staff, and gate staff. Event and venue assignments further narrow access.
- Service accounts use hashed API keys with prefix lookup, scopes, tenant, expiry, rotation, and last-used timestamp.

### 3.2 API conventions

- Base path `/v1`; opaque UUID identifiers; camelCase JSON; UTC RFC 3339 timestamps.
- RFC 9457 errors include `code`, `title`, `status`, `detail`, `fieldErrors`, `requestId`, and `retryable`. No internal SQL, provider, or stack details cross the boundary.
- Cursor pagination uses an opaque base64url envelope containing ordering values and a versioned HMAC. Responses contain `items`, `nextCursor`, and `hasMore`; offset pagination is not used for growing tables.
- Mutable aggregates expose `version`. Updates require `If-Match` and return 409 with the current version when stale.
- Retryable writes require `Idempotency-Key`. Keys are scoped by tenant, principal, operation, and normalized request hash. A reused key with a different body returns 422; an in-progress duplicate returns 409 with `Retry-After`; a completed duplicate returns the stored status and response.
- Resource reads emit ETags. Polling clients use `If-None-Match`, and unchanged responses return 304.
- Public list endpoints have bounded filter and page sizes. Query timeouts, body limits, rate limits, and maximum export sizes are explicit.

### 3.3 Transactions, audit, idempotency, and outbox

One user-visible state transition commits the following in one PostgreSQL transaction:

1. The aggregate change and its version increment.
2. The idempotency result or operation record.
3. A permission-filtered audit event for material or privileged changes.
4. One or more outbox events with stable IDs and schema versions.

The transaction helper accepts a function and supplies transaction-bound SQLC queries. Services own transaction boundaries; repositories never start hidden transactions. External calls do not run while holding inventory locks. Workers claim records with `FOR UPDATE SKIP LOCKED`, a lease deadline, bounded attempts, exponential backoff with jitter, and an inspectable dead-letter state.

## 4. Data model and integrity

All tenant-owned tables contain `organization_id`, and all relevant uniqueness and lookup indexes begin with it. Composite foreign keys include `organization_id` so a record cannot reference another tenant even if application code is wrong. Application queries always require tenant context. PostgreSQL row-level security is added as defense in depth using `SET LOCAL app.organization_id` inside transactions; maintenance workers use a separate restricted role and explicit tenant predicates.

Core tables are introduced in dependency order:

| Area | Primary tables | Important constraints |
| --- | --- | --- |
| Identity | `users`, `identity_links`, `refresh_sessions`, `api_keys` | Unique provider subject; hashed secrets only; revocation and expiry indexed |
| Tenancy | `organizations`, `memberships`, `roles`, `role_capabilities`, `resource_assignments`, `invitations` | Unique active membership per user and organization; membership version |
| Venue | `venues`, `venue_spaces`, `seat_map_versions`, `sections`, `rows`, `seats`, `entrances` | Published map immutable; seat labels unique per map; no overlapping logical identifiers |
| Events | `events`, `sessions`, `ticket_types`, `price_rules`, `custom_questions` | Publication state machine; valid sales window; version on editable aggregates |
| Inventory | `inventory_buckets`, `session_seats`, `holds`, `hold_items` | Nonnegative capacity; one active allocation per seat; indexed expiry and state |
| Orders | `orders`, `order_items`, `attendees`, `order_adjustments`, `order_notes` | Money in minor units; immutable financial lines after confirmation |
| Payments | `payment_attempts`, `provider_events`, `refunds`, `reconciliation_exceptions` | Unique provider event and attempt IDs; no duplicate effective refund |
| Tickets | `tickets`, `ticket_tokens`, `ticket_transfers`, `ticket_deliveries` | Only one active token generation; revoked tokens remain auditable |
| Admission | `check_ins`, `check_in_overrides`, `offline_scan_batches` | Atomic single-entry constraint unless the ticket policy allows re-entry |
| Growth | `promo_codes`, `promo_redemptions`, `waitlist_entries`, `waitlist_offers` | Usage enforced transactionally; one active offer per entry |
| Platform | `idempotency_records`, `audit_logs`, `outbox_events`, `jobs`, `job_attempts`, `realtime_events`, `webhook_endpoints`, `webhook_deliveries` | Stable event IDs; lease and retry indexes; retention timestamps |

Money is `(amount_minor bigint, currency char(3))`. All instants use `timestamptz`. Mutable business records have `version bigint not null default 1`. Deletion is explicit by domain: published events, financial records, tickets, provider events, audit records, and check-ins are never hard-deleted through normal APIs.

## 5. Critical booking transaction

### 5.1 Reserved seats

`POST /v1/sessions/{sessionId}/holds` receives seat IDs, an idempotency key, and the last observed availability version.

1. Validate event visibility, sales window, quantity, tenant, and pricing policy outside the lock transaction where safe.
2. Begin a short transaction and lock requested `session_seats` in stable seat-ID order to avoid deadlocks.
3. Require every row to be `available`; otherwise roll back and return 409 with the new availability version and alternatives hint.
4. Insert the hold and items, set each seat to `held`, set `hold_id` and `held_until`, and increment availability version.
5. Insert audit and outbox records, persist the idempotent response, then commit.

A partial unique index prevents more than one active held or sold allocation for `(session_id, seat_id)`. Five hundred concurrent requests for the same seat therefore produce one hold and bounded 409 responses, never oversell.

### 5.2 General admission

Use one inventory bucket per session and ticket type. Claim quantity with a conditional atomic update:

```sql
UPDATE inventory_buckets
SET held = held + $quantity, version = version + 1
WHERE organization_id = $organization_id
  AND id = $bucket_id
  AND capacity - sold - held >= $quantity
RETURNING *;
```

No returned row means insufficient inventory. Release and conversion updates use the same locked bucket and never let `held` or `sold` become negative.

### 5.3 Hold lifecycle and payment race

- Default hold duration is 10 minutes and is recorded by the server. Renewal is permitted once, only while active, and never beyond the configured maximum checkout window.
- The expiry worker claims due holds in small batches with `SKIP LOCKED`, releases inventory, marks the hold expired, and writes outbox events atomically.
- Starting payment changes the hold to `checkoutPending` and assigns a short payment deadline so ordinary expiry cannot release it mid-confirmation.
- Stripe calls occur after the local intent/attempt is committed. Provider idempotency uses the VenueOS payment attempt ID.
- Signed webhooks are timestamp checked, stored once by `(provider_account_id, provider_event_id)`, and processed idempotently.
- Payment success converts the hold to sold inventory, confirms the order, creates tickets, and writes audit/outbox records in one transaction.
- If success arrives after inventory was legitimately released, the payment enters `reconciliationRequired`; the system automatically requests reversal/refund when policy permits and alerts finance. It never silently recreates sold inventory.

## 6. REST surface

The OpenAPI contract is delivered in vertical slices. The planned resource groups are:

- `/v1/me`, `/v1/organizations`, `/v1/organizations/{id}/members`, invitations, roles, and API keys.
- `/v1/venues`, spaces, seat-map drafts, validation, publication, and versions.
- `/v1/events`, sessions, ticket types, pricing, publication, and public storefront projections.
- `/v1/sessions/{id}/availability`, holds, hold renewal, and release.
- `/v1/orders`, assisted box-office orders, notes, exchanges, and cancellations.
- `/v1/payments/checkout`, `/v1/webhooks/stripe`, payment attempts, and reconciliation exceptions.
- `/v1/tickets`, transfers, resends, wallet/pass downloads, and status.
- `/v1/check-ins`, lookup, override, offline batch synchronization, and event progress.
- `/v1/promotions`, waitlists, offers, and redemptions.
- `/v1/reports/sales`, capacity, settlements, exports, and export job status.
- `/v1/uploads` for presigned upload initiation and completion.
- `/v1/realtime/tickets` for one-time WebSocket connection authorization.

OpenAPI response schemas, generated Go server interfaces, generated TypeScript clients, examples, compatibility checks, and contract tests are CI gates.

## 7. Polling and WebSocket design

### 7.1 Authoritative polling

Polling is always supported; WebSockets only reduce delay.

- Availability: `GET /v1/sessions/{id}/availability` returns a compact snapshot, `version`, and ETag. Poll every 5 seconds while the seat picker is visible, slow to 20 seconds when idle, and stop when the page is hidden. Use 304 responses when unchanged.
- Checkout/order: poll every 2 seconds while payment is pending, then back off through 5, 10, and 30 seconds for at most 10 minutes. Stop on a terminal state.
- Operations and check-in dashboards: poll deltas with a signed cursor every 5 seconds when sockets are unavailable.
- Export and long-running jobs: poll the operation resource with exponential backoff and server-provided `Retry-After`.
- All polling receives jitter, a maximum interval, cancellation on navigation, and immediate refresh after reconnect or visibility restoration.

### 7.2 WebSocket connection and authorization

- Endpoint: `GET /v1/realtime` upgraded to WebSocket.
- Browser clients first call `POST /v1/realtime/tickets`. The API stores a hashed, single-use ticket in Redis with principal, tenant, allowed origin, and a 60-second TTL. The raw ticket is used only for the upgrade and is redacted from access logs.
- The server consumes the ticket atomically, rechecks membership version, validates `Origin`, and limits connections per user, tenant, and IP.
- Clients subscribe to authorized channels such as `session:{id}:availability`, `order:{id}`, `event:{id}:checkins`, and `organization:{id}:operations`. Every subscription is authorized independently.
- Client messages are limited to 16 KiB. Servers send ping every 25 seconds, require pong within 10 seconds, cap outbound queues at 256 messages, and close slow consumers with code 1013 so they reconnect and resynchronize.

### 7.3 Event envelope, fanout, and replay

```json
{
  "type": "availability.changed",
  "eventId": "opaque-sortable-id",
  "channel": "session:...:availability",
  "resourceId": "...",
  "version": 42,
  "occurredAt": "2026-09-19T12:00:00Z",
  "payload": {}
}
```

The business transaction writes an outbox event. The dispatcher writes a permission-safe `realtime_events` record and publishes its ID through Redis Pub/Sub. Every API replica subscribes and fans the event to matching local connections. Redis carries only the live notification; PostgreSQL retains the bounded replay window.

Clients reconnect with their last `eventId`. The API queries later authorized events in order. If the cursor is unknown, expired, over the replay limit, or crosses an authorization change, the server sends `resyncRequired`, and the client fetches a REST snapshot before subscribing again. Events may be duplicated, so clients deduplicate by `eventId` and apply only versions newer than local state.

WebSocket availability events contain version and changed identifiers, not the sole source of inventory truth. Before checkout, the hold API always performs the authoritative database transaction.

## 8. Workers, queues, and integrations

Start with PostgreSQL-backed jobs and outbox records to preserve simple deployment and transactional correctness. The worker command accepts queue selection and concurrency settings so deployments can scale `inventory`, `payments`, `notifications`, `exports`, and `webhooks` separately.

- Claim jobs in bounded batches with `SKIP LOCKED`; record worker ID and lease expiry.
- Renew leases for long jobs. Another worker may reclaim an expired lease, so handlers must be idempotent.
- Retry only classified transient failures. Use exponential backoff with full jitter and per-job maximum attempts.
- Move exhausted or invalid jobs to dead-letter state with operator-visible error category and replay controls.
- Use per-provider concurrency limits, circuit breakers, and retry budgets for Stripe, email, object storage, and outbound webhooks.
- Reconciliation jobs compare pending local payment/refund state with Stripe and surface exceptions rather than guessing.
- Migrate high-volume queues to NATS JetStream only when PostgreSQL queue contention or independent scaling is measured. Domain event contracts and consumer idempotency remain unchanged.

## 9. Pooling, limits, and caching

### PostgreSQL

- Set each process pool from a global connection budget: `(database limit - migration/admin reserve) / maximum API and worker replicas`, with separate API and worker caps.
- Configure maximum lifetime, idle lifetime, health-check period, acquisition timeout, and startup timeout. Emit pool acquired, idle, total, wait count, and wait duration metrics.
- Apply statement timeouts by workload: short interactive reads, bounded booking writes, and a separate reporting/export role for long queries.
- Add PgBouncer in transaction-pooling mode only when replica growth requires it; configure pgx query execution compatibly and test migrations and advisory behavior before rollout.
- Never hold a transaction open across HTTP provider calls, WebSocket writes, file uploads, or user think time.

### Redis

- Use a bounded client pool with connect, read, and write deadlines. Prefix every key by environment and purpose; tenant identifiers are included where data is tenant-specific.
- Cache only reconstructible data. Use short TTLs with jitter, request coalescing for hot misses, and explicit invalidation from outbox consumers.
- Implement token-bucket rate limits for login, public search, hold creation, checkout, ticket lookup, and WebSocket connection/subscription attempts.
- On Redis failure, reject operations that require a one-time realtime ticket or strict distributed rate limit, but continue authoritative booking paths with conservative local limits when safe.

### HTTP and WebSockets

- Bound request bodies, headers, read/write/idle timeouts, handler deadlines, concurrent exports, and per-instance WebSocket connections.
- Keep per-connection state small and never create an unbounded goroutine or queue per subscription.
- Publish connection count, subscription count, outbound queue depth, dropped/closed slow consumers, reconnects, replay size, and resync count.

## 10. Security and privacy

- TLS terminates at the edge; secure headers, strict allowed origins, CSRF protection for cookie-authorized operations, and request-size limits are mandatory.
- All secrets come from a managed secret store. Logs and traces redact authorization headers, cookies, realtime tickets, QR tokens, payment details, personal answers, and provider payload secrets.
- QR codes contain an opaque random token, not order or attendee data. Store only a keyed hash; transfer or reissue revokes the previous token generation.
- Stripe webhooks require signature and timestamp validation before parsing into domain commands. Outgoing webhooks are signed, replay protected, and retain delivery history.
- Uploads use tenant-prefixed presigned URLs, content length and type constraints, checksum verification, private buckets, malware-scan state, and expiring downloads.
- Audit records include actor, tenant, capability, action, target, reason where required, result, request ID, trace ID, and timestamp. Audit access is itself audited.
- CI includes tenant-isolation, IDOR, permission escalation, idempotency replay, webhook replay, secret-leak, race, dependency, and container scanning tests.

## 11. Observability and operations

- Logs include service, environment, request ID, trace ID, tenant-safe identifier, principal ID where allowed, route/job, result, duration, and error category.
- Traces cover HTTP, SQL, Redis, outbox publish, job consume, WebSocket upgrade, replay, Stripe, email, and object storage. Production sampling is configurable, with errors and critical booking traces retained at a higher policy.
- Metrics cover HTTP golden signals, DB and Redis pools, lock wait, hold contention, hold expiry lag, orders by state, payment webhook age, reconciliation exceptions, queue depth/age/retries/dead letters, ticket issuance, check-in conflicts, and realtime health.
- Liveness checks only process health. Readiness checks required dependencies with short budgets and fails when the instance cannot safely accept its assigned workload.
- Initial SLOs follow the SRS: 99.95% public reads, 99.9% booking writes, p95 public availability reads below 250 ms, and p95 hold creation below 500 ms excluding payment-provider time.
- Runbooks cover database saturation, Redis loss, queue lag/dead letters, Stripe outage, object storage failure, realtime degradation, credential rotation, rollback, and restore.

## 12. Test strategy and acceptance gates

### Correctness

- Unit tests for state machines, totals, tax/fee rounding, permissions, cursor signing, idempotency classification, QR rotation, retry classification, and error mapping.
- PostgreSQL integration tests for every repository, migration up/down where safe, tenant composite constraints, row-level security, lock ordering, hold expiry, unique provider events, cursor pagination, job leases, and transaction rollback.
- Concurrency test: at least 500 simultaneous attempts for one reserved seat produce exactly one active hold and no negative or duplicate allocation.
- Payment tests cover duplicate and out-of-order webhooks, timeout after local commit, success after expiry, duplicate refunds, and reconciliation.
- Realtime tests cover unauthorized channels, duplicate delivery, reconnect replay, cursor expiry, slow consumers, Redis loss, resync, and multiple API replicas.

### Performance and resilience

- Run baseline, spike, contention, and soak tests against the complete availability-to-hold path. Record p95/p99 latency, pool waits, row-lock time, Redis latency, queue lag, CPU, and memory.
- Terminate API and worker instances during holds and webhook processing; effective outcomes must remain idempotent and recoverable.
- Delay or remove Redis; REST remains authoritative, polling recovers, and no confirmed state is lost.
- Exercise PostgreSQL point-in-time recovery and object-storage restore before production.

### Release gates

- Generated contracts and SQL have no drift; migrations are backward compatible with the previous application version.
- Unit, race, integration, contract, authorization, security, and load budgets pass.
- No unresolved critical security or data-integrity defects.
- Dashboards, alerts, on-call ownership, and changed runbooks ship with operational behavior.

## 13. Delivery sequence

### Phase 0 Foundation

Refactor platform packages, add Redis and object-storage adapters, OIDC verification, tenant context, transaction helper, standard errors, cursor pagination, idempotency records, audit, outbox, durable jobs, telemetry, local Compose services, and CI security/contract gates.

Exit: one protected tenant-scoped read and idempotent write commits domain state, audit, and outbox together; a worker consumes the event with visible retry behavior.

### Phase 1 Catalog and publication

Implement organizations, memberships, venues, immutable seat-map versions, events, sessions, ticket types, pricing, sales windows, publication validation, storefront projections, and presigned image uploads.

Exit: an event manager can publish a valid GA or reserved-seat session; unauthorized and cross-tenant access tests pass.

### Phase 2 Inventory and realtime

Implement availability snapshots, reserved-seat and GA holds, expiry and renewal, alternatives hints, Redis fanout, WebSocket tickets/subscriptions/replay, ETag polling fallback, contention metrics, and load tests.

Exit: the 500-buyer contention test produces no oversell; socket loss and Redis loss recover through authoritative polling.

### Phase 3 Orders payments and tickets

Implement carts, immutable order totals, Stripe Connect adapter, attempts, signed webhooks, reconciliation, confirmation transaction, ticket tokens, delivery, transfer, resend, and wallet/pass generation.

Exit: duplicate and out-of-order provider events produce one effective booking and one active ticket generation.

### Phase 4 Operations

Implement online check-in, gate attribution, duplicate detection, supervisor override, offline batch sync, refunds, exchanges, order timeline, audit UI data, notification inbox, and operational dashboards.

Exit: two gates cannot consume a single-entry ticket twice; offline conflicts are visible and auditable.

### Phase 5 Commercial readiness and scale

Implement promotions, waitlists, finance reports, exports, outgoing webhooks, API keys, quotas, retention jobs, SLO dashboards, chaos tests, restore exercise, and measured queue/database tuning.

Exit: pilot tenants can be onboarded with runbooks, reconciliation, security review, accessibility review, capacity evidence, and recovery evidence.

## 14. Immediate engineering backlog

1. Replace the example `user` API with the VenueOS error envelope, tenant principal, cursor, idempotency, transaction, audit, outbox, and job primitives.
2. Add PostgreSQL, Redis, MinIO, Mailpit, and a local OIDC test issuer to Compose; keep all ports loopback-bound.
3. Create migrations for organizations, users, identity links, memberships, roles, idempotency records, audit logs, outbox events, jobs, and realtime events.
4. Implement one vertical organization endpoint with tenant isolation, capability authorization, audit, outbox, generated OpenAPI, SQLC, and Testcontainers coverage.
5. Add the worker lease/retry/dead-letter engine and dispatch one observable organization event.
6. Implement venue and event aggregates before inventory so publication rules and immutable seat-map versions are stable.
7. Build the hold transaction and contention harness before connecting Stripe or frontend checkout.
8. Add realtime only after REST snapshots and cursor polling are correct; sockets must never become the source of truth.

## 15. Explicit deferred scope

Global active-active writes, native mobile apps, arbitrary custom-role builders, automated tax-provider integration, enterprise SSO, multi-currency orders, advanced subscription billing, a separate search cluster, and microservice extraction are deferred until product validation or measured scale justifies them. Their boundaries remain represented by interfaces and events so they do not require rewriting the booking core.
