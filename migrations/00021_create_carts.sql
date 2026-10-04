-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE holds
    ADD CONSTRAINT uq_holds_organization_id UNIQUE (organization_id, id);

CREATE TABLE carts (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    session_id uuid NOT NULL,
    hold_id uuid NOT NULL,
    owner_token_hash bytea NOT NULL,
    owner_user_id uuid,
    currency text NOT NULL,
    state text NOT NULL DEFAULT 'active',
    quote_snapshot jsonb NOT NULL,
    quote_sha256 bytea NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_carts_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_carts_session FOREIGN KEY (organization_id, session_id)
        REFERENCES sessions (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_carts_hold FOREIGN KEY (organization_id, hold_id)
        REFERENCES holds (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_carts_owner_user FOREIGN KEY (owner_user_id)
        REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT ck_carts_owner_token_hash CHECK (octet_length(owner_token_hash) = 32),
    CONSTRAINT ck_carts_currency CHECK (currency ~ '^[A-Z]{3}$'),
    CONSTRAINT ck_carts_state CHECK (state IN ('active', 'checked_out', 'expired', 'cancelled')),
    CONSTRAINT ck_carts_quote_snapshot CHECK (
        jsonb_typeof(quote_snapshot) = 'object'
        AND octet_length(quote_snapshot::text) BETWEEN 2 AND 262144
    ),
    CONSTRAINT ck_carts_quote_sha256 CHECK (octet_length(quote_sha256) = 32),
    CONSTRAINT ck_carts_version CHECK (version > 0),
    CONSTRAINT ck_carts_timestamps CHECK (updated_at >= created_at),
    CONSTRAINT uq_carts_organization_id UNIQUE (organization_id, id)
);

CREATE UNIQUE INDEX uq_carts_active_hold
    ON carts (organization_id, hold_id)
    WHERE state = 'active';

CREATE INDEX ix_carts_owner
    ON carts (organization_id, id, owner_token_hash);

CREATE INDEX ix_carts_session_state_updated
    ON carts (organization_id, session_id, state, updated_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE carts;
ALTER TABLE holds DROP CONSTRAINT uq_holds_organization_id;
-- +goose StatementEnd
