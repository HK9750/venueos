# Feature Catalog

This catalog enumerates customer-visible and operational backend capabilities. Each
feature must be delivered as a vertical slice: authorization, validation, data,
contract, events, audit, telemetry, tests, and runbook behavior are part of the
feature, not later polish.

Priorities: **P0** is required for safe launch, **P1** completes the expected
product, and **P2** is a planned extension. Status begins as `planned`; it changes
only when the exit criteria in the roadmap are evidenced.

## Platform and Organization

| ID | Pri | Feature | Detailed acceptance summary |
|---|---:|---|---|
| PLT-001 | P0 | Service health | Separate liveness, readiness, startup, build-info, metrics, and optional pprof endpoints; readiness reflects required dependencies without exposing secrets |
| PLT-002 | P0 | Organization lifecycle | Create, read, update profile/settings, suspend, and reactivate; suspended tenants cannot mutate/sell but retain auditable read access for authorized support |
| PLT-003 | P0 | Memberships | Invite, accept, list, change role, revoke, and expire invitations; email matching and one-time acceptance are enforced |
| PLT-004 | P0 | Role permissions | Owner, admin, box office, door manager, scanner, finance, support, and read-only roles map to named permissions; authorization tests cover every command |
| PLT-005 | P0 | Tenant isolation | Organization scope is derived from identity, applied in repository queries, and optionally reinforced by PostgreSQL RLS; negative cross-tenant tests are mandatory |
| PLT-006 | P1 | API credentials | Create scoped organization API keys, show plaintext once, hash at rest, rotate/revoke, record last use, and rate-limit by credential |
| PLT-007 | P1 | Staff audit explorer | Filter immutable audit entries by actor, target, action, request, time, and result; sensitive values remain redacted |
| PLT-008 | P1 | Organization settings | Default locale, timezone, currency, receipt sender, hold policy, refund policy, and notification preferences with validated overrides |

## Venues, Maps, and Catalog

| ID | Pri | Feature | Detailed acceptance summary |
|---|---:|---|---|
| CAT-001 | P0 | Venue management | CRUD venue profile, address, IANA timezone, accessibility details, contact data, and status; archive is blocked while future sessions depend on venue |
| CAT-002 | P0 | Space management | CRUD halls/rooms/areas, capacity, entry instructions, default map, and operational status within a venue |
| CAT-003 | P0 | Seat-map drafts | Create rows/sections/seats/labels/accessibility/companion/obstructed-view metadata and geometry in a draft revision |
| CAT-004 | P0 | Seat-map validation | Detect duplicate labels, disconnected references, invalid coordinates, capacity mismatch, inaccessible companion rules, and seats missing price/category mapping |
| CAT-005 | P0 | Seat-map publication | Publish immutable version; sessions reference a version, later edits create a new draft/version, and used versions are never silently changed |
| CAT-006 | P0 | GA capacity | Configure one or more named capacity pools and sellable/held/sold/killed counters per session; capacity cannot become lower than committed quantity |
| CAT-007 | P0 | Event management | Draft/update event identity, description, media references, policies, tags, age/accessibility information, and status |
| CAT-008 | P0 | Session scheduling | Create scheduled occurrences with venue/space/map, doors/start/end times, sales windows, capacity, channel allocation, and cancellation status |
| CAT-009 | P0 | Publication validation | Return machine-readable blocking errors and warnings for map, price, time, provider, policy, inventory, and channel readiness |
| CAT-010 | P0 | Publish/unpublish | Publish valid event/session revisions; unpublish stops new sales without corrupting active holds or historical orders |
| CAT-011 | P0 | Price tiers | Define minor-unit base price, currency, included/excluded fees/tax rules, seat/section/pool applicability, sales window, and channel visibility |
| CAT-012 | P1 | Inventory kills/comps | Authorized staff can remove inventory from sale or allocate complimentary entitlements with a reason and audit trail |
| CAT-013 | P1 | Sales channels | Configure public, box-office, partner, or hidden channels and optional allocations; channel totals never exceed physical capacity |
| CAT-014 | P1 | Event discovery | Public list/detail by slug, time, venue, tag, and status with stable cursor pagination and cache validators |

