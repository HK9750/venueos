# VenueOS Implementation Workboard

This is the execution index for turning the VenueOS plan into code. The detailed
domain documents define behavior; this file tracks delivery order and status.

## Status Convention

- `not_started`: no production implementation exists.
- `foundation`: reusable template capability exists but is not yet VenueOS domain behavior.
- `in_progress`: implementation has an owner and active branch/change.
- `blocked`: an open decision or external dependency prevents safe completion.
- `implemented`: code and focused tests exist, but milestone exit criteria may remain.
- `verified`: milestone exit criteria and required evidence are complete.

Do not mark an epic verified from code completion alone. Verification includes
contracts, migrations, authorization, observability, recovery, tests, and docs.

## Current Baseline

| Area | Status | Current state | Next action |
|---|---|---|---|
| API process | foundation | `net/http`, middleware, health/readiness, metrics, tracing, stable VenueOS error envelope, bearer authentication wired to tenant-scoped API-key verification, metadata listing, versioned API-key create/rotate/revoke commands with show-once secrets, permission-gated audit exploration, bounded entry dashboard summaries, one-time scanner-device enrollment/lifecycle/assignment commands, authorization-bound online scan service, durable public ticket-key resolution, and authenticated device scan route | Add OIDC adapter/session loading and private-key registration/rotation |
| Worker process | implemented | PostgreSQL job claim/lease/retry/dead-letter lifecycle, bounded handlers, tracing and metrics, plus durable outbox relay mechanics | Wire domain event publishers and consumers through the relay |
| Migration process | foundation | Embedded users plus organization/audit/outbox/idempotency/inbox/job/identity/membership/invitation/venue/seat-map/event/session/pricing/channel/cart/order/payment-attempt/ticket/entry/refund/device-assignment migrations with rollback tests | Add catalog publication schemas |
| PostgreSQL | foundation | pgx pool, readiness, bounded transaction retry, atomic platform records, tenant-scoped organization/access/venue repositories, and bounded pool saturation/wait metrics | Add catalog repositories |
| OpenAPI | foundation | Common error contract plus generated user, organization/access, and venue/space operations | Retire example user contract deliberately |
| SQLC | foundation | Separate generated user, platform, catalog, inventory, cart, order, payment, entry, and refund query packages | Split additional query packages by owning VenueOS domain as domains arrive |
| Observability | foundation | structured logging, Prometheus/OpenTelemetry, bounded worker/job queue metrics, stable HTTP error-code metrics, PostgreSQL pool metrics, audit response redaction tests, and explicit entry-summary freshness fields | Add domain metrics and broader redaction tests |
| Platform primitives | implemented | UUIDv7 IDs, checked minor-unit money, UTC clocks, stable error codes, bounded validation | Adopt in the first organization vertical slice |
| CI/container | foundation | generation, race, integration, lint, vulnerability and image builds | Add contract compatibility, migration matrix, secret/image scans |
| VenueOS documentation | verified | complete product-to-delivery planning set | Keep synchronized with every implementation slice |

## Epic 0 — Project Identity and Baseline

Status: `implemented`; the development baseline is verified, while the license and
Milestone 0 ownership decisions remain open.

- [x] Create separate `venueos` project from the reusable template.
- [x] Rename root and tools Go modules to `github.com/HK9750/venueos`.
- [x] Rename service defaults, CI image tags, OpenAPI title and local database.
- [x] Carry VenueOS `AGENTS.md`, repository skill and detailed planning docs.
- [x] Initialize an independent local Git repository with default branch `main`.
- [x] Configure the intended source-control remote.
- [ ] Choose and add the project license.
- [x] Run clean generation/test/race/vet/lint/vulnerability/build checks.
- [x] Confirm the supported Go version is available locally and is pinned for CI.
- [ ] Record deployment/provider owners for open Milestone 0 decisions.

