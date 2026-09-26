-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE spaces
    ADD CONSTRAINT uq_spaces_organization_id UNIQUE (organization_id, id);

CREATE TABLE gates (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    venue_id uuid NOT NULL,
    space_id uuid,
    code text NOT NULL,
    display_name text NOT NULL,
    status text NOT NULL DEFAULT 'draft',
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_gates_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_gates_venue_tenant FOREIGN KEY (organization_id, venue_id)
        REFERENCES venues (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_gates_space_tenant FOREIGN KEY (organization_id, space_id)
        REFERENCES spaces (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_gates_organization_venue_code UNIQUE (organization_id, venue_id, code),
    CONSTRAINT ck_gates_code CHECK (code ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$' AND char_length(code) BETWEEN 1 AND 32),
    CONSTRAINT ck_gates_display_name CHECK (char_length(btrim(display_name)) BETWEEN 1 AND 120),
    CONSTRAINT ck_gates_status CHECK (status IN ('draft', 'active', 'inactive', 'archived')),
    CONSTRAINT ck_gates_version CHECK (version > 0),
    CONSTRAINT ck_gates_timestamps CHECK (updated_at >= created_at)
);

CREATE INDEX ix_gates_organization_venue_created_id
    ON gates (organization_id, venue_id, created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE gates;
ALTER TABLE spaces DROP CONSTRAINT uq_spaces_organization_id;
-- +goose StatementEnd
