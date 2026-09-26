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
| API process | foundation | `net/http`, middleware, health/readiness, metrics, tracing, and stable VenueOS error envelope | Introduce `/v1` auth and tenant context |
| Worker process | implemented | PostgreSQL job claim/lease/retry/dead-letter lifecycle, bounded handlers, tracing and metrics | Register the first domain handler and add outbox relay |
| Migration process | foundation | Embedded users plus organization/audit/outbox/idempotency/inbox/job migrations with rollback tests | Add membership schemas in the organization slice |
| PostgreSQL | foundation | pgx pool, readiness, bounded transaction retry, and atomic platform record writes | Add tenant-scoped domain repositories and pool metrics |
| OpenAPI | foundation | Common error contract plus generated example user API | Add organization slice, then retire example user contract deliberately |
| SQLC | foundation | Separate generated user and platform query packages | Split additional query packages by owning VenueOS domain as domains arrive |
| Observability | foundation | structured logging, Prometheus/OpenTelemetry, and bounded worker job/queue metrics | Add stable API error/domain metrics and broader redaction tests |
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
4. [ ] Add organization-aware principal/context types without selecting an OIDC vendor.
5. [x] Add audit, idempotency, outbox, inbox and job table migrations.
6. [x] Implement atomic audit/outbox/idempotency repository primitives.
7. [x] Implement worker claim/lease/retry/dead-letter loop and operational metrics.
8. [ ] Add generated-code cleanliness and migration-from-empty CI checks.

Gate: a synthetic tenant-scoped command can commit mutation + audit + outbox +
idempotency result once, replay safely, and be consumed by two competing workers.

## Epic 2 — Organizations and Access

Status: `not_started`; blocked on identity-provider choice only for live OIDC wiring.

1. Add organizations, users, memberships and invitations schema.
2. Implement organization lifecycle and settings domain rules.
3. Implement invitation issue/accept/expire/revoke and last-owner invariant.
4. Define fixed role-to-permission mapping and centralized authorization policies.
5. Add OIDC verifier port plus development/test fake and production adapter choice.
6. Add API key create/show-once/hash/rotate/revoke workflow.
7. Add tenant-scoped repositories and decide RLS through ADR/proof.
8. Add organization/membership OpenAPI operations and audit explorer foundation.
9. Run the first complete cross-tenant substitution and permission-matrix suite.

Gate: every organization command/query denies guessed cross-tenant resources and
membership revocation takes effect without waiting for an unsafe cache lifetime.

## Epic 3 — Catalog Vertical Slice

Status: `not_started`.

Implement in this internal order: venue → space/gates → seat-map versions/GA pool →
event revisions → session scheduling → price tiers/channels → publication validation
→ public discovery. Build one route/migration/use-case/repository/test slice at a
time. Do not begin inventory materialization until published map and session
revisions are immutable and referentially stable.

Gate: an administrator can publish one assigned-seat and one GA session, and a
public client can retrieve their catalog without accessing draft or another tenant.

## Epic 4 — Booking Core

Status: `not_started`; depends on Epic 3.

Implement session inventory, snapshot/revision, reserved-seat and GA holds, expiry,
owner tokens, update/release/renew, and PostgreSQL realtime replay events. Polling
ships here before WebSockets. The concurrency suite is an exit requirement, not a
later performance task.

Gate: repeated high-contention tests prove no oversell, no partial multi-seat hold,
one terminal expiry/confirmation outcome, and correct operation without Redis.

## Epic 5 — Purchase-to-Entry Path

Status: `not_started`; implement as sub-epics with independent gates.

1. Pricing/cart and immutable commercial snapshots.
2. Idempotent order creation and guest/staff access.
3. Stripe adapter, payment inbox and order confirmation.
4. Ticket entitlement, signing, rendering and delivery.
5. Device enrollment and online scan/admission.
6. WebSocket fan-out/replay over the already working polling model.

Gate: publish → hold → checkout → provider webhook → ticket → scan succeeds in a
deployed-like environment, and every retry/race produces one logical outcome.

## Epic 6 — Operational Completion

Status: `not_started`.

Implement refund/cancellation/reconciliation, offline scanning, promotions,
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
7. [ ] Organization domain plus create/get OpenAPI vertical slice.
8. [ ] Membership roles/authorization plus cross-tenant suite.
9. [ ] Invitation lifecycle vertical slice.
10. [ ] API keys and OIDC adapter contract.

Each change must satisfy the feature worksheet in `16-engineering-conventions.md`
and the repository `AGENTS.md` definition of done.
