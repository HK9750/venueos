# Delivery Roadmap

## Delivery Method

Build vertical, releasable slices rather than completing all tables or layers first.
Each milestone includes contract, migration, implementation, worker effects, audit,
telemetry, tests, documentation, and a demo. Do not begin a dependent milestone
until the prerequisite exit criteria are evidenced.

Sizing is intentionally omitted until the team, deployment platform, identity and
provider choices, and required launch scope are confirmed. Use milestone exit
criteria—not calendar optimism—to report progress.

## Milestone 0 — Decisions and Development Baseline

Deliver:

- resolve launch questions in the decision register;
- confirm deployment platform, primary region, managed PostgreSQL/Redis/object
  storage, identity provider, Stripe account model, email/SMS vendors;
- establish local compose/test dependencies and sanitized demo seed;
- CI checks, tool pinning, generated-code check, container builds, SBOM/scans;
- ADR format/index and change template/checklists;
- environment/config/secrets matrix and service ownership/on-call plan.

Exit: a fresh developer can build/test/run API+worker+migrations; CI is green; no
production secret is needed locally; every P0 external decision has owner/deadline.

## Milestone 1 — Platform Runtime

Deliver:

- API/worker/migrate binaries, validated process-specific config;
- logging, request/trace IDs, OpenTelemetry, metrics, panic recovery;
- public/admin listeners, liveness/startup/readiness, graceful shutdown;
- PostgreSQL and Redis clients with budgets/timeouts; object/provider ports;
- stable error envelope, request limits, validation, pagination/cursor foundation;
- migration/query generation workflow.

Exit: dependency outage and shutdown tests pass; telemetry is visible; admin surface
is not publicly exposed; base SLO dashboards exist.

## Milestone 2 — Identity, Organizations, and Audit

Deliver:

- OIDC validation and principal context;
- organization lifecycle/settings;
- users, memberships, invitations, fixed roles/permissions;
- tenant-scoped repositories and optional RLS proof-of-concept/decision;
- API keys and device credential foundation;
- immutable audit writer/explorer basics;
- idempotency, outbox/inbox/job primitives.

Exit: permission matrix and automated cross-tenant suite pass; invite/revoke/rotate
flows work; mutations atomically produce audit/outbox/idempotency where applicable.

## Milestone 3 — Venue, Map, and Event Catalog

Deliver:

- venues, spaces, gates;
- versioned seat-map draft/validation/publication and GA pool templates;
- event revisions, media upload/finalize, policies;
- sessions, schedule validation, price tiers, channels/allocations;
- publication validation and public catalog endpoints.

Exit: an administrator can create and publish assigned-seat and GA sessions; invalid
config produces actionable report; historical versions are immutable; public catalog
is tenant-safe and cacheable.

## Milestone 4 — Inventory and Holds

Deliver:

- materialize session seats/GA pools;
- availability snapshot/ETag/revision;
- reserved-seat and GA hold algorithms;
- hold ownership, retrieve, release, expiry, modification, renewal policy;
- expiration worker, outbox/realtime event rows;
- hot-session limits and concurrency/load suite.

Exit: contention tests prove zero oversell and bounded latency at reference load;
expired holds cannot confirm; crashes/retries release/retain inventory correctly;
Redis loss does not affect durable correctness.

## Milestone 5 — Cart, Pricing, and Checkout Skeleton

Deliver:

- server-side cart/customer/consent;
- pricing engine for base, fees, tax, rounding and snapshots;
- checkout validation and idempotent order creation;
- guest order access and staff order search/detail;
- order state machine and commercial audit/events.

Exit: deterministic golden price tests pass; duplicate checkout creates one order;
order snapshot is reproducible after catalog changes; no provider call occurs inside
inventory transaction.

## Milestone 6 — Payments and Confirmation

Deliver:

- Stripe-first adapter behind payment port and tenant account connection;
- payment intent action flow and provider idempotency;
- signature-verified durable webhook inbox;
- order confirmation/hold consumption/inventory sale;
- failure/retry and late-payment reconciliation states;
- payment operations dashboard/runbooks.

Exit: sandbox end-to-end purchase works; duplicate/out-of-order/timeouts converge;
payment-after-expiry branches are tested; unknown states alert and can reconcile;
client redirects are never treated as proof.

## Milestone 7 — Tickets and Delivery

Deliver:

- entitlement and ticket issuance workers;
- signing-key lifecycle and QR verification library;
- ticket retrieve, PDF/wallet artifact path;
- receipt/ticket email adapter, template versions, attempts and resend;
- void/reissue and delivery operations view;
- SMS optional behind flag after consent/cost controls.

Exit: each confirmed entitlement produces exactly one current valid credential;
retry does not duplicate; key rotation and old-version rejection pass; delivery
failure is visible/retryable without changing payment status.

## Milestone 8 — Entry Operations

Deliver:

- device enrollment/assignment/revocation;
- low-latency online scan and immutable attempts/admission;
- duplicate/wrong-session/window/void/refund decisions;
- manager override and live entry summary;
- signed offline manifest, local sequence sync, conflict handling;
- entry load/security/runbooks.

