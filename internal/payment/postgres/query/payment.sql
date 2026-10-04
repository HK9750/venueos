-- name: GetDatabaseTime :one
SELECT now()::timestamptz;

-- name: LockOrderForPayment :one
SELECT id, organization_id, currency, state, total_minor
FROM orders
WHERE organization_id = $1
  AND id = $2
FOR UPDATE;

-- name: InsertPaymentAttempt :one
INSERT INTO payment_attempts (
    id, organization_id, order_id, provider, provider_object_id, amount_minor,
    currency, state, idempotency_key, failure_class, failure_code,
    failure_message, client_action_reference, metadata, version, created_at,
    updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $16)
RETURNING id, organization_id, order_id, provider, provider_object_id, amount_minor,
          currency, state, idempotency_key, failure_class, failure_code,
          failure_message, client_action_reference, metadata, version, created_at,
          updated_at;

-- name: GetPaymentAttempt :one
SELECT id, organization_id, order_id, provider, provider_object_id, amount_minor,
       currency, state, idempotency_key, failure_class, failure_code,
       failure_message, client_action_reference, metadata, version, created_at,
       updated_at
FROM payment_attempts
WHERE organization_id = $1
  AND id = $2;

-- name: LockPaymentAttempt :one
SELECT id, organization_id, order_id, provider, provider_object_id, amount_minor,
       currency, state, idempotency_key, failure_class, failure_code,
       failure_message, client_action_reference, metadata, version, created_at,
       updated_at
FROM payment_attempts
WHERE organization_id = $1
  AND id = $2
FOR UPDATE;

-- name: UpdatePaymentAttempt :one
UPDATE payment_attempts
SET provider_object_id = $3,
    state = $4,
    failure_class = $5,
    failure_code = $6,
    failure_message = $7,
    client_action_reference = $8,
    metadata = $9,
    version = version + 1,
    updated_at = $10
WHERE organization_id = $1
  AND id = $2
  AND version = $11
RETURNING id, organization_id, order_id, provider, provider_object_id, amount_minor,
          currency, state, idempotency_key, failure_class, failure_code,
          failure_message, client_action_reference, metadata, version, created_at,
          updated_at;
