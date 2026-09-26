-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

-- Identity links keep the provider-neutral user model ready for the eventual
-- OIDC adapter without storing provider credentials or passwords.
CREATE TABLE identity_links (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL,
    issuer text NOT NULL,
    subject text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    CONSTRAINT fk_identity_links_user FOREIGN KEY (user_id)
        REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT uq_identity_links_issuer_subject UNIQUE (issuer, subject),
    CONSTRAINT uq_identity_links_user_issuer UNIQUE (user_id, issuer),
    CONSTRAINT ck_identity_links_issuer CHECK (
        char_length(btrim(issuer)) BETWEEN 1 AND 255
    ),
    CONSTRAINT ck_identity_links_subject CHECK (
        char_length(btrim(subject)) BETWEEN 1 AND 255
    ),
    CONSTRAINT ck_identity_links_timestamps CHECK (
        last_login_at IS NULL OR last_login_at >= created_at
    )
);

CREATE INDEX ix_identity_links_user ON identity_links (user_id, issuer);

CREATE TABLE memberships (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    user_id uuid NOT NULL,
    role text NOT NULL,
    status text NOT NULL DEFAULT 'active',
    venue_scope uuid[] NOT NULL DEFAULT '{}',
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    revoked_at timestamptz,
    CONSTRAINT fk_memberships_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_memberships_user FOREIGN KEY (user_id)
        REFERENCES users (id) ON DELETE RESTRICT,
    CONSTRAINT uq_memberships_organization_user UNIQUE (organization_id, user_id),
    CONSTRAINT ck_memberships_role CHECK (
        role IN ('owner', 'admin', 'box_office', 'door_manager', 'scanner', 'finance', 'support', 'read_only')
    ),
    CONSTRAINT ck_memberships_status CHECK (status IN ('active', 'revoked')),
    CONSTRAINT ck_memberships_venue_scope CHECK (cardinality(venue_scope) <= 100),
    CONSTRAINT ck_memberships_version CHECK (version > 0),
    CONSTRAINT ck_memberships_revoked_at CHECK (
        (status = 'revoked' AND revoked_at IS NOT NULL)
        OR (status = 'active' AND revoked_at IS NULL)
    ),
    CONSTRAINT ck_memberships_timestamps CHECK (updated_at >= created_at)
);

CREATE INDEX ix_memberships_organization_created_id
    ON memberships (organization_id, created_at DESC, id DESC);
CREATE INDEX ix_memberships_organization_status_role
    ON memberships (organization_id, status, role, id);
CREATE INDEX ix_memberships_user_status
    ON memberships (user_id, status, organization_id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE memberships;
DROP TABLE identity_links;
-- +goose StatementEnd