Exit: concurrent scanners admit once; revoked devices/tickets fail; peak reference
load meets objective; offline expiry/conflicts preserve evidence and dashboard truth.

## Milestone 9 — Realtime and Polling

The polling snapshot/delta path begins in Milestone 4. This milestone completes:

- one-time socket tickets and WebSocket protocol;
- Redis cross-replica fan-out and durable replay handoff;
- heartbeats, limits, backpressure, resync, graceful drain;
- client reference behavior for reconnect/poll fallback;
- realtime dashboards and outage/load tests.

Exit: lost/duplicate/gapped messages recover through replay/snapshot; slow consumers
are bounded; Redis restart preserves HTTP availability; connection/reconnect tests
meet capacity and delivery-lag budget.

## Milestone 10 — Refunds, Cancellation, and Reconciliation

Deliver:

- full and partial refund commands/workers/provider events;
- ticket revocation and optional inventory-return policy;
- unpaid order cancellation and session cancellation batch workflow;
- scheduled/on-demand payment reconciliation and discrepancy resolution;
- finance reports and runbooks.

Exit: concurrent refunds cannot exceed capture; provider failures resume; mass
cancellation is restartable; financial totals reconcile against sandbox/provider
fixtures; all operator resolutions are audited.

## Milestone 11 — Growth and Integrations

Deliver:

- promotions/redemption concurrency;
- waitlist join/leave/offer/expiry/conversion;
- outbound webhook endpoint/signing/delivery/retry/disable/replay;
- scoped public API credentials/quotas and integration documentation.

Exit: promo limits hold under contention; waitlist offers cannot oversell; webhook
SSRF/signature/retry tests pass; broken endpoints cannot exhaust critical workers.

## Milestone 12 — Reporting and Exports

Deliver:

- sales, inventory, entry and fulfilment summaries;
- incremental/rebuildable read models with freshness;
- asynchronous CSV/JSON export, signed download and expiry;
- reconciliation among report aggregates and authority.

Exit: totals match canonical test ledger; large exports stream within bounds; tenant
access and CSV injection tests pass; stale data is explicitly labeled.

## Milestone 13 — Production Hardening

Deliver:

- complete threat model/security/privacy/PCI review and remediation;
- full dashboards/alerts/runbooks/on-call exercises;
- load/soak/chaos tests, connection budgets and capacity plan;
- backup/PITR/object lifecycle and successful isolated restore drill;
- retention/deletion/export workflows;
- canary/rollback procedures and feature kill switches;
- accessibility/localization review of exposed contract semantics;
- provider production-readiness and quota checks.

Exit: launch security gate passes; SLOs hold at forecast load with headroom; restore
meets accepted RPO/RTO; incident drills and rollback succeed; no critical/high known
defect is waived without explicit executive/security acceptance.

## Milestone 14 — Pilot and General Availability

Pilot with a small set of organizations and controlled event sizes. Enable features
gradually, monitor error budgets, payment/fulfilment reconciliation, support volume,
entry behavior, and tenant feedback. Maintain daily launch reconciliation and defect
triage. GA only after pilot exit criteria and rollback window are met.

Exit: consecutive pilot events complete with no oversell/cross-tenant/security
incident, financial and entry reconciliation within tolerance, operational staff can
use runbooks, SLOs remain healthy, and remaining issues have accepted priority/owner.

## Dependency Chain

```text
runtime -> identity/tenancy -> catalog -> inventory/holds -> cart/orders
                                                    |             |
                                                    v             v
                                            realtime/polling -> payments
                                                                  |
                                                          tickets/delivery
                                                                  |
                                                                entry

payments + tickets -> refunds/reconciliation
core domain events  -> growth/webhooks/reporting
all milestones      -> hardening -> pilot -> GA
```

Some work may overlap only when contracts are stable: ticket rendering can begin
against order-confirmed fixtures; realtime protocol can begin after inventory event
schema; reporting projections can begin after canonical totals are fixed.

## Per-Slice Definition of Done

- Product behavior and non-goals documented.
- Permission/tenant/resource ownership enforced and tested.
- OpenAPI/event schemas and examples updated; compatibility check passes.
- Migration/query constraints/indexes and rollback/roll-forward reviewed.
- State transitions, transaction locks, idempotency, audit, outbox/jobs complete.
- Provider calls timed out/classified/retry-safe and outside transactions.
- Logs/metrics/traces/dashboard/alert and runbook impact supplied.
- Unit, integration, contract, concurrency, security, and failure tests appropriate
  to risk pass, including race/static/vulnerability checks.
- Docs/feature status/ADR updated and operator/customer implications communicated.
- Deployment and rollback are safe with adjacent released schema/code versions.

## Backlog Prioritization

Order implementation work by: correctness/security blockers, dependency enablers,
end-to-end customer value, operational risk reduction, measured performance need,
then convenience. Do not prioritize broad CRUD volume over closing a safe purchase-
to-entry journey. P2 work cannot displace an incomplete P0 invariant or runbook.

