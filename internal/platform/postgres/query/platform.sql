-- name: InsertAuditEntry :exec
INSERT INTO audit_entries (
    id, organization_id, actor_type, actor_id, effective_actor_type,
    effective_actor_id, action, subject_type, subject_id, result, reason,
    request_id, trace_id, source_ip, device_id, before_data, after_data,
    occurred_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10, $11,
    $12, $13, $14, $15, $16, $17,
    $18
);

-- name: InsertOutboxEvent :exec
INSERT INTO outbox_events (
    id, organization_id, aggregate_type, aggregate_id, aggregate_version,
    event_type, schema_version, payload, correlation_id, causation_id,
    state, available_at, occurred_at, created_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8, $9, $10,
    'pending', $11, $12, $12
);

-- name: ClaimOutboxEvents :many
WITH candidates AS (
    SELECT source.id
    FROM outbox_events AS source
    WHERE (
        (source.state = 'pending' AND source.available_at <= sqlc.arg(now))
        OR (source.state = 'leased' AND source.lease_expires_at <= sqlc.arg(now))
    )
      AND source.attempt_count < sqlc.arg(max_attempts)
    ORDER BY source.available_at ASC, source.occurred_at ASC, source.id ASC
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE SKIP LOCKED
)
UPDATE outbox_events AS event
SET state = 'leased',
    attempt_count = event.attempt_count + 1,
    lease_owner = sqlc.arg(lease_owner),
    lease_expires_at = sqlc.arg(now) + make_interval(secs => sqlc.arg(lease_seconds)),
    published_at = NULL,
    dead_lettered_at = NULL,
    last_error_code = CASE WHEN event.state = 'leased' THEN 'lease_expired' ELSE event.last_error_code END,
    last_error_message = CASE WHEN event.state = 'leased' THEN 'Previous relay lease expired.' ELSE event.last_error_message END
FROM candidates
WHERE event.id = candidates.id
RETURNING event.*;

-- name: MarkOutboxPublished :execrows
UPDATE outbox_events
SET state = 'published',
    lease_owner = NULL,
    lease_expires_at = NULL,
    published_at = sqlc.arg(published_at),
    last_error_code = NULL,
    last_error_message = NULL
WHERE id = sqlc.arg(id)
  AND state = 'leased'
  AND lease_owner = sqlc.arg(lease_owner);

-- name: RetryOutboxEvent :execrows
UPDATE outbox_events
SET state = 'pending',
    available_at = sqlc.arg(available_at),
    lease_owner = NULL,
    lease_expires_at = NULL,
    last_error_code = sqlc.arg(error_code),
    last_error_message = sqlc.arg(error_message)
WHERE id = sqlc.arg(id)
  AND state = 'leased'
  AND lease_owner = sqlc.arg(lease_owner);

-- name: DeadLetterOutboxEvent :execrows
UPDATE outbox_events
SET state = 'dead_letter',
    lease_owner = NULL,
    lease_expires_at = NULL,
    dead_lettered_at = sqlc.arg(dead_lettered_at),
    last_error_code = sqlc.arg(error_code),
    last_error_message = sqlc.arg(error_message)
WHERE id = sqlc.arg(id)
  AND state = 'leased'
  AND lease_owner = sqlc.arg(lease_owner);

-- name: DeadLetterExhaustedOutboxLeases :execrows
UPDATE outbox_events
SET state = 'dead_letter',
    lease_owner = NULL,
    lease_expires_at = NULL,
    dead_lettered_at = sqlc.arg(now),
    last_error_code = 'lease_expired',
    last_error_message = 'Outbox relay lease expired after the final attempt.'
WHERE state = 'leased'
  AND lease_expires_at <= sqlc.arg(now)
  AND attempt_count >= sqlc.arg(max_attempts);

