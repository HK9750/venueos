-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

CREATE TABLE ga_pool_templates (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    space_id uuid NOT NULL,
    slug text NOT NULL,
    display_name text NOT NULL,
    status text NOT NULL DEFAULT 'draft',
    physical_capacity bigint NOT NULL,
    sellable_capacity bigint NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_ga_pool_templates_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_ga_pool_templates_space_tenant FOREIGN KEY (organization_id, space_id)
        REFERENCES spaces (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_ga_pool_templates_space_slug UNIQUE (organization_id, space_id, slug),
    CONSTRAINT ck_ga_pool_templates_slug CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$' AND char_length(slug) BETWEEN 2 AND 63),
    CONSTRAINT ck_ga_pool_templates_display_name CHECK (char_length(btrim(display_name)) BETWEEN 1 AND 160),
    CONSTRAINT ck_ga_pool_templates_status CHECK (status IN ('draft', 'active', 'inactive', 'archived')),
    CONSTRAINT ck_ga_pool_templates_capacity CHECK (physical_capacity > 0 AND sellable_capacity > 0 AND sellable_capacity <= physical_capacity AND physical_capacity <= 1000000),
    CONSTRAINT ck_ga_pool_templates_version CHECK (version > 0),
    CONSTRAINT ck_ga_pool_templates_timestamps CHECK (updated_at >= created_at)
);

CREATE INDEX ix_ga_pool_templates_organization_space_created_id
    ON ga_pool_templates (organization_id, space_id, created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE ga_pool_templates;
-- +goose StatementEnd
