-- name: GetDatabaseTime :one
SELECT now()::timestamptz;

-- name: LockHoldForCart :one
SELECT id, organization_id, session_id, currency, state, expires_at
FROM holds
WHERE organization_id = $1
  AND id = $2
  AND owner_token_hash = $3
FOR UPDATE;

-- name: InsertCart :one
INSERT INTO carts (
    id, organization_id, session_id, hold_id, owner_token_hash, owner_user_id,
    currency, state, quote_snapshot, quote_snapshot_raw, quote_sha256, version, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $13)
RETURNING id, organization_id, session_id, hold_id, owner_token_hash, owner_user_id,
          currency, state, quote_snapshot, quote_snapshot_raw, quote_sha256, version, created_at, updated_at;

-- name: GetCartForOwner :one
SELECT id, organization_id, session_id, hold_id, owner_token_hash, owner_user_id,
       currency, state, quote_snapshot, quote_snapshot_raw, quote_sha256, version, created_at, updated_at
FROM carts
WHERE organization_id = $1
  AND id = $2
  AND owner_token_hash = $3;

-- name: LockCartForOwner :one
SELECT id, organization_id, session_id, hold_id, owner_token_hash, owner_user_id,
       currency, state, quote_snapshot, quote_snapshot_raw, quote_sha256, version, created_at, updated_at
FROM carts
WHERE organization_id = $1
  AND id = $2
  AND owner_token_hash = $3
FOR UPDATE;

-- name: UpdateCartQuote :one
UPDATE carts
SET quote_snapshot = $1,
    quote_snapshot_raw = $2,
    quote_sha256 = $3,
    version = version + 1,
    updated_at = $4
 WHERE organization_id = $5
  AND id = $6
  AND owner_token_hash = $7
  AND state = 'active'
  AND version = $8
RETURNING id, organization_id, session_id, hold_id, owner_token_hash, owner_user_id,
          currency, state, quote_snapshot, quote_snapshot_raw, quote_sha256, version, created_at, updated_at;
