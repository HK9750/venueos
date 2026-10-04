-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

-- Private signing material is owned by the configured secret/KMS adapter and is
-- never persisted here. PostgreSQL stores only the public verification key and
-- its rotation state so API and worker processes share one durable authority.
CREATE TABLE ticket_signing_keys (
    key_id text PRIMARY KEY,
    public_key bytea NOT NULL,
    state text NOT NULL DEFAULT 'active',
    version bigint NOT NULL DEFAULT 1,
    activated_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT ck_ticket_signing_keys_id CHECK (key_id ~ '^[A-Za-z0-9._-]{1,128}$'),
    CONSTRAINT ck_ticket_signing_keys_public_key CHECK (octet_length(public_key) = 32),
    CONSTRAINT ck_ticket_signing_keys_state CHECK (state IN ('active', 'verify_only', 'revoked')),
    CONSTRAINT ck_ticket_signing_keys_version CHECK (version > 0),
    CONSTRAINT ck_ticket_signing_keys_revocation CHECK ((state = 'revoked' AND revoked_at IS NOT NULL) OR (state <> 'revoked' AND revoked_at IS NULL)),
    CONSTRAINT ck_ticket_signing_keys_timestamps CHECK (updated_at >= created_at AND activated_at >= created_at AND (revoked_at IS NULL OR revoked_at >= activated_at))
);

CREATE INDEX ix_ticket_signing_keys_state
    ON ticket_signing_keys (state, key_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE ticket_signing_keys;
-- +goose StatementEnd