## Availability, Holds, and Checkout

| ID | Pri | Feature | Detailed acceptance summary |
|---|---:|---|---|
| BKG-001 | P0 | Availability snapshot | Return authoritative seat/pool state, price references, server time, snapshot version/cursor, and cache validator; never expose another customer's hold identity |
| BKG-002 | P0 | Reserved-seat hold | Atomically hold all requested available seats in stable lock order or fail the complete request; return conflict details safe for the customer |
| BKG-003 | P0 | GA hold | Atomically reserve quantity only when remaining capacity is sufficient; concurrent requests cannot oversell |
| BKG-004 | P0 | Hold expiry | Expire on read/command and by worker; release inventory once; publish availability change; tolerate worker delay without accepting an expired hold |
| BKG-005 | P0 | Hold ownership | Bind hold to anonymous checkout token or authenticated customer; prevent enumeration and unauthorized mutation |
| BKG-006 | P1 | Hold modification | Add/remove inventory atomically using version precondition; preserve original expiry unless policy explicitly extends it |
| BKG-007 | P1 | Hold renewal | Permit at most the configured renewal count under policy, contention, and sales-window limits; record audit/reason |
| BKG-008 | P0 | Cart pricing | Server computes line prices, fees, discounts, taxes, and total from immutable configuration references; client totals are advisory only |
| BKG-009 | P0 | Checkout validation | Validate hold state/version, sales window, quantity limits, customer fields, consent/policies, currency, promo, and provider readiness before order creation |
| BKG-010 | P0 | Idempotent checkout | Same organization/key/fingerprint replays the original response; reuse with different input returns a deterministic conflict |
| BKG-011 | P1 | Assisted box office | Staff may create customer/guest orders, select approved payment method, comp items, and print/resend tickets under explicit permissions |

## Orders, Payments, Refunds, and Reconciliation

| ID | Pri | Feature | Detailed acceptance summary |
|---|---:|---|---|
| COM-001 | P0 | Order creation | Create immutable price snapshot and order number from a valid hold; inventory is linked exactly once and order state is explicit |
| COM-002 | P0 | Payment intent | Create/reuse provider intent outside locks with provider idempotency; persist attempt/status safely across timeout and callback races |
| COM-003 | P0 | Payment webhook | Verify signature/tolerance, store inbox record, acknowledge quickly, and process duplicate/out-of-order events idempotently |
| COM-004 | P0 | Order confirmation | Confirm once after valid capture/authorization policy, consume hold, mark sold inventory, enqueue ticket issue and receipt, and emit event atomically |
| COM-005 | P0 | Payment failure | Record classified failure and allow safe retry while hold/policy permits; never leak provider internals to public errors |
| COM-006 | P0 | Payment/hold race | If payment succeeds after expiry, recover inventory if still safe or enter a visible manual-reconciliation state; never silently lose money or oversell |
| COM-007 | P0 | Order lookup | Authorized staff search by order number, customer, provider reference, event/session, status, and time; customers retrieve only with account or signed access token |
| COM-008 | P0 | Full refund | Validate refundable balance/policy, create refund request idempotently, call provider asynchronously, update order/tickets on confirmed outcome |
| COM-009 | P1 | Partial refund | Select amounts/line entitlements within captured-minus-refunded value; define inventory return and ticket revocation per line |
| COM-010 | P1 | Cancellation | Cancel unpaid/stale orders and release inventory; event/session cancellation creates affected-order workflow rather than synchronous mass refund |
| COM-011 | P1 | Reconciliation | Compare internal captures/refunds with provider balance transactions, classify discrepancies, expose report, and retain evidence |
| COM-012 | P2 | Simple exchange | Replace eligible entitlements through an atomic reserve/price-difference workflow; excluded from first launch unless tightly scoped |

## Tickets, Delivery, and Entry

