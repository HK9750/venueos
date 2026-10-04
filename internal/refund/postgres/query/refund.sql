-- name: GetDatabaseTime :one
SELECT now()::timestamptz;

-- name: LockOrderForRefund :one
SELECT id, organization_id, currency, state
FROM orders
WHERE organization_id = $1
  AND id = $2
FOR UPDATE;

-- name: LockPaymentForRefund :one
SELECT id, organization_id, order_id, amount_minor, currency, state
FROM payment_attempts
WHERE organization_id = $1
  AND id = $2
FOR UPDATE;

-- name: SumAllocatedRefunds :one
SELECT COALESCE(SUM(amount_minor), 0)::bigint
FROM refunds
WHERE organization_id = $1
  AND order_id = $2
  AND payment_attempt_id = $3
  AND state IN ('requested', 'processing', 'succeeded');

-- name: InsertRefund :one
INSERT INTO refunds (
    id, organization_id, order_id, payment_attempt_id, amount_minor, currency,
    state, idempotency_key, reason, actor_type, actor_id, version, created_at,
    updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $13)
ON CONFLICT (organization_id, order_id, idempotency_key) DO NOTHING
RETURNING id, organization_id, order_id, payment_attempt_id, amount_minor,
          currency, state, idempotency_key, reason, actor_type, actor_id,
          provider_refund_id, version, created_at, updated_at;

-- name: GetRefundByIdempotency :one
SELECT id, organization_id, order_id, payment_attempt_id, amount_minor,
       currency, state, idempotency_key, reason, actor_type, actor_id,
       provider_refund_id, version, created_at, updated_at
FROM refunds
WHERE organization_id = $1
  AND order_id = $2
  AND idempotency_key = $3
FOR UPDATE;

-- name: GetRefund :one
SELECT id, organization_id, order_id, payment_attempt_id, amount_minor,
       currency, state, idempotency_key, reason, actor_type, actor_id,
       provider_refund_id, version, created_at, updated_at
FROM refunds
WHERE organization_id = $1
  AND id = $2;

-- name: LockRefundExecution :one
SELECT r.id, r.organization_id, r.order_id, r.payment_attempt_id,
       r.amount_minor, r.currency, r.state, r.idempotency_key, r.reason,
       r.actor_type, r.actor_id, r.provider_refund_id, r.version,
       r.created_at, r.updated_at, p.provider, p.provider_object_id
FROM refunds AS r
JOIN payment_attempts AS p
  ON p.organization_id = r.organization_id AND p.id = r.payment_attempt_id
WHERE r.organization_id = $1 AND r.id = $2
FOR UPDATE OF r, p;

-- name: MarkRefundProcessing :one
UPDATE refunds
SET state = 'processing', version = version + 1, updated_at = $3
WHERE organization_id = $1 AND id = $2 AND version = $4 AND state = 'requested'
RETURNING id, organization_id, order_id, payment_attempt_id, amount_minor,
          currency, state, idempotency_key, reason, actor_type, actor_id,
          provider_refund_id, version, created_at, updated_at;

-- name: FinalizeRefund :one
UPDATE refunds
SET state = $3, provider_refund_id = $4, version = version + 1, updated_at = $5
WHERE organization_id = $1 AND id = $2 AND version = $6 AND state = 'processing'
RETURNING id, organization_id, order_id, payment_attempt_id, amount_minor,
          currency, state, idempotency_key, reason, actor_type, actor_id,
          provider_refund_id, version, created_at, updated_at;
