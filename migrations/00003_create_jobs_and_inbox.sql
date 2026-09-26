-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

CREATE TABLE inbox_messages (
    id uuid PRIMARY KEY,
    organization_id uuid,
    source text NOT NULL,
    message_id text NOT NULL,
    payload_reference text,
    payload_sha256 bytea NOT NULL,
    state text NOT NULL DEFAULT 'received',
    attempt_count integer NOT NULL DEFAULT 0,
    last_error_code text,
    last_error_message text,
    received_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_inbox_messages_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT uq_inbox_messages_source_message UNIQUE (source, message_id),
    CONSTRAINT ck_inbox_messages_source CHECK (source ~ '^[a-z][a-z0-9_.]{0,127}$'),
    CONSTRAINT ck_inbox_messages_message_id CHECK (char_length(message_id) BETWEEN 1 AND 255),
    CONSTRAINT ck_inbox_messages_payload_reference CHECK (
        payload_reference IS NULL OR char_length(payload_reference) BETWEEN 1 AND 1000
    ),
    CONSTRAINT ck_inbox_messages_payload_sha256 CHECK (octet_length(payload_sha256) = 32),
    CONSTRAINT ck_inbox_messages_state CHECK (
        state IN ('received', 'processing', 'processed', 'failed', 'dead_letter')
    ),
    CONSTRAINT ck_inbox_messages_attempt_count CHECK (attempt_count >= 0),
    CONSTRAINT ck_inbox_messages_processed_at CHECK (
        (state = 'processed' AND processed_at IS NOT NULL)
        OR (state <> 'processed' AND processed_at IS NULL)
    ),
    CONSTRAINT ck_inbox_messages_last_error CHECK (
        (last_error_code IS NULL AND last_error_message IS NULL)
        OR (
            last_error_code IS NOT NULL
            AND last_error_message IS NOT NULL
            AND char_length(last_error_code) BETWEEN 1 AND 64
            AND char_length(last_error_message) BETWEEN 1 AND 1000
        )
    ),
    CONSTRAINT ck_inbox_messages_timestamps CHECK (updated_at >= received_at)
);

CREATE INDEX ix_inbox_messages_unprocessed
    ON inbox_messages (received_at, id)
    WHERE state IN ('received', 'failed');
CREATE INDEX ix_inbox_messages_organization_received
    ON inbox_messages (organization_id, received_at DESC, id DESC);

CREATE TABLE jobs (
    id uuid PRIMARY KEY,
    organization_id uuid,
    job_type text NOT NULL,
    schema_version integer NOT NULL,
    priority smallint NOT NULL DEFAULT 0,
    dedupe_key text NOT NULL,
    payload jsonb NOT NULL,
    state text NOT NULL DEFAULT 'queued',
    run_at timestamptz NOT NULL DEFAULT now(),
    attempt_count integer NOT NULL DEFAULT 0,
    max_attempts integer NOT NULL,
    lease_owner text,
    lease_expires_at timestamptz,
    last_error_class text,
    last_error_message text,
    correlation_id text,
    request_id text,
    trace_id text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    finished_at timestamptz,
    CONSTRAINT fk_jobs_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT uq_jobs_dedupe UNIQUE NULLS NOT DISTINCT (
        organization_id, job_type, dedupe_key
    ),
    CONSTRAINT ck_jobs_type CHECK (
        job_type ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$'
    ),
    CONSTRAINT ck_jobs_schema_version CHECK (schema_version > 0),
    CONSTRAINT ck_jobs_priority CHECK (priority BETWEEN -1000 AND 1000),
    CONSTRAINT ck_jobs_dedupe_key CHECK (
        char_length(dedupe_key) BETWEEN 1 AND 200
        AND dedupe_key ~ '^[A-Za-z0-9._:-]+$'
    ),
    CONSTRAINT ck_jobs_payload CHECK (
        jsonb_typeof(payload) = 'object'
        AND octet_length(payload::text) <= 262144
    ),
    CONSTRAINT ck_jobs_state CHECK (
        state IN ('queued', 'leased', 'retry_wait', 'succeeded', 'dead_letter', 'cancelled')
    ),
    CONSTRAINT ck_jobs_attempts CHECK (
        max_attempts BETWEEN 1 AND 100
        AND attempt_count BETWEEN 0 AND max_attempts
    ),
    CONSTRAINT ck_jobs_lease CHECK (
        (
            state = 'leased'
            AND attempt_count > 0
            AND lease_owner IS NOT NULL
            AND char_length(lease_owner) BETWEEN 1 AND 128
            AND lease_expires_at IS NOT NULL
            AND finished_at IS NULL
        )
        OR (
            state <> 'leased'
            AND lease_owner IS NULL
            AND lease_expires_at IS NULL
        )
    ),
    CONSTRAINT ck_jobs_terminal CHECK (
        (state IN ('succeeded', 'dead_letter', 'cancelled') AND finished_at IS NOT NULL)
        OR (state NOT IN ('succeeded', 'dead_letter', 'cancelled') AND finished_at IS NULL)
    ),
    CONSTRAINT ck_jobs_error CHECK (
        (last_error_class IS NULL AND last_error_message IS NULL)
        OR (
            last_error_class IS NOT NULL
            AND last_error_class IN ('permanent', 'transient', 'conflict', 'unknown')
            AND last_error_message IS NOT NULL
            AND char_length(last_error_message) BETWEEN 1 AND 1000
        )
    ),
    CONSTRAINT ck_jobs_correlation_id CHECK (
        correlation_id IS NULL OR char_length(correlation_id) BETWEEN 1 AND 128
    ),
    CONSTRAINT ck_jobs_request_id CHECK (
        request_id IS NULL OR char_length(request_id) BETWEEN 1 AND 128
    ),
    CONSTRAINT ck_jobs_trace_id CHECK (
        trace_id IS NULL OR char_length(trace_id) BETWEEN 1 AND 64
    ),
    CONSTRAINT ck_jobs_timestamps CHECK (
        updated_at >= created_at
        AND (started_at IS NULL OR started_at >= created_at)
        AND (finished_at IS NULL OR finished_at >= created_at)
    )
);

CREATE INDEX ix_jobs_claim
    ON jobs (priority DESC, run_at, created_at, id)
    WHERE state IN ('queued', 'retry_wait');
CREATE INDEX ix_jobs_expired_lease
    ON jobs (lease_expires_at, id)
    WHERE state = 'leased';
CREATE INDEX ix_jobs_organization_state_created
    ON jobs (organization_id, state, created_at DESC, id DESC);
CREATE INDEX ix_jobs_type_state_run
    ON jobs (job_type, state, run_at, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE jobs;
DROP TABLE inbox_messages;
-- +goose StatementEnd
