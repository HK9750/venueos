-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

CREATE TABLE sessions (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    event_id uuid NOT NULL,
    event_revision bigint NOT NULL,
    venue_id uuid NOT NULL,
    space_id uuid NOT NULL,
    seat_map_version_id uuid,
    inventory_mode text NOT NULL,
    doors_at timestamptz,
    starts_at timestamptz NOT NULL,
    ends_at timestamptz NOT NULL,
    sales_start_at timestamptz,
    sales_end_at timestamptz,
    timezone text NOT NULL,
    status text NOT NULL DEFAULT 'scheduled',
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_sessions_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_sessions_event_tenant FOREIGN KEY (organization_id, event_id)
        REFERENCES events (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_sessions_venue_tenant FOREIGN KEY (organization_id, venue_id)
        REFERENCES venues (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_sessions_space_tenant FOREIGN KEY (organization_id, venue_id, space_id)
        REFERENCES spaces (organization_id, venue_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_sessions_seat_map_tenant FOREIGN KEY (organization_id, seat_map_version_id)
        REFERENCES seat_map_versions (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT ck_sessions_event_revision CHECK (event_revision > 0),
    CONSTRAINT ck_sessions_inventory_mode CHECK (inventory_mode IN ('assigned_seating', 'general_admission')),
    CONSTRAINT ck_sessions_times CHECK (ends_at > starts_at AND (doors_at IS NULL OR doors_at <= starts_at)),
    CONSTRAINT ck_sessions_sales_window CHECK (
        (sales_start_at IS NULL AND sales_end_at IS NULL)
        OR (sales_start_at IS NOT NULL AND sales_end_at IS NOT NULL AND sales_start_at < sales_end_at AND sales_end_at <= starts_at)
    ),
    CONSTRAINT ck_sessions_timezone CHECK (char_length(btrim(timezone)) BETWEEN 1 AND 64),
    CONSTRAINT ck_sessions_status CHECK (status IN ('draft', 'scheduled', 'on_sale', 'sales_closed', 'in_progress', 'completed', 'cancelled')),
    CONSTRAINT ck_sessions_version CHECK (version > 0),
    CONSTRAINT ck_sessions_timestamps CHECK (updated_at >= created_at)
);

CREATE INDEX ix_sessions_organization_event_start_id
    ON sessions (organization_id, event_id, starts_at, id);
CREATE INDEX ix_sessions_organization_space_start
    ON sessions (organization_id, venue_id, space_id, starts_at);
CREATE INDEX ix_sessions_organization_status_start
    ON sessions (organization_id, status, starts_at, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE sessions;
-- +goose StatementEnd
