# Venues, Events, and Catalog

## Venue and Space Model

A venue is an organization-owned location with display name, slug, status, address,
geolocation (optional), IANA timezone, contacts, accessibility notes, policies, and
public metadata. A venue contains spaces such as halls, screens, rooms, fields, or
zones. A space defines operational capacity, entry guidance, supported inventory
mode, and published seat-map versions.

Venue/space states are `draft`, `active`, `inactive`, `archived`. Archive is a
soft lifecycle transition. It is rejected when future published sessions still
reference the resource. Historical orders continue resolving archived resources.

Commands:

- create/update/archive/restore venue;
- create/update/archive space;
- manage gates/entry points and accessibility metadata;
- assign default seat-map version or GA pools;
- validate whether a space is schedulable.

Names need not be globally unique; slugs are unique within organization. All public
lookups still resolve through organization/channel context.

## Seat Maps

### Representation

A seat map consists of a version header and structural elements:

- sections and optional subsections;
- rows, seats, and stable external labels;
- geometry for client rendering (coordinates, shapes, labels, transforms);
- categories/tags used for pricing and accessibility;
- wheelchair spaces and companion relationships;
- obstructed-view/restricted/sellable flags;
- entry/gate hints;
- checksum and normalized topology metadata.

Geometry is not inventory state. A seat receives session-specific inventory only
when a session is materialized from a published map version.

General-admission sessions materialize one durable session pool for every active
pool template in the space. The copied physical and sellable capacities become the
session's inventory authority; later template edits do not change an existing
session.

### Version Lifecycle

`draft -> validating -> published -> retired`

- Drafts may be edited with optimistic version preconditions.
- Validation produces blocking errors and non-blocking warnings by exact element.
- Publishing makes the version immutable and gives it a monotonically increasing
  revision number plus content checksum.
- Editing a published map clones it to a new draft.
- A version referenced by any session/order is never deleted or altered.
- Retired versions cannot be used for new sessions but remain readable historically.

### Validation Rules

- IDs and labels are unique within their defined scope.
- Every seat belongs to valid row/section and has valid geometry.
- Sellable count matches normalized capacity and configured limits.
- Disabled/non-sellable seats cannot receive normal price tiers.
- Companion seats reference an accessible seat and policy is internally valid.
- No unknown category, gate, or adjacency reference exists.
- Coordinate payload and element count remain below configured limits.
- The checksum is computed from canonicalized content, not client formatting.

Map import is a background job for large payloads. It stores original artifact,
parsing/validation report, proposed draft, and row-level errors. Partial silent
imports are forbidden.

## General-Admission Pools

A space/session may have pools such as `floor`, `balcony-standing`, or `vip-zone`.
Each pool has stable ID, label, physical capacity, sellable capacity, optional
channel allocations, and accessibility metadata. Session counters track held,
sold, killed, and comped quantities.

The following always holds:

```text
held + sold + killed + comped <= sellable_capacity <= physical_capacity
```

Reducing capacity is rejected if it violates commitments. Capacity corrections
require reason/audit and may need a dedicated incident workflow.

## Events

An event is the reusable customer-facing definition. Fields include organization,
title, slug, short/long descriptions, media asset references, tags/categories,
age guidance, accessibility and arrival details, organizer, default policy set,
locale content, and lifecycle status.

States: `draft`, `published`, `unpublished`, `cancelled`, `archived`. Publication is
revision-based: customer-visible text, terms, and price/session links used by an
order remain reproducible through snapshots even when the event changes later.

Event cancellation is not deletion. It blocks new sales, marks applicable sessions,
creates an affected-orders workflow, publishes customer/operator events, and lets
workers process notifications/refunds under an explicit policy.

## Sessions

A session binds an event revision to venue, space, inventory configuration, local
schedule, sales windows, price tiers, channels, and entry policy.

Required time fields:

- `doors_at` (optional), `starts_at`, `ends_at` (optional), all persisted UTC;
- source IANA timezone and local scheduled representation for operator clarity;
- `sales_start_at`, `sales_end_at`; optional presale windows;
- check-in open/close offsets or exact timestamps.

Validate ordering, daylight-saving ambiguity/nonexistence, maximum duration,
venue/space availability conflicts, and sales/check-in boundaries. Edits affecting
inventory/map/pricing after sales begin require a specific safe command, not generic
PATCH.

Session states:

