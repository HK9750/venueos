-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

ALTER TABLE gates
    ADD CONSTRAINT uq_gates_organization_id UNIQUE (organization_id, id);

CREATE TABLE entitlements (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    order_line_id uuid NOT NULL,
    session_id uuid NOT NULL,
    unit_number integer NOT NULL,
    state text NOT NULL DEFAULT 'pending',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_entitlements_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_entitlements_order_line FOREIGN KEY (organization_id, order_line_id)
        REFERENCES order_lines (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_entitlements_session FOREIGN KEY (organization_id, session_id)
        REFERENCES sessions (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_entitlements_organization_id UNIQUE (organization_id, id),
    CONSTRAINT uq_entitlements_order_line_unit UNIQUE (organization_id, order_line_id, unit_number),
    CONSTRAINT ck_entitlements_unit CHECK (unit_number > 0 AND unit_number <= 1000),
    CONSTRAINT ck_entitlements_state CHECK (state IN ('pending', 'issued', 'void', 'refunded')),
    CONSTRAINT ck_entitlements_timestamps CHECK (updated_at >= created_at)
);

CREATE INDEX ix_entitlements_session_state
    ON entitlements (organization_id, session_id, state, id);

CREATE TABLE tickets (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    entitlement_id uuid NOT NULL,
    order_line_id uuid NOT NULL,
    session_id uuid NOT NULL,
    public_reference text NOT NULL,
    state text NOT NULL DEFAULT 'valid',
    version bigint NOT NULL DEFAULT 1,
    signing_key_id text NOT NULL,
    not_before timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    entry_opens_at timestamptz NOT NULL,
    entry_closes_at timestamptz NOT NULL,
    issued_at timestamptz NOT NULL,
    admitted_at timestamptz,
    void_at timestamptz,
    refunded_at timestamptz,
    expired_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_tickets_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_tickets_entitlement FOREIGN KEY (organization_id, entitlement_id)
        REFERENCES entitlements (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_tickets_order_line FOREIGN KEY (organization_id, order_line_id)
        REFERENCES order_lines (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_tickets_session FOREIGN KEY (organization_id, session_id)
        REFERENCES sessions (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_tickets_organization_id UNIQUE (organization_id, id),
    CONSTRAINT uq_tickets_public_reference UNIQUE (organization_id, public_reference),
    CONSTRAINT uq_tickets_entitlement UNIQUE (organization_id, entitlement_id),
    CONSTRAINT ck_tickets_public_reference CHECK (public_reference ~ '^[A-Z0-9-]{8,96}$'),
    CONSTRAINT ck_tickets_state CHECK (state IN ('pending', 'valid', 'admitted', 'void', 'refunded', 'expired')),
    CONSTRAINT ck_tickets_version CHECK (version > 0),
    CONSTRAINT ck_tickets_key_id CHECK (signing_key_id ~ '^[A-Za-z0-9._-]{1,128}$'),
    CONSTRAINT ck_tickets_windows CHECK (expires_at > not_before AND entry_closes_at > entry_opens_at),
    CONSTRAINT ck_tickets_timestamps CHECK (updated_at >= created_at AND issued_at >= created_at AND (admitted_at IS NULL OR admitted_at >= created_at))
);

CREATE INDEX ix_tickets_session_state
    ON tickets (organization_id, session_id, state, id);

CREATE TABLE devices (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    venue_id uuid NOT NULL,
    name text NOT NULL,
    platform text NOT NULL,
    app_version text NOT NULL,
    credential_hash bytea NOT NULL,
    state text NOT NULL DEFAULT 'active',
    last_seen_at timestamptz,
    version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_devices_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_devices_venue FOREIGN KEY (organization_id, venue_id)
        REFERENCES venues (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_devices_organization_id UNIQUE (organization_id, id),
    CONSTRAINT uq_devices_credential_hash UNIQUE (credential_hash),
    CONSTRAINT ck_devices_name CHECK (char_length(btrim(name)) BETWEEN 1 AND 160),
    CONSTRAINT ck_devices_platform CHECK (platform ~ '^[a-z0-9._-]{1,32}$'),
    CONSTRAINT ck_devices_app_version CHECK (char_length(btrim(app_version)) BETWEEN 1 AND 64),
    CONSTRAINT ck_devices_credential_hash CHECK (octet_length(credential_hash) = 32),
    CONSTRAINT ck_devices_state CHECK (state IN ('active', 'suspended', 'revoked')),
    CONSTRAINT ck_devices_version CHECK (version > 0),
    CONSTRAINT ck_devices_timestamps CHECK (updated_at >= created_at)
);

CREATE INDEX ix_devices_venue_state
    ON devices (organization_id, venue_id, state, id);

CREATE TABLE scan_attempts (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    session_id uuid NOT NULL,
    ticket_id uuid NOT NULL,
    device_id uuid NOT NULL,
    device_scan_id text NOT NULL,
    ticket_version bigint NOT NULL,
    result text NOT NULL,
    mode text NOT NULL DEFAULT 'online',
    credential_sha256 bytea NOT NULL,
    gate_id uuid,
    scanned_at timestamptz NOT NULL,
    received_at timestamptz NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT fk_scan_attempts_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_scan_attempts_session FOREIGN KEY (organization_id, session_id)
        REFERENCES sessions (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_scan_attempts_ticket FOREIGN KEY (organization_id, ticket_id)
        REFERENCES tickets (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_scan_attempts_device FOREIGN KEY (organization_id, device_id)
        REFERENCES devices (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_scan_attempts_gate FOREIGN KEY (organization_id, gate_id)
        REFERENCES gates (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_scan_attempts_organization_id UNIQUE (organization_id, id),
    CONSTRAINT uq_scan_attempts_device_key UNIQUE (organization_id, device_id, device_scan_id),
    CONSTRAINT ck_scan_attempts_device_scan_id CHECK (device_scan_id ~ '^[A-Za-z0-9._:-]{8,128}$'),
    CONSTRAINT ck_scan_attempts_ticket_version CHECK (ticket_version > 0),
    CONSTRAINT ck_scan_attempts_result CHECK (result IN ('admitted', 'already_admitted', 'unknown_ticket', 'wrong_session', 'outside_entry_window', 'void', 'refunded', 'expired')),
    CONSTRAINT ck_scan_attempts_mode CHECK (mode IN ('online', 'offline')),
    CONSTRAINT ck_scan_attempts_credential_hash CHECK (octet_length(credential_sha256) = 32),
    CONSTRAINT ck_scan_attempts_metadata CHECK (jsonb_typeof(metadata) = 'object' AND octet_length(metadata::text) BETWEEN 2 AND 65536),
    CONSTRAINT ck_scan_attempts_times CHECK (received_at >= scanned_at)
);

CREATE INDEX ix_scan_attempts_ticket_received
    ON scan_attempts (organization_id, ticket_id, received_at DESC, id DESC);

CREATE TABLE admissions (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    ticket_id uuid NOT NULL,
    scan_attempt_id uuid NOT NULL,
    session_id uuid NOT NULL,
    device_id uuid NOT NULL,
    gate_id uuid,
    admitted_at timestamptz NOT NULL,
    override_actor_id text,
    override_reason text,
    CONSTRAINT fk_admissions_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT fk_admissions_ticket FOREIGN KEY (organization_id, ticket_id)
        REFERENCES tickets (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_admissions_scan FOREIGN KEY (organization_id, scan_attempt_id)
        REFERENCES scan_attempts (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_admissions_session FOREIGN KEY (organization_id, session_id)
        REFERENCES sessions (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_admissions_device FOREIGN KEY (organization_id, device_id)
        REFERENCES devices (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT fk_admissions_gate FOREIGN KEY (organization_id, gate_id)
        REFERENCES gates (organization_id, id) ON DELETE RESTRICT,
    CONSTRAINT uq_admissions_organization_id UNIQUE (organization_id, id),
    CONSTRAINT uq_admissions_ticket UNIQUE (organization_id, ticket_id),
    CONSTRAINT uq_admissions_scan UNIQUE (organization_id, scan_attempt_id),
    CONSTRAINT ck_admissions_override CHECK ((override_actor_id IS NULL AND override_reason IS NULL) OR (override_actor_id IS NOT NULL AND override_reason IS NOT NULL AND char_length(btrim(override_reason)) BETWEEN 1 AND 1000))
);

CREATE INDEX ix_admissions_session_time
    ON admissions (organization_id, session_id, admitted_at, id);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TABLE admissions;
DROP TABLE scan_attempts;
DROP TABLE devices;
DROP TABLE tickets;
DROP TABLE entitlements;
ALTER TABLE gates DROP CONSTRAINT uq_gates_organization_id;
-- +goose StatementEnd
