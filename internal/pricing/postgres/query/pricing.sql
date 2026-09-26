-- name: LockSessionForPricing :one
SELECT id, status
FROM sessions
WHERE organization_id = $1 AND event_id = $2 AND id = $3
FOR UPDATE;

-- name: GetSessionPriceCurrency :one
SELECT currency
FROM price_tiers
WHERE organization_id = $1 AND session_id = $2
ORDER BY created_at ASC, id ASC
LIMIT 1;

-- name: InsertPriceTier :one
INSERT INTO price_tiers (id, organization_id, session_id, slug, display_name, currency, amount_minor, minimum_quantity, maximum_quantity, sales_start_at, sales_end_at, status, version, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14)
RETURNING id, organization_id, session_id, slug, display_name, currency, amount_minor, minimum_quantity, maximum_quantity, sales_start_at, sales_end_at, status, version, created_at, updated_at;

-- name: ListPriceTiers :many
SELECT id, organization_id, session_id, slug, display_name, currency, amount_minor, minimum_quantity, maximum_quantity, sales_start_at, sales_end_at, status, version, created_at, updated_at
FROM price_tiers
WHERE organization_id = $1 AND session_id = $2
ORDER BY created_at DESC, id DESC
LIMIT $3;

-- name: ListPriceTiersAfter :many
SELECT id, organization_id, session_id, slug, display_name, currency, amount_minor, minimum_quantity, maximum_quantity, sales_start_at, sales_end_at, status, version, created_at, updated_at
FROM price_tiers
WHERE organization_id = $1 AND session_id = $2
  AND (created_at, id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);
