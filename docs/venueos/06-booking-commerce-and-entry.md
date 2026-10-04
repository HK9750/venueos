# Booking, Commerce, Ticketing, and Entry

## Availability

Availability is a projection of authoritative PostgreSQL inventory. For reserved
seating each session-seat is `available`, `held`, `sold`, or `killed`. For GA,
availability is calculated from capacity counters under the invariant in the
catalog document.

Snapshots include session ID, inventory revision, server time, hold-policy data,
seat/pool state, applicable public price references, and next polling guidance.
Public snapshots collapse another customer's `held` and non-sellable details to
safe states; they never reveal hold/customer identity.

Implementation status: PostgreSQL now has session inventory revisions, materialized
GA pool counters created from active space templates, token-hash-scoped hold
records/items, atomic GA hold/release, and a bounded due-hold expiry transaction that
releases counters and emits audit/outbox records atomically. Assigned-seat sessions
also materialize immutable seat metadata from their published map. The repository
now exposes repeatable-read availability snapshots and bounded replay reads, and
release commands enforce expiry using database time when the worker has not run yet.
Authenticated tenant-scoped staff snapshot/replay routes are wired; public tenant
resolution and storefront routes remain pending.

## Holds

### State Machine

```text
active -> confirmed
  |  \-> released
  +----> expired
  +----> cancelled
```

`active` is the only mutable state. `confirmed`, `released`, `expired`, and
`cancelled` are terminal. A hold contains organization, session, owner token/user,
channel, currency, price revision, expiry, renewal count, version, items, and the
price/inventory snapshot used when created.

Default policy is a 10-minute hold and at most one controlled renewal. Organization
settings may shorten it within platform limits. Effective expiry is always enforced
at command time using database time; the expiration worker improves release latency
but is not correctness authority.

The repository now supports one owner- and version-checked renewal. Renewal uses
database time, refuses expired or terminal holds, advances the hold version, and
records audit/outbox entries atomically without changing inventory revision.

The repository also supports owner- and version-checked modification. A
modification atomically replaces the hold's GA quantity or reserved-seat set,
preserves the original expiry, advances the hold version, increments the session
inventory revision once, and records one `hold.modified` audit/outbox mutation plus
one availability replay event. Requested reserved seats are locked in stable ID
order; a failed replacement rolls back the release of the previous inventory.
Public checkout/guest HTTP routes remain gated on the open identity, tenant
resolution, hold-policy, and pricing decisions.

### Reserved-Seat Allocation

Within a short transaction:

1. Sort unique requested seat IDs by stable binary/database order.
2. Lock all session-seat rows with `SELECT ... FOR UPDATE` in that order.
3. Confirm every row belongs to organization/session and is sellable/available;
   lazily transition an expired held row when safe.
4. Validate tier/channel/quantity/customer restrictions.
5. Insert hold and items, set rows to held with hold ID/expiry, increment inventory
   revision, insert audit/outbox/idempotency result.
6. Commit all or none.

No partial seat holds. Deadlock/serialization retry wraps only this database closure
and has a low finite attempt limit.

### GA Allocation

Use one conditional update per pool, inside the hold transaction:

```sql
UPDATE session_ga_pools
SET held_qty = held_qty + $quantity, version = version + 1
WHERE id = $pool
  AND organization_id = $organization
  AND sellable_capacity - sold_qty - held_qty - killed_qty - comped_qty >= $quantity;
```

Zero updated rows means conflict/insufficient inventory. Expired GA hold release
atomically decrements held quantity exactly once.

### Hold Mutation

Modification requires `If-Match`/expected version. The transaction locks the hold,
validates owner/state/expiry, releases removed items, allocates added items, updates
price snapshot and version, and emits one aggregate availability change. Expiry is
not extended by normal edits. Renewal is a separate command checking renewal count,
sales window, checkout/payment state, and maximum lifetime.

## Cart and Pricing

A cart is server-side, belongs to one organization/session/currency and one active
hold, and contains customer/contact details, promotion selection, consent/policy
versions, and calculated lines. Phase 1 rejects multi-session carts.

Price calculation order is explicit and versioned:

1. quantity × base unit price;
2. eligible item/order discount allocation;
3. configured fees (included/excluded semantics);
4. tax by configured rule and taxable components;
5. rounding and reconciliation adjustment, if required;
6. total = subtotal - discount + fees + tax.

The algorithm returns a component-level explanation and snapshots it on checkout.
Promo codes, client totals, labels, or cached prices never override server pricing.

