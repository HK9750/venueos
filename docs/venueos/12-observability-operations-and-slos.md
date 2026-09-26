# Observability, Operations, and SLOs

## Service-Level Objectives

Initial monthly objectives, finalized after load testing and business review:

| Journey | SLI | Objective |
|---|---|---:|
| Public catalog/availability reads | Valid non-5xx responses excluding approved client errors | 99.95% |
| Booking writes | Valid hold/checkout responses excluding domain conflicts/client errors | 99.9% |
| Online entry validation | Valid scan decision responses | 99.9% during active sessions |
| Availability latency | p95 server duration | <250 ms at reference load |
| Hold creation latency | p95 server duration excluding provider work | <500 ms at reference load |
| Realtime propagation | p95 commit-to-client eligible delivery | target ≤2 s |
| Payment event processing | p95 verified receipt-to-applied state | target ≤30 s |
| Critical jobs | Oldest runnable age | target <60 s normal operation |

Define denominator, exclusions, measurement source, low-traffic handling, and error
budget policy before production. Do not exclude dependency failures merely because
they originate outside VenueOS.

## Health Endpoints

- `/livez`: process event loop is alive; no dependency calls.
- `/startupz`: mandatory initialization/config/migrations compatibility complete.
- `/readyz`: instance can safely receive its workload; fast bounded checks or recent
  dependency health, not an expensive query cascade.
- `/version`: build version/commit/time without secret/config data.
- `/metrics`: admin listener only.
- pprof: disabled by default; separate admin listener and network protection when
  temporarily enabled.

During graceful shutdown readiness fails first, admission of new requests/socket
upgrades/job claims stops, current work drains to deadline, then connections close.

## Structured Logging

JSON logs include timestamp, level, service, environment, version, message, request/
trace IDs, route template, method/status/duration, organization where safe, actor/
credential type and redacted ID, domain operation, aggregate ID, job/event ID, and
classified error. Route templates are used instead of raw paths for metrics.

Never log bearer/API/device/order access tokens, cookie/auth headers, provider
secrets/signatures, raw webhook/payment/QR data, ticket payloads, full email/phone,
unbounded request/response body, or SQL parameters containing customer data.
Redaction is centralized and tested.

## Metrics

### HTTP and Dependencies

- request rate, duration, response size, status/error code by method/route;
- in-flight requests and timeout/cancellation count;
- database query duration/errors, pool open/in-use/idle/wait count/wait duration;
- Redis operation duration/errors/pool waits/pubsub reconnects;
- provider request duration/result/circuit state by provider/operation;
- object-storage operations/bytes/errors.

### Business and Consistency

- hold create success/conflict/error/expiry, lifetime, active count;
- checkout/order/payment transition counts and age in pending/unknown states;
- confirmed sales and amounts (access-controlled dashboard, not high-cardinality
  public metrics); refund state/age; reconciliation discrepancy count/value;
- ticket issue/render/delivery success/failure/age;
- scan decisions/latency, duplicate/invalid/override/offline conflict rates;
- waitlist offer/convert and promotion rejection/redemption rates;
- invariant checker failures and late-payment recovery/reconciliation paths.

### Async and Realtime

- queue depth/runnable age/attempt/dead letter by job type;
- outbox unpublished age, inbox unprocessed age;
- webhook delivery status/latency and disabled endpoint count;
- active sockets, accepts/rejects, queue depth, disconnect reasons, heartbeats,
  replay/resync, snapshot 304 ratio, poll rate, event delivery lag.

Metric labels must be bounded. Organization/session/order/job IDs belong in traces or
logs, never Prometheus labels.

The worker foundation exports completed run count and duration by registered job
type/result/class, plus due queue depth and oldest due age by registered type.
Unsupported records collapse to the static `unsupported` label and exhausted lease
sweeps to `expired_lease`; arbitrary database job types never become metric labels.
The worker exposes these metrics on the same separately configured admin listener
policy as the API process.

## Tracing

Use OpenTelemetry across inbound HTTP, application use case, SQL/Redis/provider/
storage calls, outbox creation, and worker processing. Propagate W3C trace context in
internal messages/outbound calls where safe. Link worker spans to the originating
event rather than keeping an indefinitely open trace.

Sample errors and critical booking/payment/entry paths at higher rates, with a
bounded configurable baseline. Span attributes are allowlisted and exclude PII,
tokens, raw queries, or payloads.

## Dashboards

1. **Service overview:** SLO/error budget, traffic, latency, saturation, deploys.
2. **Booking:** availability/hold/checkout rates, conflicts, expiry, DB lock/pool,
   hot sessions, inventory invariant checks.
3. **Payments:** attempts by state, webhook lag/errors, pending/unknown age, refund
   backlog, reconciliation discrepancies, provider health.
