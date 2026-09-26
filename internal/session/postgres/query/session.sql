-- name: LockSpaceForSession :one
SELECT id, venue_id, status, inventory_mode
FROM spaces
WHERE organization_id = $1 AND venue_id = $2 AND id = $3
FOR UPDATE;

-- name: GetPublishedEventRevision :one
SELECT id
FROM event_revisions
WHERE organization_id = $1 AND event_id = $2 AND revision = $3 AND status = 'published';

-- name: GetPublishedSeatMapVersion :one
SELECT id
FROM seat_map_versions
WHERE organization_id = $1 AND space_id = $2 AND id = $3 AND status = 'published';

-- name: GetPublishedSeatMapForSession :one
SELECT id, content
FROM seat_map_versions
WHERE organization_id = $1 AND space_id = $2 AND id = $3 AND status = 'published';

-- name: ListActiveGAPoolTemplatesForSession :many
SELECT id, slug, display_name, physical_capacity, sellable_capacity
FROM ga_pool_templates
WHERE organization_id = $1 AND space_id = $2 AND status = 'active'
ORDER BY id ASC
FOR SHARE;

-- name: InsertSessionGAPool :exec
INSERT INTO session_ga_pools (
    id, organization_id, session_id, source_pool_id, slug, display_name,
    physical_capacity, sellable_capacity, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9);

-- name: InsertSessionSeat :exec
INSERT INTO session_seats (
    id, organization_id, session_id, seat_map_version_id, section_key, row_key,
    seat_key, label, category, sellable, wheelchair, companion_to, state,
    version, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, 'available', 1, $13, $13);

-- name: SessionOverlap :one
SELECT EXISTS (
    SELECT 1
    FROM sessions
    WHERE organization_id = $1
      AND venue_id = $2
      AND space_id = $3
      AND status NOT IN ('cancelled', 'completed')
      AND starts_at < sqlc.arg(new_ends_at)::timestamptz
      AND ends_at > sqlc.arg(new_starts_at)::timestamptz
) AS overlaps;

-- name: InsertSession :one
INSERT INTO sessions (id, organization_id, event_id, event_revision, venue_id, space_id, seat_map_version_id, inventory_mode, doors_at, starts_at, ends_at, sales_start_at, sales_end_at, timezone, status, version, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $17)
RETURNING id, organization_id, event_id, event_revision, venue_id, space_id, seat_map_version_id, inventory_mode, doors_at, starts_at, ends_at, sales_start_at, sales_end_at, timezone, status, version, created_at, updated_at, inventory_revision;

-- name: GetSession :one
SELECT id, organization_id, event_id, event_revision, venue_id, space_id, seat_map_version_id, inventory_mode, doors_at, starts_at, ends_at, sales_start_at, sales_end_at, timezone, status, version, created_at, updated_at, inventory_revision
FROM sessions
WHERE organization_id = $1 AND event_id = $2 AND id = $3;

-- name: ListSessions :many
SELECT id, organization_id, event_id, event_revision, venue_id, space_id, seat_map_version_id, inventory_mode, doors_at, starts_at, ends_at, sales_start_at, sales_end_at, timezone, status, version, created_at, updated_at, inventory_revision
FROM sessions
WHERE organization_id = $1 AND event_id = $2
ORDER BY created_at DESC, id DESC
LIMIT $3;

-- name: ListSessionsAfter :many
SELECT id, organization_id, event_id, event_revision, venue_id, space_id, seat_map_version_id, inventory_mode, doors_at, starts_at, ends_at, sales_start_at, sales_end_at, timezone, status, version, created_at, updated_at, inventory_revision
FROM sessions
WHERE organization_id = $1 AND event_id = $2
  AND (created_at, id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);
