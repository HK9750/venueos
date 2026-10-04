-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

CREATE TABLE device_assignments (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    device_id uuid NOT NULL,
    session_id uuid,
    gate_id uuid,
    capability text NOT NULL,
    state text NOT NULL DEFAULT 'active',
    valid_from timestamptz NOT NULL DEFAULT now(),
    valid_until timestamptz,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_device_assignments_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_device_assignments_device FOREIGN KEY (organization_id, device_id)
        REFERENCES devices (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_device_assignments_session FOREIGN KEY (organization_id, session_id)
        REFERENCES sessions (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_device_assignments_gate FOREIGN KEY (organization_id, gate_id)
        REFERENCES gates (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_device_assignments_organization_id UNIQUE (organization_id, id),
    CONSTRAINT ck_device_assignments_capability CHECK (capability IN ('entry.scan', 'entry.override')),
    CONSTRAINT ck_device_assignments_state CHECK (state IN ('active', 'revoked')),
    CONSTRAINT ck_device_assignments_window CHECK (valid_until IS NULL OR valid_until > valid_from),
    CONSTRAINT ck_device_assignments_version CHECK (version > 0),
    CONSTRAINT ck_device_assignments_timestamps CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX uq_device_assignments_scope
    ON device_assignments (
        organization_id,
        device_id,
        COALESCE(session_id, '00000000-0000-0000-0000-000000000000'::uuid),
        COALESCE(gate_id, '00000000-0000-0000-0000-000000000000'::uuid),
        capability
    )
    WHERE state = 'active';

CREATE INDEX ix_device_assignments_active_scope
    ON device_assignments (organization_id, device_id, state, session_id, gate_id, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE device_assignments;
-- +goose StatementEnd
