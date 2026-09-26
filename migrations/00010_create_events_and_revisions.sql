-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

CREATE TABLE events (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    slug text NOT NULL,
    status text NOT NULL DEFAULT 'draft',
    current_revision bigint NOT NULL DEFAULT 0,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_events_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT uq_events_organization_id UNIQUE (organization_id, id),
    CONSTRAINT uq_events_organization_slug UNIQUE (organization_id, slug),
    CONSTRAINT ck_events_slug CHECK (slug ~ '^[a-z0-9]+(?:-[a-z0-9]+)*$' AND char_length(slug) BETWEEN 2 AND 120),
    CONSTRAINT ck_events_status CHECK (status IN ('draft', 'published', 'unpublished', 'cancelled', 'archived')),
    CONSTRAINT ck_events_revision CHECK (current_revision >= 0),
    CONSTRAINT ck_events_version CHECK (version > 0),
    CONSTRAINT ck_events_timestamps CHECK (updated_at >= created_at)
);

CREATE INDEX ix_events_organization_created_id
    ON events (organization_id, created_at DESC, id DESC);
CREATE INDEX ix_events_organization_status
    ON events (organization_id, status, created_at DESC, id DESC);

CREATE TABLE event_revisions (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    event_id uuid NOT NULL,
    revision bigint NOT NULL DEFAULT 0,
    status text NOT NULL DEFAULT 'draft',
    title text NOT NULL,
    short_description text NOT NULL DEFAULT '',
    long_description text NOT NULL DEFAULT '',
    checksum bytea NOT NULL,
    version bigint NOT NULL DEFAULT 1,
    published_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_event_revisions_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_event_revisions_event_tenant FOREIGN KEY (organization_id, event_id)
        REFERENCES events (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_event_revisions_organization_id UNIQUE (organization_id, id),
    CONSTRAINT ck_event_revisions_status CHECK (status IN ('draft', 'published', 'retired')),
    CONSTRAINT ck_event_revisions_state CHECK (
        (status = 'draft' AND revision = 0 AND published_at IS NULL)
        OR (status IN ('published', 'retired') AND revision > 0 AND published_at IS NOT NULL)
    ),
    CONSTRAINT ck_event_revisions_title CHECK (char_length(btrim(title)) BETWEEN 1 AND 200),
    CONSTRAINT ck_event_revisions_short_description CHECK (char_length(short_description) <= 500),
    CONSTRAINT ck_event_revisions_long_description CHECK (char_length(long_description) <= 10000),
    CONSTRAINT ck_event_revisions_checksum CHECK (octet_length(checksum) = 32),
    CONSTRAINT ck_event_revisions_version CHECK (version > 0),
    CONSTRAINT ck_event_revisions_timestamps CHECK (updated_at >= created_at)
);

CREATE UNIQUE INDEX uq_event_revisions_published_revision
    ON event_revisions (organization_id, event_id, revision)
    WHERE revision > 0;
CREATE INDEX ix_event_revisions_event_created_id
    ON event_revisions (organization_id, event_id, created_at DESC, id DESC);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE event_revisions;
DROP TABLE events;
-- +goose StatementEnd
