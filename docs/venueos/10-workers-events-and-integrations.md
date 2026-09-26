# Workers, Events, and Integrations

## Durable Work Model

State-changing transactions insert outbox events and jobs in PostgreSQL. Worker
replicas claim bounded batches using `FOR UPDATE SKIP LOCKED`, set owner/lease expiry,
commit, then execute outside the claim transaction. A heartbeat extends only active
long jobs. A crashed lease becomes claimable after expiry.

Jobs are at-least-once. Every handler must therefore be idempotent through a stable
dedupe key, target-state check, unique constraint, provider idempotency key, or
combination. “The queue delivers once” is never an assumption.

The foundation runner implements atomic bounded claims, expired-lease reclamation,
owner-checked completion, full-jitter exponential retry, terminal dead-lettering,
per-handler timeouts, and explicit type/schema registration. Claims commit before
handler execution. The configured lease must exceed the handler timeout; heartbeat
support is reserved for a later long-running handler that cannot be split into
bounded work. Process shutdown leaves active leases untouched so another replica
can reclaim them after expiry. Unknown raw errors are persisted only as a generic
safe message; handlers must opt in to any bounded operator-safe detail.

## Job Record and States

Required fields: ID, organization (nullable only for platform jobs), type, schema
version, priority, dedupe key, payload, state, `run_at`, attempt/max attempts, lease
owner/expiry, last error class/message, trace/request correlation, created/started/
finished timestamps.

States:

```text
queued -> leased -> succeeded
   ^        |  \-> retry_wait -> queued
   |        +----> dead_letter
   +------------- cancelled
```

Cancellation is only valid before irreversible provider work. Operators can replay
a dead-letter job only after fixing cause; replay creates an audited attempt with
the same logical dedupe lineage.

## Retry Policy

Errors are classified:

- `permanent`: validation, unsupported provider response, revoked endpoint, missing
  terminal target—do not retry;
- `transient`: timeout, connection reset, provider 429/5xx, temporary object-store
  issue—exponential backoff with full jitter and provider `Retry-After` cap;
- `conflict`: lock/version/contention—short bounded retry or fresh reload;
- `unknown`: few conservative retries, then dead letter and alert.

Default backoff is documented per job type; no infinite retry. Attempts and error
messages are bounded/redacted. Circuit breakers protect unhealthy providers while
allowing probes and separate provider/tenant isolation.

## Scheduled Jobs

| Job | Cadence/trigger | Behavior |
|---|---|---|
| `holds.expire` | continuous due-time scan | Transition active expired holds and release inventory in small batches |
| `idempotency.prune` | hourly/daily | Remove response bodies/tombstones after endpoint retention |
| `realtime.prune` | scheduled | Delete replay events beyond retention in bounded chunks |
| `ticket.issue` | order confirmed event | Ensure entitlements/tickets exist exactly once |
| `ticket.render` | ticket issued/reissued | Produce PDF/wallet artifact, verify checksum, attach safely |
| `delivery.send` | receipt/ticket/waitlist/refund event | Send through email/SMS adapter and record attempt |
| `webhook.deliver` | subscribed domain event | Sign and POST versioned envelope; retry/dead-letter |
| `payment.process_event` | verified inbox row | Apply provider event idempotently and trigger confirmation/refund state |
| `payment.reconcile` | unknown state or schedule | Fetch provider facts and resolve/classify discrepancy |
| `refund.execute` | refund requested | Call provider idempotently and persist outcome |
| `waitlist.offer` | inventory became eligible | Select next candidate and create expiring offer/hold |
| `export.generate` | export requested | Stream bounded query to artifact and publish completion |
| `reports.rollup` | incremental event/schedule | Update rebuildable aggregate projections |
| `assets.verify` | upload finalized | Verify size/type/checksum/malware/process variants |
| `retention.purge` | daily | Delete/anonymize eligible data with counts/audit |
| `keys.rotate` | scheduled/manual | Prepare/re-sign where necessary and retire only after safe overlap |

The inventory repository already provides the bounded, idempotent database
transaction used by `holds.expire`: due active holds are locked with
`SKIP LOCKED`, their GA counters are released, the session revision advances, and
`hold.expired` audit/outbox records commit with the state change. The API worker now
registers the `holds.expire` handler and claims one PostgreSQL schedule row per
bounded tick; schedule advancement and job enqueue happen in one transaction.

Scheduler leadership uses PostgreSQL advisory lock or deduplicated schedule rows.
Every scheduled action remains safe if two schedulers briefly overlap.

The worker schedules invitation expiry, hold expiry, bounded pruning of expired
realtime replay rows, and idempotency-record retention. Both prune jobs use short
`SKIP LOCKED` batches; idempotency pruning leaves an actively leased processing
record untouched and neither job contends with availability readers for the whole
table.

## Domain Event Envelope

```json
{
  "id": "evt_...",
  "type": "order.confirmed",
  "schema_version": 1,
  "organization_id": "org_...",
  "aggregate": {"type": "order", "id": "ord_...", "version": 4},
  "occurred_at": "2026-09-20T10:00:00Z",
  "correlation_id": "req_...",
  "causation_id": "evt_...",
  "data": {}
}
```

Payloads carry identifiers and immutable facts, not whole mutable aggregates or
secrets/PII. Schema versions are independently evolved. Consumer idempotency keys
include event ID and consumer name. Events are named in past tense and emitted only
after the corresponding transition is committed.

Initial event catalog:

