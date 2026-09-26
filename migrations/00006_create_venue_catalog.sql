-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

CREATE TABLE venues (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    slug text NOT NULL,
    display_name text NOT NULL,
    status text NOT NULL DEFAULT 'draft',
    timezone text NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_venues_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT uq_venues_organization_id UNIQUE (organization_id, id),
    CONSTRAINT uq_venues_organization_slug UNIQUE (organization_id, slug),
    CONSTRAINT ck_venues_slug CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$' AND char_length(slug) BETWEEN 2 AND 63),
    CONSTRAINT ck_venues_display_name CHECK (char_length(btrim(display_name)) BETWEEN 1 AND 160),
    CONSTRAINT ck_venues_status CHECK (status IN ('draft', 'active', 'inactive', 'archived')),
    CONSTRAINT ck_venues_timezone CHECK (char_length(btrim(timezone)) BETWEEN 1 AND 64),
    CONSTRAINT ck_venues_version CHECK (version > 0),
    CONSTRAINT ck_venues_timestamps CHECK (updated_at >= created_at)
);

CREATE INDEX ix_venues_organization_created_id
    ON venues (organization_id, created_at DESC, id DESC);
CREATE INDEX ix_venues_organization_status
    ON venues (organization_id, status, created_at DESC, id DESC);

CREATE TABLE spaces (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    venue_id uuid NOT NULL,
    slug text NOT NULL,
    display_name text NOT NULL,
    status text NOT NULL DEFAULT 'draft',
    inventory_mode text NOT NULL,
    physical_capacity bigint NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_spaces_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_spaces_venue_tenant FOREIGN KEY (organization_id, venue_id)
        REFERENCES venues (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_spaces_organization_venue_id UNIQUE (organization_id, venue_id, id),
    CONSTRAINT uq_spaces_venue_slug UNIQUE (organization_id, venue_id, slug),
    CONSTRAINT ck_spaces_slug CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$' AND char_length(slug) BETWEEN 2 AND 63),
    CONSTRAINT ck_spaces_display_name CHECK (char_length(btrim(display_name)) BETWEEN 1 AND 160),
    CONSTRAINT ck_spaces_status CHECK (status IN ('draft', 'active', 'inactive', 'archived')),
    CONSTRAINT ck_spaces_inventory_mode CHECK (inventory_mode IN ('assigned_seating', 'general_admission')),
    CONSTRAINT ck_spaces_capacity CHECK (physical_capacity > 0 AND physical_capacity <= 1000000),
    CONSTRAINT ck_spaces_version CHECK (version > 0),
    CONSTRAINT ck_spaces_timestamps CHECK (updated_at >= created_at)
);

CREATE INDEX ix_spaces_organization_venue_created_id
    ON spaces (organization_id, venue_id, created_at DESC, id DESC);
CREATE INDEX ix_spaces_organization_status
    ON spaces (organization_id, status, created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE spaces;
DROP TABLE venues;
-- +goose StatementEnd