4. **Fulfilment:** ticket issuance/render/delivery backlog, bounces, resend rate.
5. **Entry:** scans/latency/results by allowed dimensions, online/offline ratio,
   duplicates/conflicts, device health, manifest freshness.
6. **Workers/integrations:** queue ages/dead letters, outbox/inbox, outbound webhooks,
   exports, provider circuits.
7. **Infrastructure:** replicas/CPU/memory/GC, database capacity/replication/storage,
   Redis memory/connections, object storage errors.

## Alerting

Alerts must be actionable, symptom-first, deduplicated, and linked to runbook:

- multi-window SLO burn for catalog, booking, and scans;
- elevated 5xx/latency/timeouts or database pool wait;
- no successful holds/orders/scans during expected active traffic;
- inventory invariant/oversell signal (page immediately);
- payment webhook/outbox/critical job age beyond threshold;
- unknown payment or reconciliation discrepancy/value above threshold;
- refund/ticket/delivery critical backlog;
- dead letters increasing;
- Redis loss with realtime degradation; WebSocket reconnect/resync storm;
- database replication/storage/connection saturation;
- signing key/provider credential near expiry;
- suspicious cross-tenant authorization denials or scan/signature abuse.

Low urgency data freshness, individual tenant endpoint failure, and capacity trends
create tickets/notifications instead of waking on-call unless impact is broad.

## Capacity and Pool Budgets

Set a database connection budget from server maximum minus replication, migrations,
administration, and safety reserve. Divide remaining connections across maximum API
and worker replicas, not current replicas. Each process configures maximum/minimum,
acquire timeout, connection lifetime jitter, idle time, health check, and per-query/
transaction timeouts.

Worker concurrency must not equal DB pool size blindly; jobs may use provider/network
capacity. Separate semaphores by criticality/provider. Redis uses command and
dedicated Pub/Sub pools. HTTP clients reuse transports and bound every phase.

Reference load models include ordinary day, onsale burst for hot session, session
entry spike, mass cancellation/refund, email delivery backlog, webhook endpoint
failure, Redis outage, and database failover. Record tested hardware/data volume and
headroom rather than quoting context-free requests/second.

## Deployment

- Build reproducibly, pin Go/tool/image versions, embed commit/version, generate
  SBOM and signed provenance/image if platform supports it.
- Run as non-root with read-only filesystem, minimal image, resource requests/limits,
  disruption budget, anti-affinity, and least-privilege workload identity.
- Migrations are an explicit pre-deploy job and follow expand/contract rules.
- Deploy canary or small rolling batch; compare SLO/business metrics and abort on
  regression. API, worker, and migration versions declare schema compatibility.
- Rollback normally means prior code against expanded schema; destructive schema
  contraction waits past rollback window.
- Feature flags isolate risky behavior by organization/percentage and include an
  immediate kill switch for providers/live features.

## Backup and Disaster Recovery

- PostgreSQL has encrypted automated backups plus point-in-time recovery and tested
  retention. Object storage uses versioning/lifecycle where required. Redis is not
  relied on for recovery of durable business state.
- Define business-approved RPO/RTO. Initial target proposal: RPO ≤5 minutes for
  PostgreSQL and RTO ≤60 minutes, subject to infrastructure/provider validation.
- Quarterly restore drills create an isolated environment, restore DB/object
  references, validate migrations, row counts, sample orders/tickets, tenant
  isolation, and application startup. Document actual RPO/RTO and issues.
- Disaster procedure covers provider webhook replay/reconciliation and rebuilding
  reports/realtime caches after restore.

## Runbooks

Create concrete files under `docs/venueos/runbooks/` before launch for:

- elevated HTTP errors/latency;
- database saturation, locks, failover, and storage growth;
- Redis outage and realtime degradation;
- stuck/dead-letter jobs or outbox/inbox backlog;
- payment provider outage, delayed/missing webhooks, unknown payment;
- refund backlog or reconciliation discrepancy;
- oversell/inventory invariant breach;
- ticket issuance/delivery/signing-key failure;
- scanner/check-in degradation and offline-mode activation;
- outbound webhook abuse/SSRF concern;
- leaked credential/key rotation;
- tenant suspension/data export/deletion;
- rollback and database restore.

Each runbook states symptoms, impact, dashboards/queries, immediate containment,
diagnosis, safe remediation, verification, escalation/communications, and follow-up.
No runbook should require unreviewed direct mutation of core business tables.

## Incident Process

Define severity by customer/financial/security impact; assign incident commander,
operations lead, communications, and scribe. Preserve timestamps/evidence, prefer
reversible mitigations, communicate known facts and next update time, and never hide
uncertainty. After stabilization, reconcile financial/inventory state, notify
affected parties as required, produce blameless review with owned actions, and add
tests/alerts/runbook improvements.
