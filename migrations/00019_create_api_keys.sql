-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

CREATE TABLE api_keys (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    prefix text NOT NULL,
    name text NOT NULL,
    secret_hash bytea NOT NULL,
    scopes text[] NOT NULL,
    created_by_type text NOT NULL,
    created_by_id text NOT NULL,
    expires_at timestamptz,
    revoked_at timestamptz,
    last_used_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_api_keys_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT uq_api_keys_prefix UNIQUE (prefix),
    CONSTRAINT ck_api_keys_prefix CHECK (
        prefix ~ '^vos_[A-Za-z0-9_-]{12}$'
    ),
    CONSTRAINT ck_api_keys_name CHECK (
        char_length(btrim(name)) BETWEEN 1 AND 120
    ),
    CONSTRAINT ck_api_keys_secret_hash CHECK (octet_length(secret_hash) = 32),
    CONSTRAINT ck_api_keys_scopes CHECK (
        cardinality(scopes) BETWEEN 1 AND 32
        AND array_position(scopes, NULL) IS NULL
    ),
    CONSTRAINT ck_api_keys_creator_type CHECK (
        created_by_type ~ '^[a-z][a-z0-9_]{0,63}$'
    ),
    CONSTRAINT ck_api_keys_creator_id CHECK (
        char_length(btrim(created_by_id)) BETWEEN 1 AND 200
    ),
    CONSTRAINT ck_api_keys_version CHECK (version > 0),
    CONSTRAINT ck_api_keys_expiry CHECK (
        expires_at IS NULL OR expires_at > created_at
    ),
    CONSTRAINT ck_api_keys_revoked_at CHECK (
        revoked_at IS NULL OR revoked_at >= created_at
    ),
    CONSTRAINT ck_api_keys_timestamps CHECK (updated_at >= created_at)
);

CREATE INDEX ix_api_keys_organization_created_id
    ON api_keys (organization_id, created_at DESC, id DESC);
CREATE INDEX ix_api_keys_active_expiry
    ON api_keys (expires_at, id)
    WHERE revoked_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE api_keys;
-- +goose StatementEnd
