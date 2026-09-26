-- +goose Up
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

CREATE TABLE organizations (
    id uuid PRIMARY KEY,
    slug text NOT NULL,
    display_name text NOT NULL,
    status text NOT NULL DEFAULT 'active',
    default_locale text NOT NULL,
    default_timezone text NOT NULL,
    default_currency character(3) NOT NULL,
    settings_version bigint NOT NULL DEFAULT 1,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT uq_organizations_slug UNIQUE (slug),
    CONSTRAINT ck_organizations_slug CHECK (
        char_length(slug) BETWEEN 3 AND 63
        AND slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'
    ),
    CONSTRAINT ck_organizations_display_name CHECK (
        char_length(btrim(display_name)) BETWEEN 1 AND 200
    ),
    CONSTRAINT ck_organizations_status CHECK (status IN ('active', 'suspended', 'closed')),
    CONSTRAINT ck_organizations_default_locale CHECK (
        char_length(btrim(default_locale)) BETWEEN 2 AND 35
    ),
    CONSTRAINT ck_organizations_default_timezone CHECK (
        char_length(btrim(default_timezone)) BETWEEN 1 AND 255
    ),
    CONSTRAINT ck_organizations_default_currency CHECK (
        default_currency ~ '^[A-Z]{3}$'
    ),
    CONSTRAINT ck_organizations_settings_version CHECK (settings_version > 0),
    CONSTRAINT ck_organizations_timestamps CHECK (updated_at >= created_at)
);

CREATE INDEX ix_organizations_status_created_id
    ON organizations (status, created_at DESC, id DESC);

CREATE TABLE idempotency_records (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    principal_type text NOT NULL,
    principal_id text NOT NULL,
    operation text NOT NULL,
    idempotency_key text NOT NULL,
    request_fingerprint bytea NOT NULL,
    state text NOT NULL,
    locked_until timestamptz,
    response_status smallint,
    response_body jsonb,
    resource_type text,
    resource_id text,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_idempotency_records_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT uq_idempotency_records_scope_key UNIQUE (
        organization_id, principal_type, principal_id, operation, idempotency_key
    ),
    CONSTRAINT ck_idempotency_records_principal_type CHECK (
        principal_type ~ '^[a-z][a-z0-9_]{0,63}$'
    ),
    CONSTRAINT ck_idempotency_records_principal_id CHECK (
        char_length(principal_id) BETWEEN 1 AND 200
    ),
    CONSTRAINT ck_idempotency_records_operation CHECK (
        operation ~ '^[a-z][a-z0-9_.]{0,127}$'
    ),
    CONSTRAINT ck_idempotency_records_key CHECK (
        char_length(idempotency_key) BETWEEN 16 AND 128
        AND idempotency_key ~ '^[A-Za-z0-9._:-]+$'
    ),
    CONSTRAINT ck_idempotency_records_fingerprint CHECK (
        octet_length(request_fingerprint) = 32
    ),
    CONSTRAINT ck_idempotency_records_state CHECK (
        state IN ('processing', 'completed', 'failed')
    ),
    CONSTRAINT ck_idempotency_records_state_fields CHECK (
        (
            state = 'processing'
            AND locked_until IS NOT NULL
            AND response_status IS NULL
            AND response_body IS NULL
        )
        OR (
            state IN ('completed', 'failed')
            AND locked_until IS NULL
            AND response_status IS NOT NULL
            AND response_status BETWEEN 100 AND 599
        )
    ),
    CONSTRAINT ck_idempotency_records_resource_pair CHECK (
        (resource_type IS NULL) = (resource_id IS NULL)
    ),
    CONSTRAINT ck_idempotency_records_expiry CHECK (expires_at > created_at),
    CONSTRAINT ck_idempotency_records_timestamps CHECK (updated_at >= created_at),
    CONSTRAINT ck_idempotency_records_response_size CHECK (
        response_body IS NULL OR octet_length(response_body::text) <= 262144
    )
);

CREATE INDEX ix_idempotency_records_organization_expires
    ON idempotency_records (organization_id, expires_at, id);
