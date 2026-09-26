-- name: InsertVenue :one
INSERT INTO venues (id, organization_id, slug, display_name, status, timezone, version, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
RETURNING id, organization_id, slug, display_name, status, timezone, version, created_at, updated_at;

-- name: GetVenue :one
SELECT id, organization_id, slug, display_name, status, timezone, version, created_at, updated_at
FROM venues
WHERE organization_id = $1 AND id = $2;

-- name: GetVenueForUpdate :one
SELECT id, organization_id, slug, display_name, status, timezone, version, created_at, updated_at
FROM venues
WHERE organization_id = $1 AND id = $2
FOR UPDATE;

-- name: ListVenues :many
SELECT id, organization_id, slug, display_name, status, timezone, version, created_at, updated_at
FROM venues
WHERE organization_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- name: ListVenuesAfter :many
SELECT id, organization_id, slug, display_name, status, timezone, version, created_at, updated_at
FROM venues
WHERE organization_id = $1
  AND (created_at, id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: UpdateVenue :one
UPDATE venues
SET slug = $3, display_name = $4, timezone = $5, version = version + 1, updated_at = $6
WHERE organization_id = $1 AND id = $2 AND status <> 'archived' AND version = $7
RETURNING id, organization_id, slug, display_name, status, timezone, version, created_at, updated_at;

-- name: ArchiveVenue :one
UPDATE venues
SET status = 'archived', version = version + 1, updated_at = $4
WHERE organization_id = $1 AND id = $2 AND status <> 'archived' AND version = $3
RETURNING id, organization_id, slug, display_name, status, timezone, version, created_at, updated_at;

-- name: RestoreVenue :one
UPDATE venues
SET status = 'draft', version = version + 1, updated_at = $4
WHERE organization_id = $1 AND id = $2 AND status = 'archived' AND version = $3
RETURNING id, organization_id, slug, display_name, status, timezone, version, created_at, updated_at;

-- name: LockVenue :one
SELECT id, status
FROM venues
WHERE organization_id = $1 AND id = $2
FOR UPDATE;

-- name: InsertSpace :one
INSERT INTO spaces (id, organization_id, venue_id, slug, display_name, status, inventory_mode, physical_capacity, version, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
RETURNING id, organization_id, venue_id, slug, display_name, status, inventory_mode, physical_capacity, version, created_at, updated_at;

-- name: GetSpace :one
SELECT id, organization_id, venue_id, slug, display_name, status, inventory_mode, physical_capacity, version, created_at, updated_at
FROM spaces
WHERE organization_id = $1 AND venue_id = $2 AND id = $3;

-- name: ListSpaces :many
SELECT id, organization_id, venue_id, slug, display_name, status, inventory_mode, physical_capacity, version, created_at, updated_at
FROM spaces
WHERE organization_id = $1 AND venue_id = $2
ORDER BY created_at DESC, id DESC
LIMIT $3;

-- name: ListSpacesAfter :many
SELECT id, organization_id, venue_id, slug, display_name, status, inventory_mode, physical_capacity, version, created_at, updated_at
FROM spaces
WHERE organization_id = $1 AND venue_id = $2
  AND (created_at, id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: InsertGate :one
INSERT INTO gates (id, organization_id, venue_id, space_id, code, display_name, status, version, created_at, updated_at)
VALUES ($1, $2, $3, NULLIF($4, '00000000-0000-0000-0000-000000000000')::uuid, $5, $6, $7, $8, $9, $9)
RETURNING id, organization_id, venue_id, space_id, code, display_name, status, version, created_at, updated_at;

-- name: GetGate :one
SELECT id, organization_id, venue_id, space_id, code, display_name, status, version, created_at, updated_at
FROM gates
WHERE organization_id = $1 AND venue_id = $2 AND id = $3;

-- name: ListGates :many
SELECT id, organization_id, venue_id, space_id, code, display_name, status, version, created_at, updated_at
FROM gates
WHERE organization_id = $1 AND venue_id = $2
ORDER BY created_at DESC, id DESC
LIMIT $3;

-- name: ListGatesAfter :many
SELECT id, organization_id, venue_id, space_id, code, display_name, status, version, created_at, updated_at
FROM gates
WHERE organization_id = $1 AND venue_id = $2
  AND (created_at, id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: InsertGAPoolTemplate :one
INSERT INTO ga_pool_templates (id, organization_id, space_id, slug, display_name, status, physical_capacity, sellable_capacity, version, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
RETURNING id, organization_id, space_id, slug, display_name, status, physical_capacity, sellable_capacity, version, created_at, updated_at;

-- name: GetGAPoolSpace :one
SELECT id, inventory_mode
FROM spaces
WHERE organization_id = $1 AND id = $2;

-- name: GetGAPoolTemplate :one
SELECT id, organization_id, space_id, slug, display_name, status, physical_capacity, sellable_capacity, version, created_at, updated_at
FROM ga_pool_templates
WHERE organization_id = $1 AND space_id = $2 AND id = $3;

-- name: ListGAPoolTemplates :many
SELECT id, organization_id, space_id, slug, display_name, status, physical_capacity, sellable_capacity, version, created_at, updated_at
FROM ga_pool_templates
WHERE organization_id = $1 AND space_id = $2
ORDER BY created_at DESC, id DESC
LIMIT $3;

-- name: ListGAPoolTemplatesAfter :many
SELECT id, organization_id, space_id, slug, display_name, status, physical_capacity, sellable_capacity, version, created_at, updated_at
FROM ga_pool_templates
WHERE organization_id = $1 AND space_id = $2
  AND (created_at, id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: LockSpace :one
SELECT id, status, inventory_mode
FROM spaces
WHERE organization_id = $1 AND id = $2
FOR UPDATE;

-- name: InsertSeatMapVersion :one
INSERT INTO seat_map_versions (id, organization_id, space_id, name, status, revision, checksum, content, seat_count, sellable_count, version, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)
RETURNING id, organization_id, space_id, name, status, revision, checksum, content, seat_count, sellable_count, version, published_at, created_at, updated_at;

-- name: GetSeatMapVersion :one
SELECT id, organization_id, space_id, name, status, revision, checksum, content, seat_count, sellable_count, version, published_at, created_at, updated_at
FROM seat_map_versions
WHERE organization_id = $1 AND space_id = $2 AND id = $3;

-- name: GetSeatMapVersionForUpdate :one
SELECT id, organization_id, space_id, name, status, revision, checksum, content, seat_count, sellable_count, version, published_at, created_at, updated_at
FROM seat_map_versions
WHERE organization_id = $1 AND space_id = $2 AND id = $3
FOR UPDATE;

-- name: ListSeatMapVersions :many
SELECT id, organization_id, space_id, name, status, revision, checksum, content, seat_count, sellable_count, version, published_at, created_at, updated_at
FROM seat_map_versions
WHERE organization_id = $1 AND space_id = $2
ORDER BY created_at DESC, id DESC
LIMIT $3;

-- name: ListSeatMapVersionsAfter :many
SELECT id, organization_id, space_id, name, status, revision, checksum, content, seat_count, sellable_count, version, published_at, created_at, updated_at
FROM seat_map_versions
WHERE organization_id = $1 AND space_id = $2
  AND (created_at, id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: NextSeatMapRevision :one
SELECT COALESCE(MAX(revision), 0)::bigint + 1 AS next_revision
FROM seat_map_versions
WHERE organization_id = $1 AND space_id = $2;

-- name: UpdateSeatMapDraft :one
UPDATE seat_map_versions
SET name = $4, checksum = $5, content = $6, seat_count = $7, sellable_count = $8, version = version + 1, updated_at = $9
WHERE organization_id = $1 AND space_id = $2 AND id = $3 AND status = 'draft' AND version = $10
RETURNING id, organization_id, space_id, name, status, revision, checksum, content, seat_count, sellable_count, version, published_at, created_at, updated_at;

-- name: RetirePublishedSeatMaps :exec
UPDATE seat_map_versions
SET status = 'retired', updated_at = $3
WHERE organization_id = $1 AND space_id = $2 AND status = 'published';

-- name: PublishSeatMapVersion :one
UPDATE seat_map_versions
SET status = 'published', revision = $4, published_at = $5, version = version + 1, updated_at = $5
WHERE organization_id = $1 AND space_id = $2 AND id = $3 AND status = 'draft' AND version = $6
RETURNING id, organization_id, space_id, name, status, revision, checksum, content, seat_count, sellable_count, version, published_at, created_at, updated_at;

-- name: CloneSeatMapVersion :one
INSERT INTO seat_map_versions (id, organization_id, space_id, name, status, revision, checksum, content, seat_count, sellable_count, version, created_at, updated_at)
SELECT $1, source.organization_id, source.space_id, $4, 'draft', 0, source.checksum, source.content, source.seat_count, source.sellable_count, 1, $5, $5
FROM seat_map_versions AS source
WHERE source.organization_id = $2 AND source.space_id = $3 AND source.id = $6 AND source.status IN ('published', 'retired')
RETURNING id, organization_id, space_id, name, status, revision, checksum, content, seat_count, sellable_count, version, published_at, created_at, updated_at;
