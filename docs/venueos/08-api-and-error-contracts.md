# API and Error Contracts

## Contract Principles

- REST/JSON over HTTPS under `/v1`; breaking changes require a new major version.
- OpenAPI is the contract source and generates server interfaces/types plus client
  artifacts. Generated files are never edited by hand.
- Resource IDs are opaque strings/UUIDs. Routes use nouns and explicit action
  subresources for state transitions that are not ordinary updates.
- JSON fields use `snake_case`, RFC 3339 UTC timestamps, integer minor-unit money,
  ISO currency, IANA timezone, and explicit nullable/optional semantics.
- Unknown request fields are rejected for sensitive writes. Bodies, headers, path
  values, and decompressed payloads have limits.
- Reads are safe; writes are idempotent where clients/providers reasonably retry.
- Every response includes `X-Request-ID`; traces correlate internally without
  exposing sensitive telemetry.

## Common Headers

| Header | Direction | Use |
|---|---|---|
| `Authorization` | request | Bearer/access credential; never logged |
| `Idempotency-Key` | request | Required for hold, checkout, refund, resend/reissue, and selected integration writes |
| `If-Match` | request | Aggregate version/ETag precondition for edits and state transitions |
| `X-Request-ID` | both | Accepted when valid or generated; always returned |
| `X-Organization-ID` | request | Optional selector for multi-membership staff, verified against principal; never grants scope |
| `Last-Event-ID` | request | Realtime reconnect cursor when protocol/header supports it |
| `ETag` / `If-None-Match` | both | Snapshot/catalog caching and optimistic reads |
| `Retry-After` | response | Rate limit, maintenance, or known transient unavailability |

## Error Envelope

```json
{
  "error": {
    "code": "hold_expired",
    "message": "The reservation has expired.",
    "request_id": "req_...",
    "details": [
      {"field": "hold_id", "code": "expired", "message": "Create a new hold."}
    ]
  }
}
```

`code` is stable and machine-readable; `message` is safe and localizable; `details`
is bounded structured context. Never expose SQL, stack, provider secrets, internal
hostnames, or existence of another tenant's resource.

Status mapping:

| Status | Meaning / representative codes |
|---:|---|
| 400 | malformed JSON, invalid cursor, invalid combination |
| 401 | missing/invalid/expired credential |
| 403 | authenticated but permission/policy denied where existence is safe |
| 404 | absent or undisclosable resource |
| 409 | state conflict, inventory unavailable, idempotency mismatch, duplicate |
| 412 | version/ETag precondition failed |
| 413 | body too large |
| 415 | unsupported request media type |
| 422 | syntactically valid but domain validation failed |
| 429 | rate limit/quota exceeded |
| 500 | unexpected internal failure with generic message |
| 502/503/504 | classified provider/dependency unavailable/timeout where synchronous dependency is necessary |

## Pagination, Filtering, and Sorting

Lists use `limit` (default 50, maximum 100) and opaque signed/base64url cursor. A
cursor encodes endpoint/filter/sort identity and last deterministic key, typically
`(created_at, id)`. A changed filter or expired/invalid cursor returns `invalid_cursor`.

Responses:

```json
{"items": [], "page": {"next_cursor": null, "has_more": false}}
```

Allowlisted filters and sorts are documented per endpoint. Offset pagination is not
used for high-growth or externally consumed resources. Search inputs are length-
limited, normalized, tenant-scoped, parameterized, and rate-limited.

## Idempotency Contract

Required keys are 16–128 safe characters. Scope is organization + principal/client
+ operation + key. Store a canonical request fingerprint.

- First request owns processing and records the final status/body or resource ref.
- Same key/fingerprint waits briefly or returns in-progress, then replays the same
  meaningful result including error when finalized.
- Same key/different fingerprint returns `409 idempotency_key_reused`.
- A crashed processing claim becomes reclaimable after lease, but application state
  and provider idempotency are checked before repeating side effects.
