# Decisions and Open Questions

This register prevents hidden assumptions. `Accepted default` decisions guide work
until an ADR intentionally changes them. Open questions need a named product,
security, finance, legal, or operations owner before the dependent milestone.

## Accepted Defaults

| ID | Decision | Reason / consequence |
|---|---|---|
| D-001 | Modular monolith with API, worker, and migrate processes | Preserves transactional simplicity and modular ownership; extraction requires evidence/ADR |
| D-002 | PostgreSQL is durable authority | Inventory, commerce, ticket, audit, replay, outbox, and jobs survive Redis/process loss |
| D-003 | Redis is ephemeral support only | Used for cache, rate limit, socket tickets, and fan-out; polling/DB truth remains available |
| D-004 | PostgreSQL outbox/jobs first | Avoid broker operations until measured scale/decoupling justifies it |
| D-005 | REST/JSON `/v1` plus WebSocket; conditional polling always available | Broad client compatibility and recoverable realtime behavior |
| D-006 | Provider-neutral OIDC, fixed initial roles with named permissions | Avoid custom password security and scattered role checks |
| D-007 | Stripe-first behind payment adapter | Fast first integration without coupling domain to provider |
| D-008 | Provider-hosted/tokenized payment collection | Keep raw payment instruments outside VenueOS/PCI boundary |
| D-009 | One event/session currency in Phase 1 | Makes pricing, refunds, reporting, and rounding tractable |
| D-010 | Integer minor-unit money and UTC storage + IANA venue timezone | Prevent floating/timezone ambiguity |
| D-011 | Default hold 10 minutes, maximum one renewal | Balanced starting policy; configurable only within safe limits |
| D-012 | Reserved seats use stable-order row locks; GA uses conditional counters | Simple database-enforced no-oversell algorithms |
| D-013 | Payment webhook/provider reconciliation is eventual truth | Redirect/client response cannot prove settlement |
| D-014 | Signed QR with server-side current-state check | Compact/tamper-resistant while preserving revocation and privacy |
| D-015 | Online-first scans with bounded opt-in offline manifests | Correct central truth with operational continuity |
| D-016 | Single primary write region at launch | Avoid multi-region consistency before product demand is proven |
| D-017 | Backward-compatible expand/migrate/contract schemas | Enables rolling deploy and safe rollback |
| D-018 | One session per cart/order in Phase 1 | Keeps holds, currency, cancellation, and ticket policies coherent |
| D-019 | WebSockets are never a source of truth | Snapshot + delta replay/polling recovers loss and supports simple clients |
| D-020 | Large exports/cancellations/delivery/provider work are asynchronous | Keeps requests bounded and restartable |

## Open Product Questions

| ID | Question | Why it matters / required by |
|---|---|---|
| Q-P01 | Exact launch venue types and maximum seat/GA capacity? | Map model, load tests, limits; before Milestone 3 |
| Q-P02 | Guest checkout required, customer accounts required, or both? | Identity/order access; before Milestone 5 |
| Q-P03 | Price display/tax/fee rules by launch jurisdiction? | Pricing/legal contract; before Milestone 5 |
| Q-P04 | Hold durations/renewal/customer quantity limits by event/channel? | Abuse and conversion policy; before Milestone 4 |
| Q-P05 | Does Phase 1 support authorization/capture-later or immediate capture only? | Payment/order transitions; before Milestone 6 |
| Q-P06 | Refund policy: fees/tax refundable, deadlines, ticket/inventory behavior? | Refund engine and public terms; before Milestone 10 |
| Q-P07 | Are transfers, name changes, or reserved-seat exchanges launch scope? | Ticket ownership and commerce complexity; roadmap decision |
| Q-P08 | Offline scanning mandatory for launch, and maximum offline duration/risk? | Device architecture/security; before Milestone 8 |
| Q-P09 | Waitlist fairness: FIFO, quantity fitting, tiers/VIP priority? | Offer algorithm; before Milestone 11 |
| Q-P10 | Which reports and accounting definitions are contractual? | Aggregates/exports/reconciliation; before Milestone 12 |
| Q-P11 | Required locales, accessibility claims, and receipt/ticket formats? | Content/template/API fields; before catalog/ticket delivery |
| Q-P12 | Is box-office cash/manual payment needed at launch? | Order/payment audit/reconciliation; before Milestone 5 |

## Open Technical and Operational Questions

| ID | Question | Decision owner / deadline |
|---|---|---|
| Q-T01 | Deployment target and managed-service choices? | Platform, Milestone 0 |
| Q-T02 | Identity provider, issuer/audiences, MFA/recent-auth capabilities? | Security/product, Milestone 0 |
| Q-T03 | Stripe direct vs Connect model and tenant onboarding/capabilities? | Finance/product, before Milestone 6 |
| Q-T04 | Email/SMS/object-storage/CDN vendors and regional availability? | Platform/product, before Milestone 7 |
| Q-T05 | PostgreSQL RLS enabled at launch or application scoping plus later defense? | Architecture/security, before Milestone 2 exits |
| Q-T06 | Public storefront/API domain and tenant resolution: slug, custom domain, header? | Product/security, before catalog public API |
| Q-T07 | Required RPO/RTO, maintenance windows, and availability exclusions? | Business/operations, before hardening |
| Q-T08 | Forecast peak sessions, hold/checkout QPS, sockets, scanners, and data retention volume? | Product/platform, before load signoff |
| Q-T09 | Observability/on-call vendors, paging ownership, and log retention budget? | Operations/security, Milestone 1 |
| Q-T10 | Privacy jurisdictions, financial/audit retention, data residency, subprocessors? | Legal/security, before production data |
| Q-T11 | Supported client/version/deprecation policy? | Product/engineering, before public beta |
| Q-T12 | Fraud/risk/dispute/chargeback ownership and provider features? | Finance/support/security, before paid pilot |

## ADR Process

Create `docs/venueos/adr/NNNN-short-title.md` with:

```text
Title and status
Date, owners, decision deadline
Context and forces
Options considered
Decision
Consequences and risks
Migration/rollback
Operational/security impact
Links to evidence and superseded ADRs
```

Use status `proposed`, `accepted`, `superseded`, or `rejected`. Do not edit history
to hide an old decision; supersede it. Small implementation details do not need an
ADR, but changes to authority, consistency, tenancy, external contracts, providers,
deployment topology, or launch scope do.

## Decisions Required Before Coding a Feature

If an open answer changes schema, security boundary, money movement, legal promise,
or operational architecture, stop at interface/prototype work until the authorized
owner decides. Record temporary assumptions explicitly in the issue/ADR and do not
let an experimental default silently become production policy.