```text
draft -> scheduled -> on_sale -> sales_closed -> in_progress -> completed
  |          |           |             |
  +----------+-----------+-------------+-> cancelled
```

The worker may derive time-based transitions, but commands and reads always compute
effective sales eligibility from timestamps and explicit status so worker delay
cannot permit invalid sales.

## Pricing

A price tier contains ID, label, currency, base price minor units, sales window,
seat categories/sections or GA pool scope, customer/channel visibility, per-order
minimum/maximum, tax rule reference, fee rule references, and status.

Pricing requirements:

- one currency per event in Phase 1;
- no floating-point arithmetic;
- percentage calculations define rounding mode and rounding point;
- fees state whether inclusive/exclusive and who receives them;
- tax uses an explicit versioned rule; provider tax may be an adapter;
- checkout snapshots every component and rule/version reference;
- published historical tiers are not rewritten; new revisions supersede them;
- customer-visible price presentation must match final checkout except for clearly
  disclosed variable components.

Implementation status: the current catalog slice provides tenant-scoped draft price
tiers for sessions, integer minor-unit amounts, one-currency enforcement per session,
quantity bounds, sales-window validation, cursor listing, and atomic audit/outbox
records. Seat/category applicability, channels, tax/fee rules, publication validation,
and checkout snapshots remain later slices.

## Channels and Allocations

Channels include public storefront, organization box office, partner allocation,
or private link. A channel can restrict visibility, sales window, price tiers,
quantity, and inventory allocation. Allocation is either hard-reserved capacity or
a soft policy; the choice is explicit per channel.

The sum of hard allocations plus unallocated reserved amount cannot exceed physical
inventory. Rebalancing cannot remove already held/sold inventory. Channel identity
is persisted on hold, order, and reporting records.

Implementation status: tenant-scoped channel identities are now available for public,
box-office, partner, and private-link types, with bounded configuration, lifecycle
status, cursor listing, and atomic audit/outbox records. Session allocation rows now
enforce tenant linkage, unique scopes, and serialized hard-capacity limits;
publication validation now checks materialized inventory capacity and hard
allocation overflow; a stale-safe publication transaction is still pending.

Publication validation is available as an authorization-protected report endpoint.
It currently checks event state, venue/space lifecycle, assigned-seat map
publication, session price-tier presence, materialized sellable capacity, and hard
allocation overflow. The event publish command now invokes this report as a
fail-closed precondition; an atomic capacity check is still needed before
publication can be considered fully stale-safe.

## Publication

Publication runs a deterministic validation report. P0 blockers include:

- inactive organization/venue/space;
- invalid/unpublished seat map or invalid GA capacity;
- missing event content required by channel;
- invalid session/sales times;
- no sellable price/inventory intersection;
- mismatched currencies;
- missing terms/refund policy;
- missing or unhealthy payment connection for paid sales;
- channel allocation overflow;
- no ticket signing key or delivery configuration;
- unsupported timezone/locale settings.

Warnings include incomplete optional media, unusually high capacity/price, missing
accessibility notes, or a provider health check older than the allowed interval.
Publish uses the validation revision/checksum as a precondition so content cannot
change between validation and publication.

## Media Assets

Clients request an upload intent with MIME type and size. Backend authorizes and
returns a short-lived signed object-storage upload. A finalize command verifies
object existence, size, checksum/type, organization prefix, and optional malware/
image-processing result before an asset becomes usable. Public delivery uses a CDN
or controlled signed URL; original bucket credentials never reach clients.

## Audit and Events

Material commands create audit actions such as `venue.updated`, `seat_map.published`,
`event.published`, `session.cancelled`, `price_tier.created`, and
`inventory.killed`. Corresponding domain events use immutable IDs, organization,
aggregate type/ID/version, occurrence time, actor/request correlation, and minimal
payload sufficient for consumers.

## Required Tests

- Venue/space lifecycle and dependency protection.
- Seat-map canonical checksum, validation cases, publish immutability, and clone.
- Session scheduling around DST changes and conflicting sessions.
- Publication blocker matrix and stale-validation precondition.
- Pricing rounding/golden tests across fee, tax, promo, and quantity combinations.
- Capacity/allocation constraint tests including concurrent changes.
- Historical order resolution after archive or new map/price revision.
- Media upload authorization, malicious type/size, missing object, and cross-tenant
  key substitution.