- Retention is endpoint-documented and never shorter than the advertised client
  retry window.

## Public Catalog and Booking Routes

```text
GET    /v1/public/organizations/{org_slug}/events
GET    /v1/public/organizations/{org_slug}/events/{event_slug}
GET    /v1/public/sessions/{session_id}
GET    /v1/public/sessions/{session_id}/availability
GET    /v1/public/sessions/{session_id}/availability/changes?after={cursor}
POST   /v1/public/sessions/{session_id}/holds
GET    /v1/public/holds/{hold_id}
PATCH  /v1/public/holds/{hold_id}
POST   /v1/public/holds/{hold_id}/renew
DELETE /v1/public/holds/{hold_id}
PUT    /v1/public/holds/{hold_id}/cart
POST   /v1/public/holds/{hold_id}/checkout
GET    /v1/public/orders/{order_id}
GET    /v1/public/orders/{order_id}/tickets
POST   /v1/public/orders/{order_id}/ticket-deliveries
POST   /v1/public/sessions/{session_id}/waitlist
DELETE /v1/public/waitlist-entries/{entry_id}
```

Hold owner and guest order tokens use secure headers/cookies, not query strings.
Availability supports `ETag`, `304`, and version/cursor. Checkout returns order and
provider next-action data, not a promise that a client redirect equals confirmation.

## Organization and Access Routes

```text
GET    /v1/me
GET    /v1/organizations
POST   /v1/organizations
GET    /v1/organizations/{organization_id}
PATCH  /v1/organizations/{organization_id}
GET    /v1/organizations/{organization_id}/memberships
POST   /v1/organizations/{organization_id}/invitations
PATCH  /v1/organizations/{organization_id}/memberships/{membership_id}
DELETE /v1/organizations/{organization_id}/memberships/{membership_id}
POST   /v1/organizations/{organization_id}/api-keys
GET    /v1/organizations/{organization_id}/api-keys
POST   /v1/organizations/{organization_id}/api-keys/{key_id}/rotate
DELETE /v1/organizations/{organization_id}/api-keys/{key_id}
GET    /v1/organizations/{organization_id}/audit-entries
```

## Catalog Administration Routes

```text
GET/POST      /v1/organizations/{organization_id}/venues
GET/PATCH     /v1/organizations/{organization_id}/venues/{venue_id}
POST          /v1/organizations/{organization_id}/venues/{venue_id}/archive
GET/POST      /v1/organizations/{organization_id}/venues/{venue_id}/spaces
GET/PATCH     /v1/organizations/{organization_id}/spaces/{space_id}
GET/POST      /v1/organizations/{organization_id}/spaces/{space_id}/seat-maps
POST          /v1/organizations/{organization_id}/seat-maps/{map_id}/versions
GET/PATCH     /v1/organizations/{organization_id}/seat-map-versions/{version_id}
POST          /v1/organizations/{organization_id}/seat-map-versions/{version_id}/validate
POST          /v1/organizations/{organization_id}/seat-map-versions/{version_id}/publish
GET/POST      /v1/organizations/{organization_id}/events
GET/PATCH     /v1/organizations/{organization_id}/events/{event_id}
POST          /v1/organizations/{organization_id}/events/{event_id}/validate-publication
POST          /v1/organizations/{organization_id}/events/{event_id}/publish
POST          /v1/organizations/{organization_id}/events/{event_id}/unpublish
GET/POST      /v1/organizations/{organization_id}/events/{event_id}/sessions
GET/PATCH     /v1/organizations/{organization_id}/sessions/{session_id}
POST          /v1/organizations/{organization_id}/sessions/{session_id}/cancel
GET/POST      /v1/organizations/{organization_id}/sessions/{session_id}/price-tiers
GET/POST      /v1/organizations/{organization_id}/sales-channels
```

