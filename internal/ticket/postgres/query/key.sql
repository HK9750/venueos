-- name: ResolveTicketKey :one
SELECT public_key, state
FROM ticket_signing_keys
WHERE key_id = sqlc.arg(key_id);

-- name: LockEntitlementForIssue :one
SELECT id, organization_id, order_line_id, session_id, unit_number, state
FROM entitlements
WHERE organization_id = sqlc.arg(organization_id)
  AND id = sqlc.arg(entitlement_id)
FOR UPDATE;

-- name: GetTicketByEntitlement :one
SELECT id, organization_id, entitlement_id, order_line_id, session_id,
       public_reference, state, version, signing_key_id, not_before, expires_at,
       entry_opens_at, entry_closes_at, issued_at, admitted_at, void_at,
       refunded_at, expired_at, created_at, updated_at
FROM tickets
WHERE organization_id = sqlc.arg(organization_id)
  AND entitlement_id = sqlc.arg(entitlement_id);

-- name: InsertTicket :one
INSERT INTO tickets (
    id, organization_id, entitlement_id, order_line_id, session_id,
    public_reference, state, version, signing_key_id, not_before, expires_at,
    entry_opens_at, entry_closes_at, issued_at, created_at, updated_at
) VALUES (
    sqlc.arg(id), sqlc.arg(organization_id), sqlc.arg(entitlement_id),
    sqlc.arg(order_line_id), sqlc.arg(session_id), sqlc.arg(public_reference),
    sqlc.arg(state), sqlc.arg(version), sqlc.arg(signing_key_id),
    sqlc.arg(not_before), sqlc.arg(expires_at), sqlc.arg(entry_opens_at),
    sqlc.arg(entry_closes_at), sqlc.arg(issued_at), sqlc.arg(created_at),
    sqlc.arg(updated_at)
)
RETURNING id, organization_id, entitlement_id, order_line_id, session_id,
          public_reference, state, version, signing_key_id, not_before,
          expires_at, entry_opens_at, entry_closes_at, issued_at, admitted_at,
          void_at, refunded_at, expired_at, created_at, updated_at;

-- name: MarkEntitlementIssued :one
UPDATE entitlements
SET state = 'issued', updated_at = sqlc.arg(updated_at)
WHERE organization_id = sqlc.arg(organization_id)
  AND id = sqlc.arg(entitlement_id)
  AND state = 'pending'
RETURNING id, organization_id, order_line_id, session_id, unit_number, state;
