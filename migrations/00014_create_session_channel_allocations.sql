-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

CREATE TABLE session_channel_allocations (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    session_id uuid NOT NULL,
    channel_id uuid NOT NULL,
    scope_key text NOT NULL DEFAULT 'all',
    allocation_mode text NOT NULL,
    quantity bigint NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_session_channel_allocations_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_session_channel_allocations_session FOREIGN KEY (organization_id, session_id)
        REFERENCES sessions (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_session_channel_allocations_channel FOREIGN KEY (organization_id, channel_id)
        REFERENCES sales_channels (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_session_channel_allocations_organization_id UNIQUE (organization_id, id),
    CONSTRAINT uq_session_channel_allocations_scope UNIQUE (organization_id, session_id, channel_id, scope_key),
    CONSTRAINT ck_session_channel_allocations_scope CHECK (scope_key ~ '^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$'),
    CONSTRAINT ck_session_channel_allocations_mode CHECK (allocation_mode IN ('hard_reserved', 'soft_policy')),
    CONSTRAINT ck_session_channel_allocations_quantity CHECK (quantity > 0),
    CONSTRAINT ck_session_channel_allocations_version CHECK (version > 0),
    CONSTRAINT ck_session_channel_allocations_timestamps CHECK (updated_at >= created_at)
);

CREATE INDEX ix_session_channel_allocations_session_mode
    ON session_channel_allocations (organization_id, session_id, allocation_mode, created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE session_channel_allocations;
-- +goose StatementEnd
