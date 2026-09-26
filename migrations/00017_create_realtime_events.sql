-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE sessions
    ADD CONSTRAINT uq_sessions_organization_id UNIQUE (organization_id, id);

CREATE TABLE realtime_events (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    session_id uuid NOT NULL,
    topic text NOT NULL,
    sequence bigint NOT NULL,
    event_type text NOT NULL,
    schema_version integer NOT NULL DEFAULT 1,
    payload jsonb NOT NULL,
    occurred_at timestamptz NOT NULL,
    retention_expires_at timestamptz NOT NULL,
    CONSTRAINT fk_realtime_events_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_realtime_events_session FOREIGN KEY (organization_id, session_id)
        REFERENCES sessions (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_realtime_events_sequence UNIQUE (organization_id, session_id, topic, sequence),
    CONSTRAINT ck_realtime_events_topic CHECK (topic ~ '^[a-z0-9._:-]{1,120}$'),
    CONSTRAINT ck_realtime_events_type CHECK (event_type ~ '^[a-z0-9._:-]{1,120}$'),
    CONSTRAINT ck_realtime_events_schema CHECK (schema_version > 0),
    CONSTRAINT ck_realtime_events_sequence CHECK (sequence > 0),
    CONSTRAINT ck_realtime_events_payload CHECK (jsonb_typeof(payload) = 'object'),
    CONSTRAINT ck_realtime_events_retention CHECK (retention_expires_at >= occurred_at)
);

CREATE INDEX ix_realtime_events_replay
    ON realtime_events (organization_id, session_id, topic, sequence);
CREATE INDEX ix_realtime_events_retention
    ON realtime_events (retention_expires_at, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE realtime_events;
ALTER TABLE sessions DROP CONSTRAINT uq_sessions_organization_id;
-- +goose StatementEnd