```text
organization.suspended membership.revoked
venue.updated seat_map.published event.published
session.published session.cancelled inventory.changed
hold.created hold.modified hold.expired hold.released hold.confirmed
order.created order.confirmed order.payment_failed order.cancelled
payment.updated refund.requested refund.succeeded refund.failed
ticket.issued ticket.reissued ticket.voided delivery.failed
ticket.admitted scan.recorded
promotion.redeemed waitlist.joined waitlist.offered waitlist.converted
export.completed webhook.endpoint_disabled reconciliation.discrepancy_found
```

## Outbox Publication

Outbox rows are inserted in the business transaction. Relay claims unpublished rows,
publishes to internal consumers/Redis or materializes jobs, and marks completion.
Crash between publish and mark causes duplicate delivery, so consumers deduplicate.
Rows exceeding attempts enter dead-letter state with alert and operator replay.

The PostgreSQL relay foundation in `internal/platform/outbox` now performs bounded
claiming with expiring owner leases, owner-checked publish/retry/dead-letter
transitions, exponential backoff with jitter, safe failure messages, and a sweep
for exhausted leases. Wiring each domain event to a concrete internal consumer,
Redis fan-out, or provider adapter remains domain-specific and must preserve
consumer idempotency.

Publication order is guaranteed only per aggregate version where required. A
consumer encountering a future version may reload current state or delay; it must
not assume global ordering.

## Payment Adapter

Port capabilities:

- create/retrieve/cancel payment intent;
- create/retrieve refund;
- verify/decode webhook;
- list transactions/disputes for reconciliation;
- connectivity/account capability check.

Provider-specific status/error objects are mapped to domain-neutral classifications
while sanitized raw references remain available to finance support. Each mutation
uses provider idempotency. Connect/account identity is always tenant-bound and
validated against webhook object metadata.

## Email and SMS Adapters

Messages are rendered from versioned templates with locale fallback and tested
subjects/body. Variables are allowlisted and escaped for context. Secure links use
short-lived scoped tokens; attachments are avoided or size-limited.

Adapters return provider message ID and classified result. Bounce/complaint/status
webhooks are signature-verified and inbox-deduplicated. Suppression prevents repeated
sending to known hard bounces/complaints. SMS is regionally enabled, consent-aware,
rate/cost limited, and contains no sensitive order detail.

## Object Storage Adapter

Port supports signed upload/download, stat, put stream, get stream, and delete.
Object keys are generated server-side with environment/organization/type prefixes;
user filenames are metadata only. Enforce MIME/extension/signature/size limits,
encryption, private bucket policy, lifecycle expiry, checksums, and short signed URL
TTL. Export and ticket jobs stream instead of buffering large files in memory.

## Outbound Webhooks

Endpoint configuration requires verified HTTPS URL, event selection, active status,
and secret. SSRF controls resolve/validate every connection through restricted
egress, blocking loopback, private/link-local/metadata ranges and unsafe redirects.

Delivery headers include delivery/event ID, event type/version, timestamp, and
HMAC signature over timestamp plus raw body. Receivers must tolerate duplicates.
VenueOS records response status, bounded response sample, duration, attempt, next
retry, and terminal reason.

Retry transient network/408/409/425/429/5xx outcomes with jitter; most other 4xx are
permanent after limited confirmation. Repeated terminal failures disable/pause an
endpoint according to policy and notify administrators. Manual replay preserves
original event with a new delivery attempt ID and is audited.

## Reporting and Exports

Operational screens may query indexed authority for narrow windows. Larger reports
use incremental read models that are rebuildable and expose `data_as_of`. Financial
totals reconcile from immutable order/payment/refund components, not mutable display
fields.

Export request freezes filters, fields, locale/timezone, requester, and authorization
context. Worker rechecks tenant/permission, streams CSV/JSON safely, neutralizes CSV
formula injection, encrypts/stores artifact, and returns short-lived signed download.
Artifacts expire automatically and access is audited.

## Pooling and Backpressure

- API and worker use separate PostgreSQL credentials/pools and connection budgets.
- API pool leaves headroom for migrations, operators, and worker critical jobs.
- Worker concurrency is per job class/provider; do not let email backlog starve
  payment webhooks, expirations, or refunds.
- Redis connections are pooled with operation deadlines; Pub/Sub uses dedicated
  connections separate from command pool.
- HTTP transports are reused with bounded idle connections, connect/TLS/header/body
  timeouts, response-body limits, and no unbounded retry middleware.
- Jobs are claimed only when worker capacity exists; queues and in-process channels
  are bounded.

## Operator Controls

Admin APIs/UI can inspect queue by state/type/age, retry eligible dead letters,
cancel queued jobs, pause a provider/job type, inspect redacted attempt history,
and trigger reconciliation. Every operator mutation requires permission, reason,
request ID, and audit. Direct editing of job/business rows is not an operating model.

## Tests

- Crash before/after claim, provider call, state save, publish, and acknowledgement.
- Lease expiry with two workers and heartbeat behavior.
- Provider timeout/429/5xx/permanent response classification and backoff bounds.
- Duplicate outbox/inbox/job execution yields one logical result.
- Webhook signature, replay, SSRF/DNS/redirect, response-size, and disable policy.
- Large exports stream within memory bound and escape formula payloads.
- Per-class concurrency proves critical jobs progress during bulk delivery backlog.

The current foundation integration suite verifies tenant-aware enqueue deduplication,
exclusive leases across competing owners, owner-checked state transitions, retries,
successful completion, final-attempt lease expiry, and dead-letter sweeping.