Generic `PATCH` cannot perform publish, cancel, capacity correction, or other
high-impact transitions; dedicated actions require reason and precondition.

## Operations, Commerce, and Entry Routes

```text
GET    /v1/organizations/{organization_id}/orders
POST   /v1/organizations/{organization_id}/orders
GET    /v1/organizations/{organization_id}/orders/{order_id}
POST   /v1/organizations/{organization_id}/orders/{order_id}/refunds
GET    /v1/organizations/{organization_id}/orders/{order_id}/refunds
POST   /v1/organizations/{organization_id}/orders/{order_id}/ticket-deliveries
POST   /v1/organizations/{organization_id}/tickets/{ticket_id}/void
POST   /v1/organizations/{organization_id}/tickets/{ticket_id}/reissue
POST   /v1/organizations/{organization_id}/devices/enrollment-codes
GET    /v1/organizations/{organization_id}/devices
PATCH  /v1/organizations/{organization_id}/devices/{device_id}
POST   /v1/device/sessions/{session_id}/manifest
POST   /v1/device/scans
POST   /v1/device/scans/batch
GET    /v1/organizations/{organization_id}/sessions/{session_id}/entry-summary
POST   /v1/organizations/{organization_id}/scan-attempts/{scan_id}/override
```

The single online scan endpoint is optimized for low latency. Batch sync has a
strict maximum item/byte count and per-item result, while authentication/manifest
failure can reject the whole batch.

## Growth, Reports, and Integration Routes

```text
GET/POST      /v1/organizations/{organization_id}/promotions
GET/PATCH     /v1/organizations/{organization_id}/promotions/{promotion_id}
GET           /v1/organizations/{organization_id}/waitlist-entries
GET           /v1/organizations/{organization_id}/reports/sales
GET           /v1/organizations/{organization_id}/reports/inventory
GET           /v1/organizations/{organization_id}/reports/entry
POST          /v1/organizations/{organization_id}/exports
GET           /v1/organizations/{organization_id}/exports/{export_id}
GET/POST      /v1/organizations/{organization_id}/webhook-endpoints
GET/PATCH     /v1/organizations/{organization_id}/webhook-endpoints/{endpoint_id}
POST          /v1/organizations/{organization_id}/webhook-endpoints/{endpoint_id}/rotate-secret
GET           /v1/organizations/{organization_id}/webhook-deliveries
POST          /v1/organizations/{organization_id}/webhook-deliveries/{delivery_id}/replay
POST          /v1/webhooks/payments/{provider}
POST          /v1/realtime/tickets
```

## Validation Rules

- Distinguish missing, empty, and null according to contract.
- Trim only fields whose semantics permit it; preserve customer display text while
  maintaining a normalized comparison field.
- Unicode length is defined in runes/graphemes where user-facing, bytes for payload
  safety. Reject invalid UTF-8 and control characters where inappropriate.
- Validate email/phone conservatively without claiming deliverability; verification
  is a separate behavior.
- Currency/amount, timezone, locale, URLs, enums, quantities, date ranges, and array
  uniqueness are validated before domain invocation.
- URLs for outbound webhooks reject unsupported schemes, credentials, loopback,
  link-local, private/internal networks, and DNS rebinding through egress controls.

## Compatibility and Deprecation

Additive response fields are allowed; clients must ignore unknown response fields.
Do not repurpose fields or enum meanings. New required request fields need a version
or a migration/default period. Event/webhook schemas carry independent version.
Deprecation publishes replacement, date, telemetry, and sunset headers; removal
waits for supported-client policy and measured usage.

## Contract Tests

- Every OpenAPI operation has auth, success, validation, and stable-error examples.
- Generated server implementation completeness is compiled in CI.
- Golden tests cover serialization for money, timestamps, nullable fields, cursors,
  and errors.
- A compatibility diff blocks unapproved breaking contract changes.
- Fuzz tests target JSON parsing, cursors, identifiers, QR data, and webhook headers.
