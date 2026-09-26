-- name: LockSessionForGAHold :one
SELECT id, status, inventory_mode, inventory_revision
FROM sessions
WHERE organization_id = $1 AND id = $2
FOR UPDATE;

-- name: GetDatabaseTime :one
SELECT now()::timestamptz;

-- name: GetAvailabilitySession :one
SELECT id, organization_id, inventory_mode, status, inventory_revision, now()::timestamptz AS server_time
FROM sessions
WHERE organization_id = $1 AND id = $2;

-- name: ListSessionSeatAvailability :many
SELECT id, seat_key, label, category, sellable, wheelchair, state, version
FROM session_seats
WHERE organization_id = $1 AND session_id = $2
ORDER BY seat_key ASC, id ASC;

-- name: ListSessionGAPoolAvailability :many
SELECT id, slug, sellable_capacity, sold_qty, held_qty, killed_qty, comped_qty, version
FROM session_ga_pools
WHERE organization_id = $1 AND session_id = $2
ORDER BY slug ASC, id ASC;

-- name: ListAvailabilityEventsAfter :many
SELECT id, session_id, sequence, event_type, schema_version, payload, occurred_at
FROM realtime_events
WHERE organization_id = $1
  AND session_id = $2
  AND topic = 'availability'
  AND sequence > sqlc.arg(after_sequence)
ORDER BY sequence ASC
LIMIT sqlc.arg(page_limit);

-- name: GetAvailabilityEventBounds :one
SELECT COALESCE(MIN(sequence), 0)::bigint AS first_sequence,
       COALESCE(MAX(sequence), 0)::bigint AS last_sequence
FROM realtime_events
WHERE organization_id = $1
  AND session_id = $2
  AND topic = 'availability';

-- name: LockSessionSeatsForHold :many
SELECT id, organization_id, session_id, seat_map_version_id, state, sellable, hold_id, hold_expires_at, version
FROM session_seats
WHERE organization_id = sqlc.arg(organization_id)
  AND session_id = sqlc.arg(session_id)
  AND id = ANY(sqlc.arg(seat_ids)::uuid[])
ORDER BY id ASC
FOR UPDATE;

-- name: UpdateGAPoolForHold :one
UPDATE session_ga_pools
SET held_qty = held_qty + sqlc.arg(quantity), version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE organization_id = sqlc.arg(organization_id)
  AND session_id = sqlc.arg(session_id)
  AND id = sqlc.arg(pool_id)
  AND sellable_capacity - sold_qty - held_qty - killed_qty - comped_qty >= sqlc.arg(quantity)
RETURNING id, organization_id, session_id, held_qty, version;

-- name: InsertHold :one
INSERT INTO holds (id, organization_id, session_id, owner_token_hash, owner_user_id, channel_id, currency, state, expires_at, renewal_count, version, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $12)
RETURNING id, organization_id, session_id, owner_token_hash, owner_user_id, channel_id, currency, state, expires_at, renewal_count, version, created_at, updated_at;

-- name: InsertHoldItem :one
INSERT INTO hold_items (id, organization_id, hold_id, session_id, pool_id, seat_id, quantity, created_at)
VALUES ($1, $2, $3, $4, sqlc.arg(pool_id), sqlc.arg(seat_id), sqlc.arg(quantity), sqlc.arg(created_at))
RETURNING id, organization_id, hold_id, session_id, pool_id, seat_id, quantity, created_at;

-- name: IncrementSessionInventoryRevision :one
UPDATE sessions
SET inventory_revision = inventory_revision + 1, updated_at = sqlc.arg(updated_at)
WHERE organization_id = sqlc.arg(organization_id) AND id = sqlc.arg(session_id)
RETURNING inventory_revision;

-- name: LockHoldForOwner :one
SELECT id, organization_id, session_id, owner_token_hash, owner_user_id, channel_id, currency, state, expires_at, renewal_count, version, created_at, updated_at
FROM holds
WHERE organization_id = $1 AND id = $2 AND owner_token_hash = $3
FOR UPDATE;

-- name: LockDueHolds :many
SELECT id, organization_id, session_id
FROM holds
WHERE state = 'active'
  AND expires_at <= sqlc.arg(now)
ORDER BY expires_at ASC, id ASC
LIMIT sqlc.arg(batch_limit)
FOR UPDATE SKIP LOCKED;

-- name: ListHoldItems :many
SELECT id, organization_id, hold_id, session_id, pool_id, seat_id, quantity, created_at
FROM hold_items
WHERE organization_id = $1 AND hold_id = $2
ORDER BY id ASC
FOR UPDATE;

-- name: ReleaseGAPool :execrows
UPDATE session_ga_pools
SET held_qty = held_qty - sqlc.arg(quantity), version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE organization_id = sqlc.arg(organization_id)
  AND session_id = sqlc.arg(session_id)
  AND id = sqlc.arg(pool_id)
  AND held_qty >= sqlc.arg(quantity);

-- name: HoldSessionSeat :execrows
UPDATE session_seats
SET state = 'held', hold_id = sqlc.arg(hold_id), hold_expires_at = sqlc.arg(hold_expires_at), version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE organization_id = sqlc.arg(organization_id)
  AND session_id = sqlc.arg(session_id)
  AND id = sqlc.arg(seat_id)
  AND state = 'available'
  AND sellable = true;

-- name: ReleaseSessionSeat :execrows
UPDATE session_seats
SET state = 'available', hold_id = NULL, hold_expires_at = NULL, version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE organization_id = sqlc.arg(organization_id)
  AND session_id = sqlc.arg(session_id)
  AND id = sqlc.arg(seat_id)
  AND state = 'held'
  AND hold_id = sqlc.arg(hold_id);

-- name: UpdateHoldState :one
UPDATE holds
SET state = sqlc.arg(state), version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE organization_id = sqlc.arg(organization_id) AND id = sqlc.arg(id) AND state = 'active'
RETURNING id, organization_id, session_id, owner_token_hash, owner_user_id, channel_id, currency, state, expires_at, renewal_count, version, created_at, updated_at;

-- name: UpdateHoldForModification :one
UPDATE holds
SET version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE organization_id = sqlc.arg(organization_id)
  AND id = sqlc.arg(id)
  AND state = 'active'
  AND version = sqlc.arg(expected_version)
RETURNING id, organization_id, session_id, owner_token_hash, owner_user_id, channel_id, currency, state, expires_at, renewal_count, version, created_at, updated_at;

-- name: RenewHold :one
UPDATE holds
SET expires_at = sqlc.arg(new_expires_at), renewal_count = renewal_count + 1, version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE organization_id = sqlc.arg(organization_id)
  AND id = sqlc.arg(id)
  AND state = 'active'
  AND version = sqlc.arg(expected_version)
  AND renewal_count < 1
  AND expires_at > sqlc.arg(database_now)
RETURNING id, organization_id, session_id, owner_token_hash, owner_user_id, channel_id, currency, state, expires_at, renewal_count, version, created_at, updated_at;

-- name: ExpireHold :one
UPDATE holds
SET state = 'expired', version = version + 1, updated_at = sqlc.arg(updated_at)
WHERE organization_id = sqlc.arg(organization_id) AND id = sqlc.arg(id) AND state = 'active'
RETURNING id, organization_id, session_id, version, updated_at;

-- name: DeleteHoldItems :execrows
DELETE FROM hold_items
WHERE organization_id = sqlc.arg(organization_id)
  AND hold_id = sqlc.arg(hold_id);
