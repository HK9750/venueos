# Security, Privacy, and Compliance

## Security Objectives

VenueOS must prevent cross-tenant disclosure, unauthorized inventory/financial/
admission changes, credential/payment theft, overselling through abuse, forged or
replayed tickets/webhooks, and silent repudiation of operator actions. It must also
limit damage when a client, provider, tenant administrator, or single infrastructure
component is compromised.

## Threat Boundaries

Untrusted inputs include all HTTP/WebSocket data, identity claims until verified,
QR data, uploaded files, provider webhook bodies, outbound endpoint URLs/responses,
CSV content, template variables, cached Redis data, and existing database metadata
that originated externally.

High-value assets include organization/customer data, membership authority, API and
device credentials, payment account connections, ticket signing keys, order/refund
records, inventory locks, audit evidence, exports, and provider webhook secrets.

## Required Controls

### Network and Transport

- TLS 1.2+ externally, secure internal transport according to platform threat model,
  HSTS at edge, and no plaintext credential paths.
- Trusted proxy count/CIDRs are explicit before honoring forwarded IP/proto/host.
- Host allowlist, strict CORS origins/methods/headers, and WebSocket Origin checks.
- Public API, admin/metrics, and profiling listeners are separated; admin listener
  binds loopback/private network and requires network/auth controls.
- Egress is allowlisted where practical; webhook egress has SSRF-safe resolver and
  blocks cloud metadata/internal ranges on initial and redirected destinations.

### Request Safety

- Header/body/decompressed size, read-header, read, write, idle, and handler
  deadlines are explicit.
- Reject ambiguous duplicate security headers and invalid UTF-8/control characters.
- SQL is parameterized/generated; dynamic sort/filter identifiers are allowlisted.
- HTML/email templates escape by context. CSV exports neutralize `=`, `+`, `-`, and
  `@` formula starts.
- Uploads use server keys, allowlisted type/signature, size limits, checksum,
  quarantine/scan, and private storage.
- Rate limits combine edge and application controls by route risk, IP, credential,
  organization, device, and target session. Limits never become the only defense.

### Secrets and Cryptography

- Secrets come from managed secret storage or injected runtime values; never source,
  logs, images, example configs, or API responses.
- Use established libraries/algorithms only. Ticket/webhook signing has algorithm
  allowlist, key ID, active/verify-only/retired states, rotation overlap, and
  documented emergency revocation.
- API/invitation/guest/socket tokens have at least 128 bits of entropy and are stored
  hashed when lookup permits. Compare authenticators in constant time.
- Sensitive database/object content uses platform encryption at rest; especially
  sensitive fields use envelope encryption/key reference when needed.
- Rotation covers OIDC keys, ticket keys, webhook secrets, API/device credentials,
  provider secrets, database credentials, and storage signing credentials.

### Application Authorization

- Deny by default using named permissions and tenant-scoped repository queries.
- High-risk operations require reason, current resource version, and recent auth
  where available.
- Platform support access is temporary, visible, restricted, and audited.
- Mass assignment is prevented by mapping transport DTOs into explicit commands.
- State transitions are allowlisted; clients cannot PATCH state/status directly.
- Idempotency, unique constraints, and locks prevent retry/replay from multiplying
  financial, inventory, ticket, or admission effects.

## Payment and PCI Boundary

Use provider-hosted/tokenized payment components. VenueOS must never receive/store
PAN, CVC, track, or raw bank credentials. Persist provider customer/payment-method
references only when required and safe to display in redacted form. Document the
applicable PCI self-assessment scope with a compliance professional before launch.

Webhook signature plus tenant/provider-account match is mandatory. A successful
browser redirect or client-reported token is not evidence of capture. Logs/traces
must filter provider request bodies and secrets.

## Ticket and Check-In Security

- QR payload is signed and carries no PII, raw order token, or predictable sole
  authenticator.
- Server verifies current ticket/version/status for online scans.
- Offline manifests are device/session scoped, signed, encrypted locally, bounded,
  expiring, and carry revocation epoch.
