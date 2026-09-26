-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE sessions
    ADD CONSTRAINT uq_sessions_organization_id UNIQUE (organization_id, id);

CREATE TABLE price_tiers (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    session_id uuid NOT NULL,
    slug text NOT NULL,
    display_name text NOT NULL,
    currency text NOT NULL,
    amount_minor bigint NOT NULL,
    minimum_quantity integer NOT NULL DEFAULT 1,
    maximum_quantity integer NOT NULL DEFAULT 10,
    sales_start_at timestamptz,
    sales_end_at timestamptz,
    status text NOT NULL DEFAULT 'draft',
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_price_tiers_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_price_tiers_session_tenant FOREIGN KEY (organization_id, session_id)
        REFERENCES sessions (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_price_tiers_organization_id UNIQUE (organization_id, id),
    CONSTRAINT uq_price_tiers_session_slug UNIQUE (organization_id, session_id, slug),
    CONSTRAINT ck_price_tiers_slug CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$' AND char_length(slug) BETWEEN 2 AND 63),
    CONSTRAINT ck_price_tiers_display_name CHECK (char_length(btrim(display_name)) BETWEEN 1 AND 160),
    CONSTRAINT ck_price_tiers_currency CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT ck_price_tiers_amount CHECK (amount_minor >= 0),
    CONSTRAINT ck_price_tiers_quantity CHECK (minimum_quantity > 0 AND maximum_quantity >= minimum_quantity AND maximum_quantity <= 1000),
    CONSTRAINT ck_price_tiers_sales_window CHECK (
        (sales_start_at IS NULL AND sales_end_at IS NULL)
        OR (sales_start_at IS NOT NULL AND sales_end_at IS NOT NULL AND sales_start_at < sales_end_at)
    ),
    CONSTRAINT ck_price_tiers_status CHECK (status IN ('draft', 'active', 'inactive', 'archived')),
    CONSTRAINT ck_price_tiers_version CHECK (version > 0),
    CONSTRAINT ck_price_tiers_timestamps CHECK (updated_at >= created_at)
);

CREATE INDEX ix_price_tiers_organization_session_created_id
    ON price_tiers (organization_id, session_id, created_at DESC, id DESC);
CREATE INDEX ix_price_tiers_organization_session_status
    ON price_tiers (organization_id, session_id, status, created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE price_tiers;
ALTER TABLE sessions DROP CONSTRAINT uq_sessions_organization_id;
-- +goose StatementEnd
