# Product, Personas, and Workflows

## Product Objective

VenueOS lets an organization configure places and sell access to time-bound
sessions with either assigned seats or general-admission capacity. It covers the
full operational chain: publish inventory, reserve it safely, collect payment,
issue admission credentials, scan guests, resolve order problems, and reconcile
provider activity.

The first release is a multi-tenant SaaS backend serving an operator console,
public storefronts, scanner applications, support tools, and integration clients.
It targets a single primary region initially, while keeping currency, locale,
timezone, provider, and storage boundaries explicit enough for later expansion.

## Personas and Permissions

| Persona | Primary goals | Default access |
|---|---|---|
| Platform operator | Operate the SaaS, investigate tenant/system failures | Platform-scoped support tools; no routine access to tenant secrets |
| Organization owner | Configure organization, billing/provider connections, and administrators | Full organization administration |
| Venue administrator | Configure venues, spaces, maps, sessions, inventory, channels, and reports | Venue and catalog management within assigned organization |
| Box-office agent | Create assisted orders, resend tickets, refund within policy, find customers | Operational sales and order permissions |
| Door manager | Configure devices, view entry totals, authorize scan overrides | Check-in administration |
| Scanner operator | Validate and admit tickets | Minimal session/device-scoped scan permission |
| Finance analyst | Review payments, refunds, fees, reconciliation, and exports | Read finance data and initiate approved exports |
| Support agent | Search and diagnose orders; perform explicitly granted remediations | Read-biased, audited support role |
| Customer | Browse, reserve, pay, receive and manage tickets | Public/session scope plus own order access token/account |
| Integration client | Synchronize catalog or receive events | Explicit API-key scopes or signed webhook subscription |

Roles are organization memberships. Fine-grained permissions are stable strings
checked by application policy; clients never gain authority by submitting a role
or `organization_id` in a payload.

## Core Customer Journey

1. Customer opens a published event/session page.
2. Client retrieves a versioned availability snapshot.
3. Customer selects seats or quantity and creates a short-lived hold.
4. Backend atomically reserves inventory and returns hold version and expiry.
5. Client creates/updates a server-priced cart and begins checkout.
6. Backend validates the hold, customer contact, price, promo, fees, tax, and terms.
7. Backend creates an order/payment attempt using an idempotency key.
8. Provider confirms payment asynchronously; inbox processing confirms the order.
9. Worker issues tickets and dispatches email/SMS; client can poll throughout.
10. Scanner validates signature, session/device authorization, status, and prior use.
11. Operations may refund, void, exchange, resend, or investigate with audit trail.

Every step must survive retries, duplicate requests, stale browser state, worker
restarts, delayed webhooks, and a temporarily unavailable Redis instance.

## Core Operator Journey

1. Owner creates an organization and invites staff.
2. Administrator creates a venue, one or more spaces, access gates, and a seat map
   or GA capacity configuration.
3. Administrator creates an event, price tiers, sales windows, policy text, tax and
   fee configuration, then schedules one or more sessions.
4. A publication validation report identifies missing/invalid configuration.
5. Publishing freezes the sellable session revision and exposes it to configured
   channels.
6. Live operations watch inventory, sales, payment, delivery, and entry metrics.
7. Support resolves customer issues through audited commands.
8. Finance reconciles captured/refunded totals against provider transactions and
   exports organization-local reporting.

## Terminology

- **Organization:** tenant and top-level authorization/billing boundary.
- **Venue:** physical or virtual location owned by an organization.
- **Space:** independently bookable area inside a venue.
- **Seat map:** versioned geometry and sellable seat topology for a space.
- **Event:** customer-facing production or activity definition.
- **Session:** a scheduled occurrence of an event in a space.
- **Inventory unit:** a reserved seat or an amount of a GA pool for one session.
- **Hold:** expiring exclusive claim on inventory before order confirmation.
- **Cart:** server-priced checkout intent associated with a hold.
- **Order:** commercial record of the purchase, regardless of payment outcome.
- **Payment attempt:** one provider interaction for an order.
- **Ticket:** revocable admission credential for one entitlement.
- **Check-in:** immutable entry attempt/result associated with a ticket and device.
- **Channel:** storefront, box office, partner, or other inventory distribution path.

## Scope Boundaries

Phase 1 includes hosted/public API support for single-session purchases, one
currency per event, assigned seating and GA, Stripe-compatible payment abstraction,
QR tickets, online-first scanning with controlled offline mode, refunds, basic
promotions/waitlists, organization reports, and outbound webhooks.

Not in initial launch unless promoted through an ADR and roadmap change:
subscriptions, season passes, marketplace resale, multi-currency orders, complex
reserved-seat exchanges, dynamic pricing optimization, full accounting ledger,
multi-region active/active writes, arbitrary workflow scripting, white-label native
mobile apps, or a general event-streaming platform.

## Success Measures

- No confirmed oversells and no cross-tenant data disclosure.
- At-least-once client/provider delivery never creates duplicate orders, captures,
  refunds, tickets, or admissions.
- Published availability remains understandable under contention and reconnects.
- p95 availability reads stay below 250 ms and hold creation below 500 ms excluding
  provider latency at the agreed reference load.
- Monthly read availability reaches 99.95%; booking writes reach 99.9%.
- Every financial or admission mutation is attributable and auditable.
- Operators can detect and remediate stuck jobs, provider lag, ticket delivery
  failures, and reconciliation discrepancies without direct database edits.

