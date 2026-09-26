-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

CREATE TABLE job_schedules (
    schedule_key text PRIMARY KEY,
    organization_id uuid,
    job_type text NOT NULL,
    schema_version integer NOT NULL DEFAULT 1,
    priority smallint NOT NULL DEFAULT 0,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    interval_seconds integer NOT NULL,
    next_run_at timestamptz NOT NULL,
    max_attempts integer NOT NULL DEFAULT 3,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_job_schedules_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT ck_job_schedules_key CHECK (schedule_key ~ '^[A-Za-z0-9._:-]{1,160}$'),
    CONSTRAINT ck_job_schedules_type CHECK (job_type ~ '^[a-z][a-z0-9_.-]{0,119}$'),
    CONSTRAINT ck_job_schedules_schema CHECK (schema_version > 0),
    CONSTRAINT ck_job_schedules_priority CHECK (priority BETWEEN -1000 AND 1000),
    CONSTRAINT ck_job_schedules_payload CHECK (jsonb_typeof(payload) = 'object' AND octet_length(payload::text) <= 65536),
    CONSTRAINT ck_job_schedules_interval CHECK (interval_seconds BETWEEN 1 AND 86400),
    CONSTRAINT ck_job_schedules_attempts CHECK (max_attempts BETWEEN 1 AND 20),
    CONSTRAINT ck_job_schedules_timestamps CHECK (updated_at >= created_at)
);

CREATE INDEX ix_job_schedules_due
    ON job_schedules (enabled, next_run_at, schedule_key);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE job_schedules;
-- +goose StatementEnd