- Device enrollment codes are one-time and short-lived; credentials are rotatable.
- Duplicate/invalid scans are rate monitored, but every operationally useful attempt
  is retained without reflecting excessive attacker-controlled content.
- Overrides require elevated permission and reason; scanner operators cannot grant
  themselves override rights.

## Privacy Data Inventory

| Data | Purpose | Exposure minimization |
|---|---|---|
| Customer name/contact | Delivery, support, legal receipt | Snapshot only necessary fields; encrypt/restrict; redact logs |
| Identity profile | Staff/customer login | Provider subject plus minimal profile |
| Order/payment metadata | Contract and reconciliation | No raw instrument data; role-restricted |
| Device/IP/user agent | Security/audit/fraud | Truncate/hash where full value is unnecessary; defined retention |
| Scan history | Entry operations/security | Minimal ticket/device/result data; no QR raw payload by default |
| Waitlist contact/consent | Offer notification | Separate consent, short retention, delete on withdrawal where lawful |
| Audit trail | Accountability/security | Safe diffs and IDs; avoid secret/PII values |
| Exports/artifacts | Authorized business use | Private, expiring, signed access, access audit |

Define controller/processor responsibilities, privacy notice, lawful bases/consent,
subprocessors, regional transfer, breach process, and retention with legal review.

## Data Subject and Organization Lifecycle

- Export/access requests collect user-linked data from identity, order, ticket,
  waitlist, and communication systems with verified requester authority.
- Correction updates mutable profile data but does not rewrite immutable financial/
  audit evidence; append correction references when needed.
- Deletion/anonymization removes non-required PII and cryptographically deletes
  eligible encrypted values while retaining lawfully required pseudonymous records.
- Tenant closure stops access/new processing, exports if authorized, revokes keys,
  disconnects providers, schedules retention actions, and produces completion log.
- Backups follow delayed deletion limitations disclosed by policy and are protected
  against normal application access.

## Audit Trail

Audit entries are append-only and include tenant, actor type/ID, effective/support
actor, action, subject, timestamp, request/trace, source/device, result, reason, and
safe before/after field changes. Never include tokens, secret config, raw webhook/
QR/payment content, complete contact values, or uncontrolled request bodies.

Audit access is permissioned and itself logged. Retention and export integrity are
documented. For stronger tamper evidence, periodically chain/hash/sign batches and
store a checkpoint outside the primary write path.

## Secure Development

- Threat model each high-risk feature and revisit after architecture changes.
- Pin dependencies/tools; review updates; generate SBOM; scan source, dependencies,
  images, IaC, and secrets in CI.
- Minimal/distroless non-root containers, read-only filesystem where possible,
  dropped capabilities, resource limits, and separate workload identities.
- Production data is not copied into developer/test environments without approved
  de-identification.
- Error responses are generic; internal logs are structured and redacted.
- Security findings receive owner, severity, remediation target, and regression test.

## Abuse Cases to Test

- Substitute organization/resource IDs across every endpoint.
- Reuse idempotency key with altered amount/items; replay invitation/order/socket
  token; brute force order/ticket references.
- Hold inventory repeatedly to deny sales; open excessive WebSockets/polls; submit
  huge seat arrays, QR values, cursors, JSON, or compressed bodies.
- Forge payment/outbound webhook signatures; replay old signed messages; exploit
  provider-account mismatch.
- SSRF via direct IP, DNS rebinding, redirect, IPv6, userinfo, alternate encoding,
  and cloud metadata hostname.
- Upload polyglot/oversized/mismatched file; inject HTML/template/CSV/log content.
- Use revoked/demoted membership, API key, device, ticket key, or offline manifest.
- Attempt refund/scan races and operator override without permission.

## Launch Security Gate

Launch requires documented threat model, external/internal penetration test or
equivalent review, no unresolved critical/high findings, verified secret rotation,
restored backup test, least-privilege access review, log redaction tests, provider
webhook verification, tenant-isolation automation, abuse/load tests, incident
contacts, and data retention/privacy signoff.