CREATE INDEX ix_idempotency_records_processing_lease
    ON idempotency_records (locked_until, id)
    WHERE state = 'processing';

CREATE TABLE outbox_events (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    aggregate_type text NOT NULL,
    aggregate_id uuid NOT NULL,
    aggregate_version bigint NOT NULL,
    event_type text NOT NULL,
    schema_version integer NOT NULL,
    payload jsonb NOT NULL,
    correlation_id text,
    causation_id uuid,
    state text NOT NULL DEFAULT 'pending',
    available_at timestamptz NOT NULL DEFAULT now(),
    attempt_count integer NOT NULL DEFAULT 0,
    lease_owner text,
    lease_expires_at timestamptz,
    published_at timestamptz,
    dead_lettered_at timestamptz,
    last_error_code text,
    last_error_message text,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_outbox_events_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT uq_outbox_events_aggregate_event UNIQUE (
        organization_id, aggregate_type, aggregate_id, aggregate_version, event_type
    ),
    CONSTRAINT ck_outbox_events_aggregate_type CHECK (
        aggregate_type ~ '^[a-z][a-z0-9_]{0,63}$'
    ),
    CONSTRAINT ck_outbox_events_aggregate_version CHECK (aggregate_version > 0),
    CONSTRAINT ck_outbox_events_event_type CHECK (
        event_type ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$'
    ),
    CONSTRAINT ck_outbox_events_schema_version CHECK (schema_version > 0),
    CONSTRAINT ck_outbox_events_payload CHECK (
        jsonb_typeof(payload) = 'object'
        AND octet_length(payload::text) <= 262144
    ),
    CONSTRAINT ck_outbox_events_correlation_id CHECK (
        correlation_id IS NULL OR char_length(correlation_id) BETWEEN 1 AND 128
    ),
    CONSTRAINT ck_outbox_events_state CHECK (
        state IN ('pending', 'leased', 'published', 'dead_letter')
    ),
    CONSTRAINT ck_outbox_events_attempt_count CHECK (attempt_count >= 0),
    CONSTRAINT ck_outbox_events_state_fields CHECK (
        (
            state = 'pending'
            AND lease_owner IS NULL
            AND lease_expires_at IS NULL
            AND published_at IS NULL
            AND dead_lettered_at IS NULL
        )
        OR (
            state = 'leased'
            AND lease_owner IS NOT NULL
            AND char_length(lease_owner) BETWEEN 1 AND 128
            AND lease_expires_at IS NOT NULL
            AND published_at IS NULL
            AND dead_lettered_at IS NULL
        )
        OR (
            state = 'published'
            AND lease_owner IS NULL
            AND lease_expires_at IS NULL
            AND published_at IS NOT NULL
            AND dead_lettered_at IS NULL
        )
        OR (
            state = 'dead_letter'
            AND lease_owner IS NULL
            AND lease_expires_at IS NULL
            AND published_at IS NULL
            AND dead_lettered_at IS NOT NULL
        )
    ),
    CONSTRAINT ck_outbox_events_last_error CHECK (
        (last_error_code IS NULL AND last_error_message IS NULL)
        OR (
            last_error_code IS NOT NULL
            AND last_error_message IS NOT NULL
            AND char_length(last_error_code) BETWEEN 1 AND 64
            AND char_length(last_error_message) BETWEEN 1 AND 1000
        )
    ),
    CONSTRAINT ck_outbox_events_times CHECK (
        available_at >= occurred_at AND created_at >= occurred_at
    )
);

CREATE INDEX ix_outbox_events_claim
    ON outbox_events (available_at, occurred_at, id)
    WHERE state = 'pending';
CREATE INDEX ix_outbox_events_expired_lease
    ON outbox_events (lease_expires_at, id)
    WHERE state = 'leased';
CREATE INDEX ix_outbox_events_organization_aggregate
    ON outbox_events (organization_id, aggregate_type, aggregate_id, aggregate_version);

