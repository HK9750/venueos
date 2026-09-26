-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE sessions
    ADD COLUMN inventory_revision bigint NOT NULL DEFAULT 0,
    ADD CONSTRAINT ck_sessions_inventory_revision CHECK (inventory_revision >= 0);
ALTER TABLE ga_pool_templates
    ADD CONSTRAINT uq_ga_pool_templates_organization_id UNIQUE (organization_id, id);

CREATE TABLE session_ga_pools (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    session_id uuid NOT NULL,
    source_pool_id uuid NOT NULL,
    slug text NOT NULL,
    display_name text NOT NULL,
    physical_capacity bigint NOT NULL,
    sellable_capacity bigint NOT NULL,
    held_qty bigint NOT NULL DEFAULT 0,
    sold_qty bigint NOT NULL DEFAULT 0,
    killed_qty bigint NOT NULL DEFAULT 0,
    comped_qty bigint NOT NULL DEFAULT 0,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_session_ga_pools_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_session_ga_pools_session FOREIGN KEY (organization_id, session_id)
        REFERENCES sessions (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_session_ga_pools_source FOREIGN KEY (organization_id, source_pool_id)
        REFERENCES ga_pool_templates (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_session_ga_pools_organization_id UNIQUE (organization_id, id),
    CONSTRAINT uq_session_ga_pools_source UNIQUE (organization_id, session_id, source_pool_id),
    CONSTRAINT ck_session_ga_pools_capacity CHECK (physical_capacity > 0 AND sellable_capacity > 0 AND sellable_capacity <= physical_capacity),
    CONSTRAINT ck_session_ga_pools_counters CHECK (held_qty >= 0 AND sold_qty >= 0 AND killed_qty >= 0 AND comped_qty >= 0 AND held_qty + sold_qty + killed_qty + comped_qty <= sellable_capacity),
    CONSTRAINT ck_session_ga_pools_version CHECK (version > 0),
    CONSTRAINT ck_session_ga_pools_timestamps CHECK (updated_at >= created_at)
);

CREATE INDEX ix_session_ga_pools_session_slug
    ON session_ga_pools (organization_id, session_id, slug, id);

CREATE TABLE holds (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    session_id uuid NOT NULL,
    owner_token_hash bytea NOT NULL,
    owner_user_id uuid,
    channel_id uuid,
    currency text NOT NULL,
    state text NOT NULL DEFAULT 'active',
    expires_at timestamptz NOT NULL,
    renewal_count integer NOT NULL DEFAULT 0,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_holds_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_holds_session FOREIGN KEY (organization_id, session_id)
        REFERENCES sessions (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_holds_channel FOREIGN KEY (organization_id, channel_id)
        REFERENCES sales_channels (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_holds_user FOREIGN KEY (owner_user_id) REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT ck_holds_token_hash CHECK (octet_length(owner_token_hash) = 32),
    CONSTRAINT ck_holds_currency CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT ck_holds_state CHECK (state IN ('active', 'confirmed', 'released', 'expired', 'cancelled')),
    CONSTRAINT ck_holds_renewal_count CHECK (renewal_count >= 0 AND renewal_count <= 1),
    CONSTRAINT ck_holds_version CHECK (version > 0),
    CONSTRAINT ck_holds_timestamps CHECK (updated_at >= created_at)
);

CREATE INDEX ix_holds_active_expiry
    ON holds (organization_id, expires_at, id)
    WHERE state = 'active';
CREATE INDEX ix_holds_owner_token
    ON holds (organization_id, session_id, owner_token_hash, created_at DESC, id DESC);

CREATE TABLE hold_items (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    hold_id uuid NOT NULL,
    session_id uuid NOT NULL,
    pool_id uuid NOT NULL,
    quantity bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_hold_items_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_hold_items_hold FOREIGN KEY (hold_id) REFERENCES holds (id) ON DELETE RESTRICT,
    CONSTRAINT fk_hold_items_pool FOREIGN KEY (organization_id, pool_id)
        REFERENCES session_ga_pools (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_hold_items_hold_pool UNIQUE (organization_id, hold_id, pool_id),
    CONSTRAINT ck_hold_items_quantity CHECK (quantity > 0)
);

CREATE INDEX ix_hold_items_hold ON hold_items (organization_id, hold_id, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE hold_items;
DROP TABLE holds;
DROP TABLE session_ga_pools;
ALTER TABLE sessions DROP CONSTRAINT ck_sessions_inventory_revision;
ALTER TABLE sessions DROP COLUMN inventory_revision;
ALTER TABLE ga_pool_templates DROP CONSTRAINT uq_ga_pool_templates_organization_id;
-- +goose StatementEnd
