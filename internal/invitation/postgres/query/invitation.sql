-- name: InsertInvitation :one
INSERT INTO invitations (
    id, organization_id, normalized_email, role, token_hash, status, expires_at,
    created_by_type, created_by_id, version, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, 'invited', $6, $7, NULLIF($8::text, ''), $9, $10, $10)
RETURNING id, organization_id, normalized_email, role, status, expires_at,
          created_by_type, created_by_id, membership_id, accepted_at, revoked_at,
          version, created_at, updated_at;

-- name: GetInvitationForUpdateByToken :one
SELECT id, organization_id, normalized_email, role, status, expires_at,
       created_by_type, created_by_id, membership_id, accepted_at, revoked_at,
       version, created_at, updated_at
FROM invitations
WHERE token_hash = $1
FOR UPDATE;

-- name: GetInvitationForUpdate :one
SELECT id, organization_id, normalized_email, role, status, expires_at,
       created_by_type, created_by_id, membership_id, accepted_at, revoked_at,
       version, created_at, updated_at
FROM invitations
WHERE organization_id = $1 AND id = $2
FOR UPDATE;

-- name: LockOrganizationForInvitation :one
SELECT id, status
FROM organizations
WHERE id = $1
FOR UPDATE;

-- name: GetUserForInvitation :one
SELECT id, email
FROM users
WHERE id = $1
FOR UPDATE;

-- name: GetMembershipForInvitation :one
SELECT id, status
FROM memberships
WHERE organization_id = $1 AND user_id = $2
FOR UPDATE;

-- name: InsertMembershipFromInvitation :one
INSERT INTO memberships (
    id, organization_id, user_id, role, status, venue_scope, version, created_at, updated_at
)
VALUES ($1, $2, $3, $4, 'active', '{}', 1, $5, $5)
RETURNING id, organization_id, user_id, role, status, venue_scope, version,
          created_at, updated_at, revoked_at;

-- name: AcceptInvitation :one
UPDATE invitations
SET status = 'accepted', membership_id = $2, accepted_at = $3, version = version + 1, updated_at = $3
WHERE id = $1 AND status = 'invited' AND version = $4
RETURNING id, organization_id, normalized_email, role, status, expires_at,
          created_by_type, created_by_id, membership_id, accepted_at, revoked_at,
          version, created_at, updated_at;

-- name: RevokeInvitation :one
UPDATE invitations
SET status = 'revoked', revoked_at = $4, version = version + 1, updated_at = $4
WHERE organization_id = $1 AND id = $2 AND status = 'invited' AND version = $3
RETURNING id, organization_id, normalized_email, role, status, expires_at,
          created_by_type, created_by_id, membership_id, accepted_at, revoked_at,
          version, created_at, updated_at;

-- name: ListDueInvitations :many
SELECT id, organization_id, version
FROM invitations
WHERE status = 'invited' AND expires_at <= $1
ORDER BY expires_at ASC, id ASC
LIMIT $2
FOR UPDATE SKIP LOCKED;

-- name: ExpireInvitation :one
UPDATE invitations
SET status = 'expired', version = version + 1, updated_at = $2
WHERE id = $1 AND status = 'invited' AND version = $3
RETURNING id, organization_id, normalized_email, role, status, expires_at,
          created_by_type, created_by_id, membership_id, accepted_at, revoked_at,
          version, created_at, updated_at;