| ID | Pri | Feature | Detailed acceptance summary |
|---|---:|---|---|
| TKT-001 | P0 | Ticket issuance | Create one entitlement/ticket per seat or GA unit after confirmation; issuance is idempotent and records signing key version |
| TKT-002 | P0 | QR credential | Sign opaque compact payload containing non-sensitive ticket reference, version, and expiry/context; scanners reject tampering and revoked keys |
| TKT-003 | P0 | Ticket retrieval | Customer access is scoped by account or signed order token; staff requires permission; response supports wallet/PDF references without exposing signing secret |
| TKT-004 | P0 | Email receipt/ticket | Queue templated, locale-aware delivery; record attempts/provider IDs; retry transient failures and expose resend command |
| TKT-005 | P1 | SMS delivery | Optional phone-verified delivery with consent, rate limits, concise secure link, cost telemetry, and regional enablement |
| TKT-006 | P1 | Ticket void/reissue | Authorized command revokes prior credential, increments ticket version, reissues, emits event, and leaves immutable audit history |
| ENT-001 | P0 | Online check-in | Validate device/session, signature, ticket/order/session status, entry window, and prior admission; write immutable attempt and ticket status atomically |
| ENT-002 | P0 | Duplicate handling | Return stable already-used result with original scan time/gate without creating another admission; every attempt remains auditable |
| ENT-003 | P1 | Scan override | Door manager may admit/reject with reason under permission; override is visually distinct and fully audited |
| ENT-004 | P1 | Device enrollment | Issue/rotate/revoke device credentials scoped to organization/venue/session; retain last seen, app version, and key metadata |
| ENT-005 | P1 | Offline scanning | Download signed, bounded session manifest and revocation set; device records offline sequence; sync detects conflicts and never erases evidence |
| ENT-006 | P1 | Entry dashboard | Live/pollable totals by session, gate, result, minute, device, and ticket type with data-delay indicator |

## Growth, Reporting, and Integrations

| ID | Pri | Feature | Detailed acceptance summary |
|---|---:|---|---|
| GRW-001 | P1 | Promotion codes | Configure scope, amount/percent, validity, redemption limit, per-customer rules, minimums, and combinability; redemption is transactional |
| GRW-002 | P1 | Waitlist join | Join verified contact to sold-out session with consent and deduplication; allow leave/expiry and retain privacy controls |
| GRW-003 | P1 | Waitlist offer | When inventory becomes eligible, create limited-time offer/hold for next candidate, notify asynchronously, and advance on expiry |
| RPT-001 | P1 | Sales summary | Aggregate gross, discounts, tax, fees, captured, refunded, net, quantity, and order count by event/session/channel/tier and time |
| RPT-002 | P1 | Inventory report | Show configured, killed, held, sold, available, and comped units with snapshot timestamp and invariant checks |
| RPT-003 | P1 | Entry report | Show issued, valid, void, admitted, duplicate, rejected, and override counts by session/gate/type |
| RPT-004 | P1 | Asynchronous export | Request CSV/JSON export from a bounded filter, track status, store encrypted artifact, return expiring signed download, and expire artifact by policy |
| INT-001 | P1 | Outbound webhooks | Tenant-configured HTTPS endpoints, event selection, signing secret, delivery attempts, exponential retry, disable policy, replay, and logs |
| INT-002 | P1 | Provider connections | Connect/disconnect payment, email, SMS, and storage adapters; secrets are encrypted/referenced and connectivity is health-tested |
| INT-003 | P1 | Public API | Scoped API keys/OAuth clients, documented versions, quotas, idempotent writes, request IDs, and deprecation policy |

## Universal Acceptance Checklist

For every row above, implementation review answers all of the following:

- Who can invoke it, in which organization, against which resource state?
- What inputs, size limits, time limits, and semantic combinations are valid?
- What is the state machine and which transitions are forbidden?
- Which unique/check/foreign-key constraints defend the invariant?
- What happens on retry, duplicate, timeout, crash, stale version, or contention?
- Which audit entry and domain/outbox event are produced?
- What does the caller receive, and which stable error code applies?
- Does a snapshot/polling/realtime consumer observe the change?
- Which metrics, logs, traces, dashboard, alert, and runbook cover it?
- Which unit, integration, contract, concurrency, and recovery tests prove it?
- How is it migrated, rolled back, retained, exported, and deleted?

