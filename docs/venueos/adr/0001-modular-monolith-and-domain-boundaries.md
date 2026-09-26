# ADR-0001: Modular Monolith and Domain Boundaries

- Status: accepted
- Date: 2026-09-23
- Owners: VenueOS architecture maintainers
- Decision deadline: accepted baseline; changes require a superseding ADR

## Context and Forces

VenueOS must preserve tenant isolation and transactional correctness across catalog,
inventory, checkout, payment, ticketing, and entry workflows. Several mutations must
commit business state, audit data, idempotency results, and outbox events atomically.
The initial team and operational platform should not absorb distributed transaction,
network-failure, version-skew, and multi-service deployment costs before measured
scale or ownership needs justify them.

At the same time, a single deployable must not become an unstructured codebase where
domains reach into one another's repositories or tables. Boundaries need to support
independent reasoning, testing, ownership, and possible later extraction.

This ADR records accepted default D-001 and the domain ownership described in the
architecture plan. It does not decide the deployment provider, PostgreSQL RLS, or
any external provider selection.

## Options Considered

### Modular monolith with explicit domain boundaries

Keep one primary Go module and one PostgreSQL authority, with separately deployable
API, worker, and migration commands. Domains expose application interfaces and
publish durable events for cross-domain effects.

This preserves local transactions and a simple operating model while retaining
clear seams for later extraction.

### Independently deployed services from the start

Split identity, catalog, booking, commerce, ticketing, and entry into separate
network services and data ownership boundaries immediately.

This can provide independent scaling and deployment, but introduces premature
distributed consistency, contract-versioning, observability, and incident-response
costs. The current requirements provide no evidence that those costs are necessary.

### Layered monolith without enforced domain ownership

Keep one deployment and organize code only by technical layers such as handlers,
services, and repositories.

This is initially simple, but permits cross-domain repository access and makes
business invariants, ownership, and future extraction increasingly difficult to
identify and enforce.

## Decision

VenueOS will be a modular monolith with three explicit process entry points:

- `api` owns synchronous HTTP, health, and future realtime interfaces;
- `worker` owns durable asynchronous processing and scheduled maintenance;
- `migrate` owns explicit schema migration execution during deployment.

The codebase will use the dependency direction `transport -> application -> domain`,
with infrastructure adapters implementing ports owned by the consuming application
layer. Domain and application packages must not import HTTP frameworks, SQL drivers,
Redis clients, or external provider SDKs.

The initial domain ownership is:

- identity: organizations, users, memberships, invitations, and credentials;
- catalog: venues, spaces, maps, events, sessions, pricing tiers, and publication;
- booking: session inventory, availability, and holds;
- commerce: carts, orders, payments, refunds, and reconciliation;
- ticketing: entitlements, ticket versions, artifacts, and delivery requests;
- entry: gates, devices, scan attempts, admissions, and offline manifests;
- growth: promotions, redemptions, waitlists, and offers;
- reporting: read models and exports;
- integrations: outbound webhooks and provider coordination;
- platform: cross-cutting primitives and implementation mechanisms, but no
  domain-specific policy.

Domains communicate through explicit application interfaces or versioned durable
domain events. A domain must not import another domain's infrastructure repository,
issue SQL against another domain's tables, or mutate another domain's state behind
its application boundary. A use case that requires one atomic local transaction may
use an application-level coordinator while preserving repository ownership.

PostgreSQL remains the authority for durable business state. Cross-domain effects
that do not require the initiating transaction use the PostgreSQL outbox/job path.
Redis and future brokers cannot become the sole record of a business mutation.

A domain may be extracted into a service only after measured scaling, isolation,
team ownership, or release-cadence evidence justifies the added network boundary.
Extraction requires a new ADR covering data ownership, consistency changes,
contracts, backfill, failure recovery, observability, rollout, and rollback.

## Consequences and Risks

Benefits:

- critical workflows can use PostgreSQL transactions without distributed commits;
- local development, testing, deployment, and incident response remain tractable;
- explicit ownership reduces accidental coupling and provides extraction seams;
- API and worker processes can scale independently while sharing domain code.

Costs and risks:

- process-level scaling is coarser than independently deployed domain services;
- package boundaries rely on code review and automated dependency checks that must
  be added as the domain tree grows;
- a shared database makes unauthorized cross-domain SQL technically possible, so
  query ownership and reviews must remain strict;
- incompatible in-process API changes are easier to make, requiring contract and
  event compatibility tests at every externally durable boundary.

## Migration and Rollback

The repository already follows this topology, so acceptance requires no data
migration or production rollout. New VenueOS slices will be introduced inside the
recorded domain boundaries while the transitional example user slice is retired
deliberately.

This decision can be reversed only through a superseding ADR. Service extraction
must use an expand/migrate/contract sequence with a tested fallback to the existing
in-process path until data ownership and replay are verified. Rolling back an
individual feature continues to require backward-compatible schema and event
contracts.

## Operational and Security Impact

- API, worker, and migrate receive separate configuration and least-privilege
  credentials appropriate to their process responsibilities.
- Tenant identity is derived from authenticated membership and is passed explicitly
  across application boundaries; domain separation never substitutes for tenant
  authorization.
- Sensitive data redaction, request/trace correlation, audit writing, and metrics
  remain shared platform mechanisms with domain-specific event meaning.
- Worker crashes and Redis loss must not lose durable state because mutations and
  asynchronous intent are committed to PostgreSQL before acknowledgement.
- Runbooks and dashboards distinguish process health and domain workload even when
  those domains share one deployment artifact.

## Links

- [Accepted decisions](../15-decisions-and-open-questions.md#accepted-defaults)
- [Architecture and repository design](../03-architecture-and-repository.md)
- [Implementation workboard](../18-implementation-workboard.md)
- [Engineering conventions](../16-engineering-conventions.md)

