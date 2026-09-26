-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

CREATE TABLE seat_map_versions (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    space_id uuid NOT NULL,
    name text NOT NULL,
    status text NOT NULL DEFAULT 'draft',
    revision bigint NOT NULL DEFAULT 0,
    checksum bytea NOT NULL,
    content jsonb NOT NULL,
    seat_count integer NOT NULL,
    sellable_count integer NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    published_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_seat_map_versions_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_seat_map_versions_space_tenant FOREIGN KEY (organization_id, space_id)
        REFERENCES spaces (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_seat_map_versions_organization_id UNIQUE (organization_id, id),
    CONSTRAINT ck_seat_map_versions_name CHECK (char_length(btrim(name)) BETWEEN 1 AND 160),
    CONSTRAINT ck_seat_map_versions_status CHECK (status IN ('draft', 'validating', 'published', 'retired')),
    CONSTRAINT ck_seat_map_versions_revision CHECK (
        (status IN ('draft', 'validating') AND revision = 0 AND published_at IS NULL)
        OR (status IN ('published', 'retired') AND revision > 0 AND published_at IS NOT NULL)
    ),
    CONSTRAINT ck_seat_map_versions_checksum CHECK (octet_length(checksum) = 32),
    CONSTRAINT ck_seat_map_versions_content CHECK (jsonb_typeof(content) = 'object'),
    CONSTRAINT ck_seat_map_versions_counts CHECK (seat_count > 0 AND sellable_count >= 0 AND sellable_count <= seat_count),
    CONSTRAINT ck_seat_map_versions_version CHECK (version > 0),
    CONSTRAINT ck_seat_map_versions_timestamps CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX uq_seat_map_versions_published_revision
    ON seat_map_versions (organization_id, space_id, revision)
    WHERE revision > 0;
CREATE INDEX ix_seat_map_versions_space_created_id
    ON seat_map_versions (organization_id, space_id, created_at DESC, id DESC);
CREATE INDEX ix_seat_map_versions_space_status_revision
    ON seat_map_versions (organization_id, space_id, status, revision DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE seat_map_versions;
-- +goose StatementEnd
