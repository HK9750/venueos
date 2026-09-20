# Identity, Tenancy, and Access

## Authentication Surfaces

| Surface | Credential | Notes |
|---|---|---|
| Staff console | OIDC authorization-code flow with PKCE | Validate issuer, audience, signature, expiry, nonce/session; local user and membership remain authoritative for VenueOS access |
| Customer account | OIDC/passwordless provider | Optional for checkout; account links orders without making guest checkout impossible |
| Guest order access | High-entropy signed/hashed order access token | Scoped to one order and limited customer actions; rotate/revoke on suspected disclosure |
| Scanner device | Enrolled device key/token | Organization, venue, optional session, and capabilities are explicit; short-lived access derived from rotatable device credential |
| Server integration | Hashed API key or OAuth client credential | Scoped permissions, organization, expiry, last-use tracking, quotas, and revocation |
| Payment webhook | Provider signature over raw body | Verify before parsing/processing; apply timestamp tolerance and event deduplication |
| Outbound webhook | VenueOS HMAC signature | Include timestamp and delivery ID to prevent tampering and replay |
| WebSocket | One-time short-lived socket ticket | Mint through authenticated HTTPS; single use, topic-limited, never pass a long-lived bearer token in URL |

Passwords, MFA recovery, and primary identity proofing should be delegated to the
configured identity provider. VenueOS stores provider subject identifiers and its
own memberships, permissions, and account state.

## Organization Lifecycle

States: `active`, `suspended`, `closed`.

- `active`: normal configured operations.
- `suspended`: no new publication, holds, checkout, API writes, or device
  enrollment. Authorized owners/support may read, export, settle, and resolve
  existing obligations according to explicit permissions.
- `closed`: login/integration access disabled; data follows retention/deletion
  policy. Reopening requires platform-level audited operation.

Creation requires unique canonical slug, display name, default locale, timezone,
and currency. Provider connection is not required to draft catalog but is required
before paid publication.

## Users, Memberships, and Invitations

A user is global identity metadata keyed by `(issuer, subject)`. A membership binds
that user to one organization and contains role, status, and optional venue scope.
One user may belong to many organizations without sharing tenant data.

Membership states: `invited -> active -> revoked`; an invitation may also become
`expired` or `cancelled`. Acceptance requires a single-use, high-entropy token
stored only as a hash, matching intended normalized email (unless an owner uses an
explicit transfer flow), unexpired invitation, and active organization.

Rules:

- The last active owner cannot remove or demote themselves until ownership is
  transferred or organization closure is initiated.
- Membership role changes invalidate cached authorization and active sessions when
  privilege is reduced.
- Revocation takes effect immediately for new requests and invalidates device/API
  credentials explicitly owned by that membership when policy requires it.
- Invitations are rate-limited and deduplicated by organization/email/status.
- All invite, accept, role, revoke, and transfer operations emit audit records.

## Permissions

Permissions are explicit strings, not inferred throughout handlers. Initial set:

```text
organization.read organization.manage membership.read membership.manage
venue.read venue.manage catalog.read catalog.manage catalog.publish
inventory.read inventory.manage order.read order.create order.refund
order.comp ticket.read ticket.resend ticket.void
entry.scan entry.read entry.override device.manage
finance.read reconciliation.manage report.read export.create
promotion.manage waitlist.manage integration.manage webhook.manage
audit.read support.impersonate platform.support
```

Roles map to permissions in code/config with tests. An optional membership venue
scope restricts otherwise granted permissions to listed venues. High-risk commands
(`order.refund`, `ticket.void`, `entry.override`, credential rotation, impersonation)
require recent authentication where the identity provider supports it.

## Authorization Algorithm

For every protected command/query:

1. Authenticate credential and reject disabled/expired identity.
2. Determine organization from route/resource; do not trust body scope.
3. Load active membership or credential scoped to that organization.
4. Require named permission.
5. Enforce venue/resource constraints.
6. Load the target with `WHERE organization_id = $org` in the same repository call.
7. Apply state and ownership policy (customer owns order, device is assigned, etc.).
8. Audit high-risk success and denied administrative attempts without logging
   secret input.

Return the same public `not_found` response for absent and cross-tenant resources
when revealing existence would leak information. Use `forbidden` only when resource
existence is already safe to disclose.

## Tenant Context and Database Defense

Application context carries typed principal, organization ID, permissions, request
ID, and trace ID. Repository methods receiving tenant-owned data require an
organization ID argument; unscoped `GetByID` methods are forbidden outside
platform-admin code.

PostgreSQL row-level security is recommended as defense in depth:

- transaction sets a local verified organization setting;
- policies require `organization_id` to match that setting;
- worker/platform roles use separate database users with deliberate bypass;
- connection reset/tests ensure tenant context cannot leak through pooling;
- RLS is never used as a substitute for application authorization.

## API Keys

Key format includes a non-secret prefix/ID and high-entropy secret. Store only an
Argon2id/HMAC-derived verifier, organization, name, scopes, created/expiry/revoked
timestamps, creator, and last-used metadata. Plaintext appears exactly once.

- Rotation creates a new credential; an optional short overlap period is explicit.
- Revocation is immediate and audit logged.
- Scope may only be a subset of the creator's grantable permissions.
- Rate limits apply per key plus organization and IP safety limits.
- Public logs contain credential ID prefix, never the secret.

## Device Credentials

Enrollment is initiated by a door manager using a one-time code or QR with a short
expiry. The device generates or receives a secret, which is stored using platform
secure storage on the device and a verifier server-side. Record device name,
platform, app version, assigned venue/session/gates, status, last seen, credential
version, and clock skew estimate.

States: `pending`, `active`, `revoked`, `retired`. Revocation blocks sync and online
scans immediately. Offline manifests include their own expiry and revocation epoch
so long-offline devices cannot operate indefinitely.

## Support Access and Impersonation

Support never receives blanket silent tenant access. Any elevation requires a
platform permission, ticket/reason, target organization, bounded duration, visible
banner/claim, immutable audit record, and optionally organization notification.
Impersonated sessions cannot view secrets, create/rotate credentials, or change
ownership unless a narrower emergency procedure explicitly permits it.

## Session and Token Safety

- Access tokens are short-lived; refresh/session revocation is honored.
- Cookies, if used, are `Secure`, `HttpOnly`, appropriate `SameSite`, scoped, and
  protected by CSRF tokens for state-changing browser requests.
- Bearer-token APIs do not enable credentialed wildcard CORS.
- Validate `iss`, `aud`, `exp`, `nbf`, algorithm, and key rotation; cache JWKS with
  expiry and safe refresh on unknown key ID.
- Authorization caches have short bounded TTL and are explicitly invalidated after
  membership/credential changes.
- Failed auth uses stable external messages and classified internal metrics.

## Required Tests

- Permission matrix test for every route/use case.
- Cross-tenant ID substitution for every tenant resource type.
- Membership revoke/demotion cache invalidation.
- Invitation replay, expiry, wrong email, and concurrency.
- API key show-once, hash verification, rotation overlap, expiry, and revocation.
- OIDC wrong issuer/audience/algorithm/key, expiry, and JWKS rotation.
- Device revoked/offline-expired behavior.
- RLS pooled-connection leakage test when RLS is enabled.
- Support impersonation restriction and complete audit evidence.

