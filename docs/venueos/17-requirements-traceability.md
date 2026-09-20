# Requirements Traceability

This crosswalk connects product requirements to the detailed design, delivery
milestone, and minimum proof. It is the release auditor's index; the linked domain
documents remain authoritative for behavior.

## Functional Requirements

| Requirement | Design source | Milestone | Minimum acceptance evidence |
|---|---|---:|---|
| Multi-tenant organizations | 04 identity/tenancy, 07 data | 2 | Cross-tenant matrix, scoped SQL/RLS tests |
| Staff invitations and roles | 04 identity/access | 2 | Invite lifecycle and permission matrix |
| Scoped API/device credentials | 04 identity/access, 11 security | 2/8 | Show-once/hash/rotate/revoke tests |
| Venue and space configuration | 05 catalog | 3 | Lifecycle/API/repository tests and admin demo |
| Assigned seat-map editing | 05 catalog, 07 data | 3 | Validation corpus, published immutability |
| General-admission capacity | 05 catalog, 06 booking | 3/4 | Constraint and concurrent final-unit tests |
| Event/session scheduling | 05 catalog | 3 | DST/conflict/state transition tests |
| Price tiers, fees, tax | 05 catalog, 06 pricing | 3/5 | Golden rounding/component reconciliation |
| Sales windows and channels | 05 catalog | 3 | Boundary, allocation, and publication tests |
| Publication validation | 05 catalog | 3 | Full blocker/warning matrix and stale revision |
| Public event discovery | 08 API | 3 | Contract, cursor, cache, tenant routing tests |
| Availability snapshot | 06 booking, 09 realtime | 4 | ETag/revision/audience/privacy tests |
| Reserved-seat hold | 06 booking | 4 | All-or-none contention with zero oversell |
| GA quantity hold | 06 booking | 4 | Conditional update capacity proof |
| Hold expiry/release | 06 booking, 10 workers | 4 | Worker delay/crash/read-time expiry tests |
| Hold update/renewal | 06 booking | 4 | Version, lifetime, policy and race tests |
| Server-side cart/pricing | 06 commerce | 5 | Tampered-client total rejected, snapshot proof |
| Guest/customer checkout | 06 commerce, 08 API | 5 | Ownership/access-token and validation tests |
| Idempotent order creation | 06 commerce | 5 | Same/different fingerprint and crash cases |
| Payment collection | 06 payments, 10 adapter | 6 | Provider sandbox and timeout recovery |
| Payment webhooks | 06 payments, 10 inbox | 6 | Signature, duplicate, ordering, account match |
| Late-payment recovery | 06 payments | 6 | Available/reallocated inventory branch tests |
| Order lookup/support | 06 commerce, 08 API | 5 | Authorized filters and enumeration defense |
| Ticket issuance and QR | 06 tickets, 11 security | 7 | Idempotency, tamper, version and key rotation |
| Ticket email/SMS delivery | 06 tickets, 10 integrations | 7 | Retry/bounce/suppression/resend evidence |
| Ticket void/reissue | 06 tickets | 7 | Old credential revoked and audit proof |
| Online check-in | 06 entry | 8 | Peak latency and one-admission contention |
| Device enrollment/scoping | 04 devices, 06 entry | 8 | Revoke/scope/expiry tests |
| Offline check-in | 06 entry, 11 security | 8 | Manifest expiry and conflict sync tests |
| Entry dashboard | 06 entry, 09 realtime | 8/9 | Count reconciliation and delay indicator |
| Polling delta/recovery | 09 realtime | 4/9 | Cursor/gap/retention/snapshot resync tests |
| WebSocket live updates | 09 realtime | 9 | Replay/handoff/backpressure/outage load test |
| Full/partial refunds | 06 refunds, 10 worker | 10 | Concurrent limit and provider failure tests |
| Session cancellation | 06 refunds, 10 worker | 10 | Restartable paged workflow and reconciliation |
| Payment reconciliation | 06 payments, 10 worker | 10 | Provider comparison/discrepancy resolution |
| Promotion codes | 06 promotions | 11 | Eligibility/rounding/final-limit concurrency |
| Waitlists and offers | 06 waitlists, 10 worker | 11 | Order/dedupe/expiry/conversion contention |
| Outbound webhooks | 10 integrations, 11 security | 11 | HMAC/retry/replay/SSRF/disable tests |
| Public integration API | 04 API keys, 08 API | 11 | Scope/quota/idempotency/deprecation tests |
| Sales/inventory/entry reports | 10 reporting | 12 | Canonical-total and freshness reconciliation |
| Async data exports | 10 reporting, 11 privacy | 12 | Access, stream, formula, signed expiry tests |
| Audit explorer | 04 access, 11 audit | 2+ | Completeness, redaction, access logging |

## Non-Functional Requirements

| Requirement | Design source | Milestone/gate | Proof |
|---|---|---:|---|
| Read availability 99.95% | 12 SLOs | 13/GA | Production-like SLI and burn alert |
| Booking write availability 99.9% | 12 SLOs | 13/GA | Error-budget dashboard and load/chaos data |
| Availability p95 <250 ms | 09 hot sessions, 12 SLOs | 4/13 | Recorded reference-load result |
| Hold create p95 <500 ms | 06 allocation, 12 SLOs | 4/13 | Contended/uncontended load result |
| No oversell | 06 algorithms, 07 constraints | Every release | Repeated concurrency and invariant checker |
| Tenant isolation | 04, 07, 11 | Every release | Automated negative suite and security review |
| Idempotent external writes | 06, 08, 10 | Every slice | Retry/crash/duplicate tests |
| Durable async work | 10 workers | 2+ | Claim/lease/crash/dead-letter tests |
| Full auditability | 03 transactions, 11 audit | Every slice | Mutation-to-audit coverage and redaction |
| Realtime recoverability | 09 realtime | 9 | Loss/gap/Redis outage recovery tests |
| Horizontal API/worker scaling | 03 architecture, 10 pooling | 13 | Multi-replica contention/load tests |
| Graceful shutdown | 03 runtime, 12 health | 1+ | In-flight HTTP/socket/job termination tests |
| Secure payment boundary | 11 PCI | 6/launch | Provider-hosted flow and data/log inspection |
| Privacy/retention lifecycle | 07 retention, 11 privacy | 13 | Export/deletion/purge and legal signoff |
| Backup and recovery | 12 DR | 13 | Isolated restore with measured RPO/RTO |
| Safe rolling deployment | 07 migrations, 12 deployment | Every release | Adjacent-version compatibility/rollback |
| Diagnosability | 12 observability | 1+ | Dashboards, alerts, traces, exercised runbooks |
| Vulnerability/supply chain controls | 11 secure development, 13 CI | Every build | Scan/SBOM/provenance results |

## Evidence Storage

Release evidence should be linked from the release record and include:

- CI run and immutable commit/image digest;
- migration and OpenAPI/event compatibility reports;
- automated test and race/fuzz/security scan results;
- load-test profile, environment, dataset, thresholds, and raw summary;
- provider sandbox scenario IDs and reconciliation output;
- backup restore and operational game-day record when applicable;
- accepted ADRs, open-risk approvals, feature flags, and rollback plan.

Do not mark a requirement complete because code exists. Completion means the
behavior is enabled or deliberately flagged, observable, supportable, documented,
and backed by the evidence in this table.