-- name: InsertRealtimeEvent :exec
INSERT INTO realtime_events (
    id, organization_id, session_id, topic, sequence, event_type,
    schema_version, payload, occurred_at, retention_expires_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: DeleteExpiredRealtimeEvents :execrows
WITH expired AS (
    SELECT organization_id, id
    FROM realtime_events
    WHERE retention_expires_at <= now()
    ORDER BY retention_expires_at ASC, id ASC
    LIMIT sqlc.arg(batch_limit)
    FOR UPDATE SKIP LOCKED
)
DELETE FROM realtime_events AS events
USING expired
WHERE events.organization_id = expired.organization_id
  AND events.id = expired.id;

-- name: DeleteExpiredIdempotencyRecords :execrows
WITH expired AS (
    SELECT source.id
    FROM idempotency_records AS source
    WHERE source.expires_at <= sqlc.arg(now)
      AND (
          source.state <> 'processing'
          OR source.locked_until IS NULL
          OR source.locked_until <= sqlc.arg(now)
      )
    ORDER BY source.expires_at ASC, source.id ASC
    LIMIT sqlc.arg(batch_limit)
    FOR UPDATE SKIP LOCKED
)
DELETE FROM idempotency_records AS records
USING expired
WHERE records.id = expired.id;

-- name: InsertIdempotencyRecord :execrows
INSERT INTO idempotency_records (
    id, organization_id, principal_type, principal_id, operation,
    idempotency_key, request_fingerprint, state, locked_until, expires_at,
    created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, 'processing', $8, $9,
    $10, $10
)
ON CONFLICT (organization_id, principal_type, principal_id, operation, idempotency_key)
DO NOTHING;

-- name: GetIdempotencyRecordForUpdate :one
SELECT id, organization_id, principal_type, principal_id, operation,
       idempotency_key, request_fingerprint, state, locked_until,
       response_status, response_body, resource_type, resource_id,
       expires_at, created_at, updated_at
FROM idempotency_records
WHERE organization_id = $1
  AND principal_type = $2
  AND principal_id = $3
  AND operation = $4
  AND idempotency_key = $5
FOR UPDATE;

-- name: CompleteIdempotencyRecord :execrows
UPDATE idempotency_records
SET state = $3,
    locked_until = NULL,
    response_status = $4,
    response_body = $5,
    resource_type = $6,
    resource_id = $7,
    updated_at = $8
WHERE id = $1
  AND organization_id = $2
  AND state = 'processing';

-- name: EnqueueJob :execrows
INSERT INTO jobs (
    id, organization_id, job_type, schema_version, priority, dedupe_key,
    payload, state, run_at, max_attempts, correlation_id, request_id,
    trace_id, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, 'queued', $8, $9, $10, $11,
    $12, $13, $13
)
ON CONFLICT (organization_id, job_type, dedupe_key)
DO NOTHING;

-- name: EnsureJobSchedule :exec
INSERT INTO job_schedules (
    schedule_key, organization_id, job_type, schema_version, priority, payload,
    interval_seconds, next_run_at, max_attempts, enabled, created_at, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, true, $10, $10)
ON CONFLICT (schedule_key)
DO UPDATE SET
    organization_id = EXCLUDED.organization_id,
    job_type = EXCLUDED.job_type,
    schema_version = EXCLUDED.schema_version,
    priority = EXCLUDED.priority,
    payload = EXCLUDED.payload,
    interval_seconds = EXCLUDED.interval_seconds,
    max_attempts = EXCLUDED.max_attempts,
    enabled = true,
    updated_at = EXCLUDED.updated_at;

-- name: ClaimDueJobSchedule :one
WITH due AS (
    SELECT schedule_key, next_run_at AS due_at
    FROM job_schedules
    WHERE enabled = true AND next_run_at <= sqlc.arg(now)
    ORDER BY next_run_at ASC, schedule_key ASC
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
UPDATE job_schedules AS schedule
SET next_run_at = GREATEST(
        schedule.next_run_at + make_interval(secs => schedule.interval_seconds),
        sqlc.arg(now) + make_interval(secs => schedule.interval_seconds)
    ),
    updated_at = sqlc.arg(now)
FROM due
WHERE schedule.schedule_key = due.schedule_key
RETURNING schedule.*, due.due_at;

-- name: ClaimJobs :many
WITH candidates AS (
    SELECT id
    FROM jobs
    WHERE (
        (state IN ('queued', 'retry_wait') AND run_at <= now())
        OR (state = 'leased' AND lease_expires_at <= now())
    )
      AND attempt_count < max_attempts
    ORDER BY priority DESC, run_at, created_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT sqlc.arg(batch_size)
)
UPDATE jobs AS job
SET state = 'leased',
    attempt_count = job.attempt_count + 1,
    lease_owner = sqlc.arg(lease_owner),
    lease_expires_at = now() + make_interval(secs => sqlc.arg(lease_seconds)),
    last_error_class = CASE WHEN job.state = 'leased' THEN 'unknown' ELSE job.last_error_class END,
    last_error_message = CASE WHEN job.state = 'leased' THEN 'Previous worker lease expired.' ELSE job.last_error_message END,
    started_at = COALESCE(job.started_at, now()),
    updated_at = now()
FROM candidates
WHERE job.id = candidates.id
RETURNING job.*;

-- name: SucceedJob :execrows
UPDATE jobs
SET state = 'succeeded',
    lease_owner = NULL,
    lease_expires_at = NULL,
    last_error_class = NULL,
    last_error_message = NULL,
    finished_at = now(),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND state = 'leased'
  AND lease_owner = sqlc.arg(lease_owner);

-- name: RetryJob :execrows
UPDATE jobs
SET state = 'retry_wait',
    run_at = now() + make_interval(secs => sqlc.arg(delay_milliseconds)::double precision / 1000.0),
    lease_owner = NULL,
    lease_expires_at = NULL,
    last_error_class = sqlc.arg(error_class),
    last_error_message = sqlc.arg(error_message),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND state = 'leased'
  AND lease_owner = sqlc.arg(lease_owner)
  AND attempt_count < max_attempts;

-- name: DeadLetterJob :execrows
UPDATE jobs
SET state = 'dead_letter',
    lease_owner = NULL,
    lease_expires_at = NULL,
    last_error_class = sqlc.arg(error_class),
    last_error_message = sqlc.arg(error_message),
    finished_at = now(),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND state = 'leased'
  AND lease_owner = sqlc.arg(lease_owner);

-- name: DeadLetterExhaustedLeases :execrows
UPDATE jobs
SET state = 'dead_letter',
    lease_owner = NULL,
    lease_expires_at = NULL,
    last_error_class = 'unknown',
    last_error_message = 'Worker lease expired after the final attempt.',
    finished_at = now(),
    updated_at = now()
WHERE state = 'leased'
  AND lease_expires_at <= now()
  AND attempt_count >= max_attempts;

-- name: ExtendJobLease :execrows
UPDATE jobs
SET lease_expires_at = now() + make_interval(secs => sqlc.arg(lease_seconds)),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND state = 'leased'
  AND lease_owner = sqlc.arg(lease_owner)
  AND lease_expires_at > now();

-- name: GetJobQueueStats :many
SELECT job_type,
       count(*)::bigint AS depth,
       COALESCE(EXTRACT(EPOCH FROM now() - min(run_at)), 0)::double precision AS oldest_age_seconds
FROM jobs
WHERE state IN ('queued', 'retry_wait')
  AND run_at <= now()
  AND job_type = ANY(sqlc.arg(job_types)::text[])
GROUP BY job_type
ORDER BY job_type;
