-- name: InsertOrganization :one
INSERT INTO organizations (
    id, slug, display_name, status, default_locale, default_timezone,
    default_currency, settings_version, created_at, updated_at
) VALUES (
    $1, $2, $3, 'active', $4, $5,
    $6, 1, $7, $7
)
RETURNING id, slug, display_name, status, default_locale, default_timezone,
          default_currency, settings_version, created_at, updated_at;

-- name: GetOrganization :one
SELECT id, slug, display_name, status, default_locale, default_timezone,
       default_currency, settings_version, created_at, updated_at
FROM organizations
WHERE id = $1;

-- name: InsertPlatformIdempotencyRecord :execrows
INSERT INTO idempotency_records (
    id, organization_id, principal_type, principal_id, operation,
    idempotency_key, request_fingerprint, state, locked_until, expires_at,
    created_at, updated_at
) VALUES (
    $1, NULL, 'platform', $2, 'organization.create',
    $3, $4, 'processing', $5, $6,
    $7, $7
)
ON CONFLICT (organization_id, principal_type, principal_id, operation, idempotency_key)
DO NOTHING;

-- name: GetPlatformIdempotencyRecordForUpdate :one
SELECT id, organization_id, principal_type, principal_id, operation,
       idempotency_key, request_fingerprint, state, locked_until,
       response_status, response_body, resource_type, resource_id,
       expires_at, created_at, updated_at
FROM idempotency_records
WHERE organization_id IS NULL
  AND principal_type = 'platform'
  AND principal_id = $1
  AND operation = 'organization.create'
  AND idempotency_key = $2
FOR UPDATE;

-- name: CompletePlatformIdempotencyRecord :execrows
UPDATE idempotency_records
SET state = 'completed', locked_until = NULL, response_status = 201,
    resource_type = 'organization', resource_id = $2, updated_at = $3
WHERE id = $1 AND organization_id IS NULL AND state = 'processing';