CREATE TABLE audit_entries (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL,
    actor_type text NOT NULL,
    actor_id text,
    effective_actor_type text,
    effective_actor_id text,
    action text NOT NULL,
    subject_type text NOT NULL,
    subject_id text NOT NULL,
    result text NOT NULL,
    reason text,
    request_id text,
    trace_id text,
    source_ip inet,
    device_id uuid,
    before_data jsonb,
    after_data jsonb,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fk_audit_entries_organization FOREIGN KEY (organization_id)
        REFERENCES organizations (id) ON DELETE RESTRICT,
    CONSTRAINT ck_audit_entries_actor_type CHECK (
        actor_type IN ('user', 'api_key', 'device', 'support', 'system')
    ),
    CONSTRAINT ck_audit_entries_actor_id CHECK (
        (actor_type = 'system' AND actor_id IS NULL)
        OR (
            actor_type <> 'system'
            AND actor_id IS NOT NULL
            AND char_length(actor_id) BETWEEN 1 AND 200
        )
    ),
    CONSTRAINT ck_audit_entries_effective_actor CHECK (
        (effective_actor_type IS NULL AND effective_actor_id IS NULL)
        OR (
            effective_actor_type IS NOT NULL
            AND effective_actor_id IS NOT NULL
            AND effective_actor_type IN ('user', 'api_key', 'device', 'support')
            AND char_length(effective_actor_id) BETWEEN 1 AND 200
        )
    ),
    CONSTRAINT ck_audit_entries_action CHECK (
        action ~ '^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$'
    ),
    CONSTRAINT ck_audit_entries_subject_type CHECK (
        subject_type ~ '^[a-z][a-z0-9_]{0,63}$'
    ),
    CONSTRAINT ck_audit_entries_subject_id CHECK (
        char_length(subject_id) BETWEEN 1 AND 200
    ),
    CONSTRAINT ck_audit_entries_result CHECK (result IN ('success', 'denied', 'failure')),
    CONSTRAINT ck_audit_entries_reason CHECK (
        reason IS NULL OR char_length(reason) BETWEEN 1 AND 1000
    ),
    CONSTRAINT ck_audit_entries_request_id CHECK (
        request_id IS NULL OR char_length(request_id) BETWEEN 1 AND 128
    ),
    CONSTRAINT ck_audit_entries_trace_id CHECK (
        trace_id IS NULL OR char_length(trace_id) BETWEEN 1 AND 64
    ),
    CONSTRAINT ck_audit_entries_before_data CHECK (
        before_data IS NULL OR (
            jsonb_typeof(before_data) = 'object'
            AND octet_length(before_data::text) <= 65536
        )
    ),
    CONSTRAINT ck_audit_entries_after_data CHECK (
        after_data IS NULL OR (
            jsonb_typeof(after_data) = 'object'
            AND octet_length(after_data::text) <= 65536
        )
    )
);

CREATE INDEX ix_audit_entries_organization_occurred_id
    ON audit_entries (organization_id, occurred_at DESC, id DESC);
CREATE INDEX ix_audit_entries_organization_subject
    ON audit_entries (organization_id, subject_type, subject_id, occurred_at DESC);
CREATE INDEX ix_audit_entries_organization_actor
    ON audit_entries (organization_id, actor_type, actor_id, occurred_at DESC);

CREATE FUNCTION prevent_audit_entry_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'audit entries are append-only' USING ERRCODE = '55000';
END;
$$;

CREATE TRIGGER trg_audit_entries_prevent_update
BEFORE UPDATE ON audit_entries
FOR EACH ROW EXECUTE FUNCTION prevent_audit_entry_mutation();

CREATE TRIGGER trg_audit_entries_prevent_delete
BEFORE DELETE ON audit_entries
FOR EACH ROW EXECUTE FUNCTION prevent_audit_entry_mutation();
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '30s';

DROP TRIGGER trg_audit_entries_prevent_delete ON audit_entries;
DROP TRIGGER trg_audit_entries_prevent_update ON audit_entries;
DROP FUNCTION prevent_audit_entry_mutation();
DROP TABLE audit_entries;
DROP TABLE outbox_events;
DROP TABLE idempotency_records;
DROP TABLE organizations;
-- +goose StatementEnd
