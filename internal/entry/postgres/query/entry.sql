-- name: GetDatabaseTime :one
SELECT now()::timestamptz;

-- name: GetEntrySummaryMeta :one
SELECT now()::timestamptz AS generated_at,
       COUNT(scan.id)::bigint AS scan_count
FROM sessions AS session
LEFT JOIN scan_attempts AS scan
  ON scan.organization_id = session.organization_id
 AND scan.session_id = session.id
 AND scan.scanned_at >= sqlc.arg(from_time)
 AND scan.scanned_at < sqlc.arg(until_time)
WHERE session.organization_id = sqlc.arg(organization_id)
  AND session.id = sqlc.arg(session_id);

-- name: GetLatestEntryReceivedAt :one
SELECT scan.received_at
FROM scan_attempts AS scan
WHERE scan.organization_id = sqlc.arg(organization_id)
  AND scan.session_id = sqlc.arg(session_id)
  AND scan.scanned_at >= sqlc.arg(from_time)
  AND scan.scanned_at < sqlc.arg(until_time)
ORDER BY scan.received_at DESC, scan.id DESC
LIMIT 1;

-- name: ListEntrySummaryRows :many
SELECT scan.result,
       scan.gate_id,
       date_trunc('minute', scan.scanned_at)::timestamptz AS minute,
       order_line.price_tier_id,
       COUNT(*)::bigint AS scan_count
FROM scan_attempts AS scan
JOIN tickets AS ticket
  ON ticket.organization_id = scan.organization_id
 AND ticket.id = scan.ticket_id
JOIN entitlements AS entitlement
  ON entitlement.organization_id = ticket.organization_id
 AND entitlement.id = ticket.entitlement_id
JOIN order_lines AS order_line
  ON order_line.organization_id = entitlement.organization_id
 AND order_line.id = entitlement.order_line_id
WHERE scan.organization_id = sqlc.arg(organization_id)
  AND scan.session_id = sqlc.arg(session_id)
  AND scan.scanned_at >= sqlc.arg(from_time)
  AND scan.scanned_at < sqlc.arg(until_time)
GROUP BY scan.result, scan.gate_id, date_trunc('minute', scan.scanned_at), order_line.price_tier_id
ORDER BY minute ASC, scan.result ASC, scan.gate_id ASC NULLS FIRST, order_line.price_tier_id ASC;

-- name: LockDeviceForScan :one
SELECT id, organization_id, state
FROM devices
WHERE organization_id = $1
  AND id = $2
FOR UPDATE;

-- name: IsDeviceAuthorizedForScan :one
SELECT EXISTS (
    SELECT 1
    FROM device_assignments AS assignment
    WHERE assignment.organization_id = sqlc.arg(organization_id)
      AND assignment.device_id = sqlc.arg(device_id)
      AND assignment.capability = 'entry.scan'
      AND assignment.state = 'active'
      AND assignment.valid_from <= sqlc.arg(at_time)
      AND (assignment.valid_until IS NULL OR assignment.valid_until > sqlc.arg(at_time))
      AND (assignment.session_id IS NULL OR assignment.session_id = sqlc.arg(session_id))
      AND (
          assignment.gate_id IS NULL
          OR (sqlc.narg(gate_id)::uuid IS NOT NULL AND assignment.gate_id = sqlc.narg(gate_id)::uuid)
      )
) AS authorized;

-- name: LockTicketForScan :one
SELECT id, organization_id, session_id, state, version, entry_opens_at,
       entry_closes_at, admitted_at
FROM tickets
WHERE organization_id = $1
  AND id = $2
FOR UPDATE;

-- name: InsertScanAttempt :one
INSERT INTO scan_attempts (
    id, organization_id, session_id, ticket_id, device_id, device_scan_id,
    ticket_version, result, mode, credential_sha256, gate_id, scanned_at,
    received_at, metadata
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'online', $9, $10, $11, $11, $12)
RETURNING id, organization_id, session_id, ticket_id, device_id, device_scan_id,
          ticket_version, result, mode, credential_sha256, gate_id, scanned_at,
          received_at, metadata;

-- name: GetScanAttemptByDeviceKey :one
SELECT id, organization_id, session_id, ticket_id, device_id, device_scan_id,
       ticket_version, result, mode, credential_sha256, gate_id, scanned_at,
       received_at, metadata
FROM scan_attempts
WHERE organization_id = $1
  AND device_id = $2
  AND device_scan_id = $3;

-- name: MarkTicketAdmitted :one
UPDATE tickets
SET state = 'admitted', version = version + 1, admitted_at = $3, updated_at = $3
WHERE organization_id = $1
  AND id = $2
  AND state = 'valid'
  AND version = $4
RETURNING id, organization_id, session_id, state, version, entry_opens_at,
          entry_closes_at, admitted_at;

-- name: InsertAdmission :exec
INSERT INTO admissions (
    id, organization_id, ticket_id, scan_attempt_id, session_id, device_id,
    gate_id, admitted_at, override_actor_id, override_reason
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);