Baseline evidence (2026-09-23): Go 1.27.1 matched `go.mod`; module verification,
generation with a clean generated diff, unit tests, race tests, vet, lint,
`govulncheck`, command builds, the Testcontainers PostgreSQL integration suite,
and API/worker/migrate container builds passed. The configured GitHub `origin` had
already completed [CI run #1](https://github.com/HK9750/venueos/actions/runs/35519773383)
successfully on 2026-09-20, including tests, lint, and all three image builds.

Gate: clean clone can configure, generate, test, build, migrate and run all three
commands without access to production secrets.

## Epic 1 — Platform Contracts

Status: `in_progress`.

1. [x] Define the VenueOS API error envelope and request ID schema in OpenAPI;
   cursor schemas remain with the first cursor-paginated endpoint.
2. [x] Introduce typed platform packages for identifiers, money, clock, validation,
   and stable application error codes.
3. [x] Add transaction runner with isolation/deadlock classification and bounded retry.
4. [x] Add organization-aware principal/context types without selecting an OIDC vendor.
5. [x] Add audit, idempotency, outbox, inbox and job table migrations.
6. [x] Implement atomic audit/outbox/idempotency repository primitives.
7. [x] Implement worker claim/lease/retry/dead-letter loop and operational metrics.
8. [x] Add PostgreSQL outbox claim/lease/ack/retry/dead-letter relay mechanics with bounded safe failures.
9. [x] Add provider-neutral inbox receive/deduplicate/lease/retry/dead-letter primitives.
10. [x] Add generated-code cleanliness and migration-from-empty CI checks.

Gate: a synthetic tenant-scoped command can commit mutation + audit + outbox +
idempotency result once, replay safely, and be consumed by two competing workers.

## Epic 2 — Organizations and Access

Status: `in_progress`; provider-neutral membership/invitation/API-key behavior is
implemented, while live OIDC verification and the final provider choice remain.

1. [x] Add organizations, users, memberships and invitations schema.
2. [x] Implement organization lifecycle and settings domain rules.
3. [x] Implement invitation issue/accept/expire/revoke and last-owner invariant.
4. [x] Define fixed role-to-permission mapping and centralized authorization policies.
5. [x] Add provider-neutral OIDC verifier port plus development/test fake; choose
   and harden the production adapter after the identity-provider decision.
6. [x] Add API key create/show-once/hash/expiry/revoke/rotate workflow; rotation
   defaults to immediate revocation without overlap. Metadata list, versioned
   revocation, create, and rotate HTTP routes now return one-time secrets only on
   the issuing response.
7. [x] Add tenant-scoped repositories and defer RLS through the documented decision gate.
8. [x] Add organization/membership/invitation OpenAPI operations, permission-gated audit exploration, and audit/outbox foundation.
9. [x] Run the first permission-matrix and cross-tenant substitution suite for the implemented access commands.

Gate: every organization command/query denies guessed cross-tenant resources and
membership revocation takes effect without waiting for an unsafe cache lifetime.

## Epic 3 — Catalog Vertical Slice

Status: `in_progress`; venue, space, gate, general-admission pool, assigned
seat-map version, event revision, session scheduling, and base price-tier
foundations, session allocation wiring, publication validation, and fail-closed
publish enforcement are implemented, while discovery remains.

Implement in this internal order: venue → space/gates → seat-map versions/GA pool →
event revisions → session scheduling → price tiers/channels → publication validation
→ public discovery. Build one route/migration/use-case/repository/test slice at a
time. Do not begin inventory materialization until published map and session
revisions are immutable and referentially stable.

1. [x] Venue and space create/get/list/update/archive/restore foundation with tenant
   predicates, lifecycle/version checks, and atomic audit/outbox records.
2. [x] Gates and entry-point metadata with optional tenant-scoped space assignment.
3. [x] General-admission pool templates and immutable assigned-seat map versions.
4. [x] Event identity, revision lifecycle, session scheduling, base price-tier
   lifecycle, sales-channel identities, session allocation wiring, publication
   validation reporting, and fail-closed publish enforcement; discovery remains.

Gate: an administrator can publish one assigned-seat and one GA session, and a
public client can retrieve their catalog without accessing draft or another tenant.

## Epic 4 — Booking Core

Status: `in_progress`; GA pool and assigned-seat materialization, the GA inventory/
hold foundation, reserved-seat hold/release transactions, bounded due-hold expiry,
version-checked renewal and modification, atomic availability replay rows, and
repeatable-read snapshot/replay repository reads are implemented, while public
storefront/checkout routes and the remaining hold-policy contract remain.

Implement session inventory, snapshot/revision, reserved-seat and GA holds, expiry,
owner tokens, update/release/renew, and PostgreSQL realtime replay events. Polling
ships here before WebSockets. The concurrency suite is an exit requirement, not a
later performance task.

Completed repository behaviors include hold release, one-renewal policy, and
atomic version-checked hold modification with inventory rollback on conflict.

Gate: repeated high-contention tests prove no oversell, no partial multi-seat hold,
one terminal expiry/confirmation outcome, and correct operation without Redis.

## Epic 5 — Purchase-to-Entry Path

Status: `in_progress`; durable tenant-scoped cart/quote/order/payment-attempt
foundations, ticket/entry persistence, provider-neutral transitions, credential
verification, transactional online admission, and one-time scanner-device
enrollment/lifecycle/assignment and credential authentication are implemented. The
authorization-bound scan application service, durable public ticket-key resolution,
authenticated HTTP scan route, idempotent entitlement-keyed ticket issuance
transaction, strict `ticket.issue` payload, tenant-bound worker handler, and
verified payment webhook receipt boundary are now in place; provider confirmation,
worker/signer composition, delivery, private-key registration/rotation, offline
scanning, and realtime fan-out remain.

1. [x] Add deterministic minor-unit pricing/quote arithmetic and immutable
   snapshot digest plus a durable one-session cart with owner/version checks;
   policy selection and checkout remain.
2. [x] Add provider-neutral order/payment transition tables with idempotent
   same-state replay and durable cart-to-order creation; guest/customer access
   and confirmation remain.
3. [x] Add provider-neutral payment intent/refund adapter and webhook-event
   contract with inbox-ready hash/failure handling plus durable payment-attempt
   constraints; Stripe wiring, capture policy, and order confirmation remain.
4. [x] Add provider-neutral signed QR credential verification with key rotation
   support, durable entitlement/ticket state, idempotent entitlement-keyed ticket
   issuance, and the strict version-one issuance job contract; issuance artifacts,
   rendering, delivery, and private-key registration remain.
5. [~] Add pure online-scan decision checks, the serializable ticket/device
   admission transaction, bounded staff entry summary read, one-time device
   enrollment/lifecycle/assignment commands and credential authentication, the
   authorization-bound scan application service, durable public ticket-key
   resolution, authenticated HTTP scan route, idempotent ticket issuance, and
   tenant-bound issuance worker adapter; worker/signer composition, private-key
   registration/rotation, and offline scanning remain.
6. WebSocket fan-out/replay over the already working polling model.

Gate: publish → hold → checkout → provider webhook → ticket → scan succeeds in a
deployed-like environment, and every retry/race produces one logical outcome.

## Epic 6 — Operational Completion

Status: `in_progress`; provider-neutral refund invariants, lifecycle guards,
durable locked refund requests, authenticated refund-request transport, execution state transitions, strict
`refund.execute` jobs, and bounded provider orchestration are implemented, while
policy, concrete provider wiring, webhook/reconciliation convergence, offline
scanning, promotions, and reporting remain.

1. [x] Add provider-neutral refund amount ceilings, lifecycle guards, a locked
   durable refund-request repository, authenticated idempotent refund-request
   transport, and idempotent `refund.execute` orchestration with processing/final
   states; policy, concrete provider wiring, and reconciliation remain.
2. Implement refund/cancellation/reconciliation, offline scanning, promotions,
waitlists, outbound webhooks, reporting and exports. Each capability follows the
ordering and gates in `14-delivery-roadmap.md`; no growth feature displaces an
unfinished financial, inventory, tenant or entry invariant.

## Epic 7 — Production Readiness and Launch

Status: `not_started`.

Complete threat/privacy/PCI reviews, provider readiness, SLO load/soak/chaos tests,
alerts/runbooks, retention, backup restore, canary/rollback, pilot reconciliation,
and GA evidence. See milestones 13–14 and the traceability matrix.

## First Ten Implementation Changes

Keep these as small reviewable changes, in order:

1. [x] Project bootstrap verification and baseline status update.
2. [x] [ADR-0001](adr/0001-modular-monolith-and-domain-boundaries.md) confirming
   modular monolith and domain boundaries.
3. [x] VenueOS platform IDs, money, clock, error codes and tests.
4. [x] Organization/audit/outbox/idempotency initial migration.
5. [x] Transaction runner and atomic platform repositories.
6. [x] Durable job runner with lease, retry and dead-letter tests.
7. [x] Organization domain plus create/get OpenAPI vertical slice; live credential
   verification remains blocked on the identity-provider decision.
8. [x] Membership roles/authorization plus cross-tenant suite; live credential verification remains blocked on the identity-provider decision.
9. [x] Invitation lifecycle vertical slice with hashed one-time tokens and atomic acceptance.
10. [x] API key create/show-once/hash/expiry/revoke/rotate workflow and provider-neutral
    OIDC adapter contract; production OIDC wiring remains blocked on the provider decision.

Each change must satisfy the feature worksheet in `16-engineering-conventions.md`
and the repository `AGENTS.md` definition of done.