`pricing.CalculateQuote` now implements the provider-neutral arithmetic boundary:
it extends immutable line price snapshots with checked integer multiplication,
normalizes line/component order, applies an explicit discount, then explicit fees
and taxes, and returns a SHA-256 snapshot digest. It does not select tax rates,
fee semantics, promotions, or rounding policy; those inputs remain owned by the
event/jurisdiction policy layer and must be supplied explicitly.

The durable `internal/cart` slice now creates and owner-reads one active cart per
hold under organization/session scope, locks the active hold with database time,
stores only a PII-free quote snapshot and digest, and updates quotes with an
expected-version check. Create/update mutations append audit and outbox records in
the same transaction. The durable `internal/order` slice now locks that cart and
hold, validates database-time expiry and currency, copies immutable quote lines,
checks the cart out, and records the order/cart audit and outbox events in one
serializable transaction. Guest/customer contact, promotion selection, provider
execution, and confirmation remain intentionally separate while Q-P02/Q-P03 and
the provider/capture decisions remain open.
The JSONB quote remains available for bounded inspection, but the repositories
also persist the exact raw snapshot bytes used for SHA-256 so JSONB normalization
cannot invalidate a cart/order integrity check.

## Checkout and Orders

### Checkout Sequence

1. Claim idempotency `(organization, operation, key)` and compare request hash.
2. Load cart/hold under owner and organization scope.
3. Validate active/unexpired hold, expected versions, sales window, limits, customer
   fields, currency, policy consent, promo availability, and provider connection.
4. In a short transaction create an order in `payment_pending`, copy immutable
   item/price/customer/policy snapshots, link the hold, and commit outbox/audit.
5. Outside the transaction create/reuse the provider payment intent with VenueOS
   order ID and the same idempotency lineage.
6. Persist provider reference/client action/status in another short transaction.
7. Return the stable order/checkout result; later provider webhook is authoritative.

If the process times out after a provider call, retry queries/reuses the provider
idempotency result. It never blindly creates a second intent.

### Order State Machine

```text
draft -> payment_pending -> confirmed -> partially_refunded -> refunded
   |            |              |  \-> fulfilment_issue
   |            +-> payment_failed
   +------------+-> cancelled
                 \-> reconciliation_required
```

Use explicit transition functions. `confirmed` means payment requirements and
inventory confirmation succeeded; ticket delivery may still be pending. Failed
delivery is not payment failure. `reconciliation_required` is visible to operators
and blocks unsafe automatic guesses.

Order numbers are human-readable, tenant-unique, non-enumerable enough for support,
but never serve as sole customer authorization. Database IDs remain opaque UUIDs.

The provider-neutral `internal/order` package now encodes the documented order
and payment transition tables. It accepts same-state delivery as a no-op for
webhook idempotency and rejects illegal rollback from terminal/financial states.
The durable order creation transaction now locks the tenant-scoped cart and hold,
appends order and cart audit/outbox results, and stores immutable order lines. It
does not call a remote provider or consume inventory; those actions remain in the
confirmation workflow and must be added with the documented stable lock order.

## Payments

Payment attempts have states `created`, `requires_action`, `processing`,
`authorized`, `captured`, `failed`, `cancelled`, and `unknown`. Store provider,
provider object ID, amount/currency, error classification, idempotency reference,
timestamps, and sanitized provider metadata.

Raw card/bank details never pass through or persist in VenueOS. The provider's
hosted/tokenized component handles payment instruments.

The provider-neutral `internal/payment` boundary now validates positive integer
minor-unit intent/refund requests and bounded idempotency/provider references. Its
adapter returns sanitized intent facts and verified webhook metadata with payload
hash/reference for `internal/platform/inbox`; raw provider bodies and payment
instruments remain outside the domain. Migration `00022_create_orders.sql` adds
tenant-scoped orders, immutable order lines, and payment-attempt rows with bounded
provider references, status/failure checks, and sanitized metadata. Capture mode
and tenant account selection remain open decisions.

The durable refund request boundary now locks the order and captured payment
attempt, counts pending and successful refunds, applies `refund.ValidateAmount`
under that lock, and records a `requested` refund plus audit/outbox mutation in
one transaction. The execution repository transitions `requested -> processing`
under the same durable authority, then the provider-neutral execution service
calls the injected adapter outside the transaction with the stable refund
idempotency key and finalizes verified success/failure states. Transient and
unknown provider failures remain retryable for reconciliation; it stores no raw
provider response. The protected `POST
/v1/organizations/{organization_id}/orders/{order_id}/refunds` transport now
requires `order.refund`, derives tenant and actor scope from verified
authorization, and accepts a required idempotency key. Repeating the same key
and request details returns the original durable refund without another
audit/outbox mutation; reusing it with different details is a conflict.

