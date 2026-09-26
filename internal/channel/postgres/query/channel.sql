-- name: InsertSalesChannel :one
INSERT INTO sales_channels (id, organization_id, channel_key, display_name, channel_type, configuration, status, version, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
RETURNING id, organization_id, channel_key, display_name, channel_type, configuration, status, version, created_at, updated_at;

-- name: ListSalesChannels :many
SELECT id, organization_id, channel_key, display_name, channel_type, configuration, status, version, created_at, updated_at
FROM sales_channels
WHERE organization_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- name: ListSalesChannelsAfter :many
SELECT id, organization_id, channel_key, display_name, channel_type, configuration, status, version, created_at, updated_at
FROM sales_channels
WHERE organization_id = $1
  AND (created_at, id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: LockSessionForAllocation :one
SELECT s.id, s.space_id, sp.physical_capacity
FROM sessions s
JOIN spaces sp ON sp.organization_id = s.organization_id AND sp.id = s.space_id
WHERE s.organization_id = $1 AND s.event_id = $2 AND s.id = $3
FOR UPDATE OF s;

-- name: SumHardSessionAllocations :one
SELECT COALESCE(SUM(quantity), 0)::bigint
FROM session_channel_allocations
WHERE organization_id = $1 AND session_id = $2 AND allocation_mode = 'hard_reserved';

-- name: InsertSessionChannelAllocation :one
INSERT INTO session_channel_allocations (id, organization_id, session_id, channel_id, scope_key, allocation_mode, quantity, version, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
RETURNING id, organization_id, session_id, channel_id, scope_key, allocation_mode, quantity, version, created_at, updated_at;

-- name: ListSessionChannelAllocations :many
SELECT id, organization_id, session_id, channel_id, scope_key, allocation_mode, quantity, version, created_at, updated_at
FROM session_channel_allocations
WHERE organization_id = $1 AND session_id = $2
ORDER BY created_at DESC, id DESC
LIMIT $3;

-- name: ListSessionChannelAllocationsAfter :many
SELECT id, organization_id, session_id, channel_id, scope_key, allocation_mode, quantity, version, created_at, updated_at
FROM session_channel_allocations
WHERE organization_id = $1 AND session_id = $2
  AND (created_at, id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);
