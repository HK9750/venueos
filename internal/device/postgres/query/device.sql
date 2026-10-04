-- name: ListDevices :many
SELECT id, organization_id, venue_id, name, platform, app_version,
       state, last_seen_at, version, created_at, updated_at
FROM devices
WHERE organization_id = sqlc.arg(organization_id)
  AND (
      sqlc.narg(after_created_at)::timestamptz IS NULL
      OR (created_at, id) < (sqlc.narg(after_created_at)::timestamptz, sqlc.narg(after_id)::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(limit_count);

-- name: InsertDevice :one
INSERT INTO devices (
    id, organization_id, venue_id, name, platform, app_version,
    credential_hash, state, version, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(organization_id), sqlc.arg(venue_id), sqlc.arg(name),
    sqlc.arg(platform), sqlc.arg(app_version), sqlc.arg(credential_hash),
    sqlc.arg(state), sqlc.arg(version), sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING id, organization_id, venue_id, name, platform, app_version,
          state, last_seen_at, version, created_at, updated_at;

-- name: LockVenueForDevice :one
SELECT id, status
FROM venues
WHERE organization_id = sqlc.arg(organization_id)
  AND id = sqlc.arg(venue_id)
FOR UPDATE;

-- name: GetDevice :one
SELECT id, organization_id, venue_id, name, platform, app_version,
       state, last_seen_at, version, created_at, updated_at
FROM devices
WHERE organization_id = sqlc.arg(organization_id)
  AND id = sqlc.arg(id);

-- name: LookupDeviceByCredentialHash :one
SELECT id, organization_id, venue_id, name, platform, app_version,
       state, last_seen_at, version, created_at, updated_at
FROM devices
WHERE credential_hash = sqlc.arg(credential_hash);

-- name: GetDeviceForUpdate :one
SELECT id, organization_id, venue_id, name, platform, app_version,
       state, last_seen_at, version, created_at, updated_at
FROM devices
WHERE organization_id = sqlc.arg(organization_id)
  AND id = sqlc.arg(id)
FOR UPDATE;

-- name: ChangeDeviceState :one
UPDATE devices
SET state = sqlc.arg(state), version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE organization_id = sqlc.arg(organization_id)
  AND id = sqlc.arg(id)
  AND version = sqlc.arg(expected_version)
RETURNING id, organization_id, venue_id, name, platform, app_version,
          state, last_seen_at, version, created_at, updated_at;

-- name: ListDeviceAssignments :many
SELECT id, organization_id, device_id, session_id, gate_id, capability,
       state, valid_from, valid_until, version, created_at, updated_at
FROM device_assignments
WHERE organization_id = sqlc.arg(organization_id)
  AND device_id = sqlc.arg(device_id)
  AND (
      sqlc.narg(after_created_at)::timestamptz IS NULL
      OR (created_at, id) < (sqlc.narg(after_created_at)::timestamptz, sqlc.narg(after_id)::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(limit_count);

-- name: LockDeviceForAssignment :one
SELECT id, state
FROM devices
WHERE organization_id = sqlc.arg(organization_id)
  AND id = sqlc.arg(device_id)
FOR UPDATE;

-- name: InsertDeviceAssignment :one
INSERT INTO device_assignments (
    id, organization_id, device_id, session_id, gate_id, capability,
    state, valid_from, valid_until, version, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(organization_id), sqlc.arg(device_id),
    NULLIF(sqlc.arg(session_id), '00000000-0000-0000-0000-000000000000')::uuid,
    NULLIF(sqlc.arg(gate_id), '00000000-0000-0000-0000-000000000000')::uuid,
    sqlc.arg(capability), sqlc.arg(state), sqlc.arg(valid_from),
    sqlc.arg(valid_until), sqlc.arg(version), sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING id, organization_id, device_id, session_id, gate_id, capability,
          state, valid_from, valid_until, version, created_at, updated_at;

-- name: GetDeviceAssignmentForUpdate :one
SELECT id, organization_id, device_id, session_id, gate_id, capability,
       state, valid_from, valid_until, version, created_at, updated_at
FROM device_assignments
WHERE organization_id = sqlc.arg(organization_id)
  AND device_id = sqlc.arg(device_id)
  AND id = sqlc.arg(id)
FOR UPDATE;

-- name: RevokeDeviceAssignment :one
UPDATE device_assignments
SET state = 'revoked', version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE organization_id = sqlc.arg(organization_id)
  AND device_id = sqlc.arg(device_id)
  AND id = sqlc.arg(id)
  AND version = sqlc.arg(expected_version)
  AND state = 'active'
RETURNING id, organization_id, device_id, session_id, gate_id, capability,
          state, valid_from, valid_until, version, created_at, updated_at;
