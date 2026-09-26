-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE holds
    ADD CONSTRAINT uq_holds_organization_id UNIQUE (organization_id, id);

CREATE TABLE session_seats (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    session_id uuid NOT NULL,
    seat_map_version_id uuid NOT NULL,
    section_key text NOT NULL,
    row_key text NOT NULL,
    seat_key text NOT NULL,
    label text NOT NULL,
    category text NOT NULL DEFAULT '',
    sellable boolean NOT NULL,
    wheelchair boolean NOT NULL DEFAULT false,
    companion_to text,
    state text NOT NULL DEFAULT 'available',
    hold_id uuid,
    order_id uuid,
    hold_expires_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_session_seats_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_session_seats_session FOREIGN KEY (organization_id, session_id)
        REFERENCES sessions (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_session_seats_map FOREIGN KEY (organization_id, seat_map_version_id)
        REFERENCES seat_map_versions (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_session_seats_hold FOREIGN KEY (organization_id, hold_id)
        REFERENCES holds (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_session_seats_organization_id UNIQUE (organization_id, id),
    CONSTRAINT uq_session_seats_session_key UNIQUE (organization_id, session_id, seat_key),
    CONSTRAINT ck_session_seats_keys CHECK (
        char_length(btrim(section_key)) BETWEEN 1 AND 64
        AND char_length(btrim(row_key)) BETWEEN 1 AND 64
        AND char_length(btrim(seat_key)) BETWEEN 1 AND 64
        AND char_length(btrim(label)) BETWEEN 1 AND 64
        AND char_length(category) <= 64
    ),
    CONSTRAINT ck_session_seats_state CHECK (state IN ('available', 'held', 'sold', 'killed', 'comped')),
    CONSTRAINT ck_session_seats_hold_state CHECK (
        (state = 'held' AND hold_id IS NOT NULL AND hold_expires_at IS NOT NULL)
        OR (state <> 'held' AND hold_id IS NULL AND hold_expires_at IS NULL)
    ),
    CONSTRAINT ck_session_seats_version CHECK (version > 0),
    CONSTRAINT ck_session_seats_timestamps CHECK (updated_at >= created_at)
);

CREATE INDEX ix_session_seats_session_state_key
    ON session_seats (organization_id, session_id, state, seat_key, id);
CREATE INDEX ix_session_seats_active_hold_expiry
    ON session_seats (organization_id, hold_expires_at, id)
    WHERE state = 'held';

ALTER TABLE hold_items
    ALTER COLUMN pool_id DROP NOT NULL,
    ADD COLUMN seat_id uuid,
    ADD CONSTRAINT fk_hold_items_seat FOREIGN KEY (organization_id, seat_id)
        REFERENCES session_seats (organization_id, id) ON DELETE RESTRICT,
    ADD CONSTRAINT ck_hold_items_inventory_reference CHECK ((pool_id IS NOT NULL) <> (seat_id IS NOT NULL));

CREATE UNIQUE INDEX uq_hold_items_hold_seat
    ON hold_items (organization_id, hold_id, seat_id)
    WHERE seat_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP INDEX uq_hold_items_hold_seat;
ALTER TABLE hold_items
    DROP CONSTRAINT ck_hold_items_inventory_reference,
    DROP CONSTRAINT fk_hold_items_seat,
    DROP COLUMN seat_id,
    ALTER COLUMN pool_id SET NOT NULL;
DROP TABLE session_seats;
ALTER TABLE holds DROP CONSTRAINT uq_holds_organization_id;
-- +goose StatementEnd