### Webhook Processing

1. Read raw request under strict size/time limit.
2. Verify provider signature and timestamp tolerance.
3. Insert provider/event ID plus payload reference/hash into inbox under unique
   constraint; duplicate returns success.
4. Respond quickly after durable acceptance.
5. Worker locks inbox event, loads payment/order, applies monotonic/idempotent
   transition, and records audit/outbox.
6. Unsupported/stale events are recorded as ignored with reason, not retried forever.

Event ordering cannot be assumed. When an event is ambiguous, fetch provider state
through a rate-limited reconciliation job and apply a transition from authoritative
facts.

### Payment Versus Hold Expiry

The confirmation transaction locks the order, hold, and affected inventory in a
documented stable order.

- If hold is active: consume it, move inventory held→sold, confirm order.
- If hold expired but inventory has not been reallocated: recover/confirm under the
  same lock and record `late_payment_recovered`.
- If any inventory was reallocated: mark `reconciliation_required`; attempt
  provider void/refund asynchronously according to payment state, alert operators,
  and never issue tickets for unavailable inventory.

## Refunds and Cancellation

Refund request states: `requested`, `processing`, `succeeded`, `failed`, `cancelled`,
`reconciliation_required`. The request records requested minor units, line
allocation, reason, actor, policy result, provider reference, and idempotency key.

Rules:

- cumulative successful/pending refunds cannot exceed captured amount;
- currency must match order;
- concurrency is guarded by order/refund row locks and database constraints;
- provider call occurs in a worker outside the initiating transaction;
- provider webhook/reconciliation may finalize result;
- ticket revocation and inventory return happen according to explicit line/session
  policy only after the financially appropriate state;
- a refund can succeed even if customer notification fails; delivery retries remain
  separate.

The provider-neutral `internal/refund` package now enforces currency matching,
positive requested amounts, and the invariant that pending plus successful
refunds cannot exceed captured value. Its lifecycle transition helper accepts
same-state replay and rejects terminal rollback. The strict `refund.execute` job
payload and worker adapter preserve tenant scope and provider failure classes;
refund policy, ticket behavior, reconciliation, and concrete provider wiring
remain application decisions.

Cancelling a session creates a durable batch workflow that freezes sales, selects
affected orders in pages, creates idempotent refund/notification tasks, tracks
progress, and supports resume. It never performs thousands of provider calls in an
HTTP request.

## Tickets

One confirmed entitlement creates one ticket. Reserved-seat entitlement points to
the exact session-seat; GA creates discrete admission units so scan/revoke status is
unambiguous.

Ticket states: `pending`, `valid`, `admitted`, `void`, `refunded`, `expired`. A
ticket version increments on reissue. QR content is a signed opaque payload with
ticket ID/reference, version, key ID, and limited context; it contains no customer
PII or price. Verification checks algorithm allowlist, signature, key state,
ticket/version, session, validity window, and server-side current status.

The provider-neutral `internal/ticket` package implements the first part of this
contract as a bounded `vos-ticket.v1` credential. It signs only the ticket ID,
session ID, ticket version, key ID, algorithm/version, and validity window with
Ed25519. Active and verify-only keys can verify during rotation; revoked keys are
rejected. The verifier does not decide ticket status or admission: the scan
transaction must still load and lock the current organization-scoped ticket.

Issuance is an idempotent worker keyed by entitlement. The current
`internal/ticket` application service locks that entitlement, creates the durable
ticket exactly once, signs the bounded credential, and returns the existing ticket
on retry without duplicating audit/outbox evidence. Its strict version-one job
payload carries only immutable tenant/order/session facts and is checked against
the leased job tenant before execution. Rendering PDF/wallet assets is
separate from credential issuance. Asset failure leaves a valid ticket retrievable
as data and visible as a fulfilment issue; signer/KMS registration and delivery
adapters remain separate.

Delivery has independent attempts by channel. Each attempt stores template/version,
recipient hash/redacted address, locale, provider ID, status, classified error, and
next retry. Resend creates a new attempt subject to rate limit; it does not create a
new ticket unless a revoke/reissue command was explicitly used.

## Check-In

### Online Scan Algorithm

