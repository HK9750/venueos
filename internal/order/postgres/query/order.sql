-- name: GetDatabaseTime :one
SELECT now()::timestamptz;

-- name: LockCartForCheckout :one
SELECT id, organization_id, session_id, hold_id, owner_token_hash, owner_user_id,
       currency, state, quote_snapshot, quote_snapshot_raw, quote_sha256, version, created_at, updated_at
FROM carts
WHERE organization_id = $1
  AND id = $2
  AND owner_token_hash = $3
FOR UPDATE;

-- name: LockHoldForCheckout :one
SELECT id, organization_id, session_id, owner_token_hash, currency, state, expires_at
FROM holds
WHERE organization_id = $1
  AND id = $2
  AND owner_token_hash = $3
FOR UPDATE;

-- name: InsertOrder :one
INSERT INTO orders (
    id, organization_id, cart_id, session_id, hold_id, order_number,
    owner_token_hash, owner_user_id, currency, state, subtotal_minor,
    discount_minor, fees_minor, taxes_minor, total_minor, quote_snapshot,
    quote_snapshot_raw, quote_sha256, version, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $20)
RETURNING id, organization_id, cart_id, session_id, hold_id, order_number,
          owner_token_hash, owner_user_id, currency, state, subtotal_minor,
          discount_minor, fees_minor, taxes_minor, total_minor, quote_snapshot,
          quote_snapshot_raw, quote_sha256, version, created_at, updated_at, confirmed_at;

-- name: InsertOrderLine :exec
INSERT INTO order_lines (
    id, organization_id, order_id, line_number, price_tier_id, quantity,
    unit_minor, subtotal_minor, snapshot, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: MarkCartCheckedOut :one
UPDATE carts
SET state = 'checked_out', version = version + 1, updated_at = $3
WHERE organization_id = $1
  AND id = $2
  AND owner_token_hash = $4
  AND state = 'active'
  AND version = $5
RETURNING id, organization_id, session_id, hold_id, owner_token_hash, owner_user_id,
          currency, state, quote_snapshot, quote_snapshot_raw, quote_sha256, version, created_at, updated_at;

-- name: GetOrderForOwner :one
SELECT id, organization_id, cart_id, session_id, hold_id, order_number,
       owner_token_hash, owner_user_id, currency, state, subtotal_minor,
       discount_minor, fees_minor, taxes_minor, total_minor, quote_snapshot,
       quote_snapshot_raw, quote_sha256, version, created_at, updated_at, confirmed_at
FROM orders
WHERE organization_id = $1
  AND id = $2
  AND owner_token_hash = $3;

-- name: GetOrderForStaff :one
SELECT id, organization_id, cart_id, session_id, hold_id, order_number,
       owner_token_hash, owner_user_id, currency, state, subtotal_minor,
       discount_minor, fees_minor, taxes_minor, total_minor, quote_snapshot,
       quote_snapshot_raw, quote_sha256, version, created_at, updated_at, confirmed_at
FROM orders
WHERE organization_id = $1
  AND id = $2;

-- name: ListOrdersForStaff :many
SELECT id, organization_id, cart_id, session_id, hold_id, order_number,
       owner_token_hash, owner_user_id, currency, state, subtotal_minor,
       discount_minor, fees_minor, taxes_minor, total_minor, quote_snapshot,
       quote_snapshot_raw, quote_sha256, version, created_at, updated_at, confirmed_at
FROM orders
WHERE organization_id = $1
ORDER BY created_at DESC, id DESC
LIMIT $2;

-- name: ListOrdersForStaffAfter :many
SELECT id, organization_id, cart_id, session_id, hold_id, order_number,
       owner_token_hash, owner_user_id, currency, state, subtotal_minor,
       discount_minor, fees_minor, taxes_minor, total_minor, quote_snapshot,
       quote_snapshot_raw, quote_sha256, version, created_at, updated_at, confirmed_at
FROM orders
WHERE organization_id = $1
  AND (created_at, id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: ListOrderLines :many
SELECT id, organization_id, order_id, line_number, price_tier_id, quantity,
       unit_minor, subtotal_minor, snapshot, created_at
FROM order_lines
WHERE organization_id = $1
  AND order_id = $2
ORDER BY line_number ASC, id ASC;
