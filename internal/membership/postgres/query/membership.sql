-- name: ListMemberships :many
SELECT id, organization_id, user_id, role, status, venue_scope, version,
       created_at, updated_at, revoked_at
FROM memberships
WHERE organization_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- name: ListMembershipsAfter :many
SELECT id, organization_id, user_id, role, status, venue_scope, version,
       created_at, updated_at, revoked_at
FROM memberships
WHERE organization_id = $1
  AND (created_at, id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: LockOrganization :one
SELECT id
FROM organizations
WHERE id = $1
FOR UPDATE;

-- name: GetMembershipForUpdate :one
SELECT id, organization_id, user_id, role, status, venue_scope, version,
       created_at, updated_at, revoked_at
FROM memberships
WHERE organization_id = $1 AND id = $2
FOR UPDATE;

-- name: CountActiveOwners :one
SELECT count(*)::bigint
FROM memberships
WHERE organization_id = $1 AND status = 'active' AND role = 'owner';

-- name: UpdateMembershipRole :one
UPDATE memberships
SET role = $4, version = version + 1, updated_at = $5
WHERE organization_id = $1 AND id = $2 AND status = 'active' AND version = $3
RETURNING id, organization_id, user_id, role, status, venue_scope, version,
          created_at, updated_at, revoked_at;

-- name: RevokeMembership :one
UPDATE memberships
SET status = 'revoked', version = version + 1, updated_at = $4, revoked_at = $4
WHERE organization_id = $1 AND id = $2 AND status = 'active' AND version = $3
RETURNING id, organization_id, user_id, role, status, venue_scope, version,
          created_at, updated_at, revoked_at;