1. Authenticate device and verify organization/venue/session/gate assignment.
2. Parse QR under strict length/format bounds and verify signature/key.
3. In a transaction load and lock current ticket under tenant/session scope.
4. Evaluate order/ticket/session status, entry window, and prior admission.
5. Insert immutable scan attempt for every result.
6. For valid first use, set ticket admitted and create admission record atomically.
7. Commit, emit entry event, and return a stable result code plus safe display data.

The provider-neutral `internal/entry` decision boundary now implements the
post-verification checks for ticket identity, session, credential version, entry
window, and current mutable status. It returns only the documented safe result
codes and never mutates state. Its application service requires a verified device
principal and `entry.scan` permission, verifies the signed credential through the
ticket adapter, hashes the raw credential, and derives actor/device scope from
authorization context before calling the repository. The database scan
transaction remains responsible for recording every attempt and resolving
concurrent first admissions. The `internal/entry/postgres` adapter now locks an
organization-scoped active device, requires an active session/gate assignment for
the scan scope, and then locks the ticket, inserts the scan attempt, and atomically
creates the unique admission with audit/outbox events. Duplicate device scan IDs
replay only the original result. The tenant device-management slice now supports
one-time credential enrollment plus versioned suspend/reactivate/revoke commands,
and session/gate assignment with capability and validity bounds. Migration `00027`
now provides durable public ticket-key resolution, and `POST /v1/device/scans`
exposes the authenticated scan application service with a safe result contract.
Worker/signer wiring, private signing-key registration, and delivery remain
separate adapter-backed work; offline manifests remain deferred.

Result codes include `admitted`, `already_admitted`, `invalid_signature`,
`unknown_ticket`, `wrong_session`, `outside_entry_window`, `void`, `refunded`,
`expired`, `device_not_authorized`, and `override_required`. Scanner UI derives
color/sound from code but does not decide validity.

Duplicate requests with the same device scan ID replay the original result. Two
devices racing on the same ticket yield one admission and one already-admitted
result, never two admissions.

Staff entry operations now have a bounded tenant-scoped summary endpoint. It
aggregates immutable scan attempts by result, gate, minute, and price-tier
ticket type for an explicit window of at most 31 days. The response includes the
database generation time, latest received scan time, and non-negative data-delay
indicator so dashboards cannot present stale totals as real time. It is a read
projection only; scan attempts and admissions remain the authority.

### Offline Mode

Offline operation is opt-in per organization/session. Device downloads a signed,
encrypted-at-rest manifest bounded to assigned session and expiry. It includes
credential verification data and revocation epoch without unnecessary PII.

Device records monotonically sequenced, signed local scan outcomes. Sync uploads in
batches with device/manifest/sequence IDs. Server preserves all attempts, assigns
canonical results, and flags conflicts such as two offline first-admissions. Conflict
policy affects reporting/manual review; evidence is never overwritten. Offline
mode displays staleness and stops after manifest expiry.

## Promotions

Promotion configuration includes normalized code, type (`fixed`/`percent`), value,
currency where applicable, event/session/tier/channel scope, start/end, total and
per-customer limits, minimum quantity/value, eligible inventory, combinability,
status, and version.

Redemption eligibility is checked during pricing and reserved transactionally at
checkout/confirmation so concurrent use cannot exceed limit. Abandoned/failed
orders release a reservation according to policy. Orders retain discount snapshot
and promo reference; later promo edits do not change them.

## Waitlists

Waitlist entries contain session, verified/normalized contact, requested quantity,
consent/version, status, position metadata, and expiry. States are `waiting`,
`offered`, `converted`, `expired`, `left`, `blocked`.

On eligible released inventory, a worker locks the queue in stable order, selects a
candidate, creates an offer with short expiry and optionally a restricted hold,
marks entry offered, and sends notification. Expired offers release inventory and
advance. Duplicate contact/session entries are prevented. Rate limits and privacy
deletion apply; position is approximate under concurrency and must be described as
such.

## Critical Tests

- Hundreds of concurrent attempts for the same seat/last GA unit produce no
  oversell and no deadlock storm.
- Hold create/modify/release/expire/confirm retries are exactly-once in effect.
- Database clock boundary at expiry is deterministic.
- Checkout timeout before/after provider creation reuses one payment intent.
- Duplicate and out-of-order provider events converge to correct order state.
- Payment-after-expiry exercises recoverable and reallocated branches.
- Concurrent partial refunds never exceed captured total.
- Ticket issue/render/delivery retries do not duplicate credentials.
- Concurrent scans admit once; offline conflicts preserve both attempts.
- Promo final-redemption race and waitlist offer race respect limits/order.
