-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

CREATE TABLE invitations (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    normalized_email text NOT NULL,
    role text NOT NULL,
    token_hash bytea NOT NULL,
    status text NOT NULL DEFAULT 'invited',
    expires_at timestamptz NOT NULL,
    created_by_type text NOT NULL,
    created_by_id text,
    membership_id uuid,
    accepted_at timestamptz,
    revoked_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_invitations_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_invitations_membership FOREIGN KEY (membership_id)
        REFERENCES memberships (id) ON DELETE RESTRICT,
    CONSTRAINT uq_invitations_token_hash UNIQUE (token_hash),
    CONSTRAINT ck_invitations_email CHECK (
        char_length(normalized_email) BETWEEN 3 AND 254
        AND normalized_email = lower(btrim(normalized_email))
    ),
    CONSTRAINT ck_invitations_role CHECK (
        role IN ('owner', 'admin', 'box_office', 'door_manager', 'scanner', 'finance', 'support', 'read_only')
    ),
    CONSTRAINT ck_invitations_status CHECK (
        status IN ('invited', 'accepted', 'expired', 'revoked', 'cancelled')
    ),
    CONSTRAINT ck_invitations_token_hash CHECK (octet_length(token_hash) = 32),
    CONSTRAINT ck_invitations_expiry CHECK (expires_at > created_at),
    CONSTRAINT ck_invitations_version CHECK (version > 0),
    CONSTRAINT ck_invitations_terminal_metadata CHECK (
        (status = 'accepted' AND membership_id IS NOT NULL AND accepted_at IS NOT NULL AND revoked_at IS NULL)
        OR (status IN ('expired', 'revoked', 'cancelled') AND accepted_at IS NULL)
        OR (status = 'invited' AND membership_id IS NULL AND accepted_at IS NULL AND revoked_at IS NULL)
    ),
    CONSTRAINT ck_invitations_revoked_metadata CHECK (
        (status = 'revoked' AND revoked_at IS NOT NULL) OR (status <> 'revoked' AND revoked_at IS NULL)
    ),
    CONSTRAINT ck_invitations_timestamps CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX uq_invitations_active_email
    ON invitations (organization_id, normalized_email)
    WHERE status = 'invited';
CREATE INDEX ix_invitations_expiry
    ON invitations (status, expires_at)
    WHERE status = 'invited';
CREATE INDEX ix_invitations_organization_created_id
    ON invitations (organization_id, created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE invitations;
-- +goose StatementEnd
