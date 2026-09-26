-- name: LockOrganizationForAPIKey :one
SELECT id, status
FROM organizations
WHERE id = sqlc.arg(id)
FOR UPDATE;

-- name: InsertAPIKey :one
INSERT INTO api_keys (
    id, organization_id, prefix, name, secret_hash, scopes,
    created_by_type, created_by_id, expires_at, version, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(organization_id), sqlc.arg(prefix), sqlc.arg(name),
    sqlc.arg(secret_hash), sqlc.arg(scopes), sqlc.arg(created_by_type),
    sqlc.arg(created_by_id), sqlc.arg(expires_at), sqlc.arg(version),
    sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING id, organization_id, prefix, name, secret_hash, scopes,
          created_by_type, created_by_id, expires_at, revoked_at,
          last_used_at, version, created_at, updated_at;

-- name: GetAPIKeyForUpdate :one
SELECT id, organization_id, prefix, name, secret_hash, scopes,
       created_by_type, created_by_id, expires_at, revoked_at,
       last_used_at, version, created_at, updated_at
FROM api_keys
WHERE organization_id = sqlc.arg(organization_id)
  AND id = sqlc.arg(id)
FOR UPDATE;

-- name: GetAPIKey :one
SELECT id, organization_id, prefix, name, secret_hash, scopes,
       created_by_type, created_by_id, expires_at, revoked_at,
       last_used_at, version, created_at, updated_at
FROM api_keys
WHERE organization_id = sqlc.arg(organization_id)
  AND id = sqlc.arg(id);

-- name: LookupAPIKeyByPrefix :one
SELECT id, organization_id, prefix, name, secret_hash, scopes,
       created_by_type, created_by_id, expires_at, revoked_at,
       last_used_at, version, created_at, updated_at
FROM api_keys
WHERE prefix = sqlc.arg(prefix);

-- name: RevokeAPIKey :one
UPDATE api_keys
SET revoked_at = sqlc.arg(revoked_at),
    version = version + 1,
    updated_at = sqlc.arg(updated_at)
WHERE organization_id = sqlc.arg(organization_id)
  AND id = sqlc.arg(id)
  AND version = sqlc.arg(expected_version)
  AND revoked_at IS NULL
RETURNING id, organization_id, prefix, name, secret_hash, scopes,
          created_by_type, created_by_id, expires_at, revoked_at,
          last_used_at, version, created_at, updated_at;

-- name: TouchAPIKey :execrows
UPDATE api_keys
SET last_used_at = sqlc.arg(last_used_at),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND revoked_at IS NULL;
