-- name: InsertEvent :one
INSERT INTO events (id, organization_id, slug, status, current_revision, version, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
RETURNING id, organization_id, slug, status, current_revision, version, created_at, updated_at;

-- name: InsertEventRevision :one
INSERT INTO event_revisions (id, organization_id, event_id, revision, status, title, short_description, long_description, checksum, version, created_at, updated_at)
VALUES ($1, $2, $3, 0, 'draft', $4, $5, $6, $7, 1, $8, $8)
RETURNING id;

-- name: GetEventProjection :one
SELECT e.id, e.organization_id, e.slug, e.status, e.current_revision, e.version, e.created_at, e.updated_at,
       COALESCE(r.title, '') AS title,
       COALESCE(r.short_description, '') AS short_description,
       COALESCE(r.long_description, '') AS long_description,
       COALESCE(r.checksum, ''::bytea) AS checksum,
       r.published_at
FROM events AS e
LEFT JOIN event_revisions AS r
  ON r.organization_id = e.organization_id AND r.event_id = e.id AND r.revision = e.current_revision
WHERE e.organization_id = $1 AND e.id = $2;

-- name: GetEventForUpdate :one
SELECT id, organization_id, slug, status, current_revision, version, created_at, updated_at
FROM events
WHERE organization_id = $1 AND id = $2
FOR UPDATE;

-- name: ListEvents :many
SELECT e.id, e.organization_id, e.slug, e.status, e.current_revision, e.version, e.created_at, e.updated_at,
       COALESCE(r.title, '') AS title,
       COALESCE(r.short_description, '') AS short_description,
       COALESCE(r.long_description, '') AS long_description,
       COALESCE(r.checksum, ''::bytea) AS checksum,
       r.published_at
FROM events AS e
LEFT JOIN event_revisions AS r
  ON r.organization_id = e.organization_id AND r.event_id = e.id AND r.revision = e.current_revision
WHERE e.organization_id = $1
ORDER BY e.created_at DESC, e.id DESC
LIMIT $2;

-- name: ListEventsAfter :many
SELECT e.id, e.organization_id, e.slug, e.status, e.current_revision, e.version, e.created_at, e.updated_at,
       COALESCE(r.title, '') AS title,
       COALESCE(r.short_description, '') AS short_description,
       COALESCE(r.long_description, '') AS long_description,
       COALESCE(r.checksum, ''::bytea) AS checksum,
       r.published_at
FROM events AS e
LEFT JOIN event_revisions AS r
  ON r.organization_id = e.organization_id AND r.event_id = e.id AND r.revision = e.current_revision
WHERE e.organization_id = $1
  AND (e.created_at, e.id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
ORDER BY e.created_at DESC, e.id DESC
LIMIT sqlc.arg(page_limit);

-- name: GetDraftRevisionForUpdate :one
SELECT id, version
FROM event_revisions
WHERE organization_id = $1 AND event_id = $2 AND revision = 0 AND status = 'draft'
FOR UPDATE;

-- name: UpdateEventIdentity :one
UPDATE events
SET slug = $3, version = version + 1, updated_at = $4
WHERE organization_id = $1 AND id = $2 AND status NOT IN ('cancelled', 'archived') AND version = $5
RETURNING id, organization_id, slug, status, current_revision, version, created_at, updated_at;

-- name: UpdateEventDraftRevision :exec
UPDATE event_revisions
SET title = $3, short_description = $4, long_description = $5, checksum = $6, version = version + 1, updated_at = $7
WHERE organization_id = $1 AND event_id = $2 AND revision = 0 AND status = 'draft' AND version = $8;

-- name: NextEventRevision :one
SELECT COALESCE(MAX(revision), 0)::bigint + 1 AS next_revision
FROM event_revisions
WHERE organization_id = $1 AND event_id = $2;

-- name: RetirePublishedEventRevisions :exec
UPDATE event_revisions
SET status = 'retired', updated_at = $3
WHERE organization_id = $1 AND event_id = $2 AND status = 'published';

-- name: PublishEventRevision :exec
UPDATE event_revisions
SET status = 'published', revision = $3, published_at = $4, version = version + 1, updated_at = $4
WHERE organization_id = $1 AND event_id = $2 AND revision = 0 AND status = 'draft' AND version = $5;

-- name: PublishEventAggregate :exec
UPDATE events
SET status = 'published', current_revision = $3, version = version + 1, updated_at = $4
WHERE organization_id = $1 AND id = $2 AND version = $5;

-- name: CloneEventDraft :one
INSERT INTO event_revisions (id, organization_id, event_id, revision, status, title, short_description, long_description, checksum, version, created_at, updated_at)
SELECT $1, source.organization_id, source.event_id, 0, 'draft', source.title, source.short_description, source.long_description, source.checksum, 1, $4, $4
FROM event_revisions AS source
JOIN events AS e ON e.organization_id = source.organization_id AND e.id = source.event_id AND e.current_revision = source.revision
WHERE source.organization_id = $2 AND source.event_id = $3 AND source.status IN ('published', 'retired')
RETURNING id;

-- name: IncrementEventVersion :exec
UPDATE events
SET version = version + 1, updated_at = $3
WHERE organization_id = $1 AND id = $2;
