//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/HK9750/venueos/migrations"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestPlatformTenancyMigration(t *testing.T) {
	ctx := context.Background()
	container, err := postgrescontainer.Run(ctx,
		"postgres:18-alpine",
		postgrescontainer.WithDatabase("app"),
		postgrescontainer.WithUsername("app"),
		postgrescontainer.WithPassword("app"),
		postgrescontainer.BasicWaitStrategies(),
	)
	require.NoError(t, err)
	testcontainers.CleanupContainer(t, container)

	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	db, err := sql.Open("pgx", databaseURL)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	goose.SetBaseFS(migrations.FS)
	require.NoError(t, goose.SetDialect("postgres"))
	require.NoError(t, goose.UpContext(ctx, db, "."))

	organizationID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO organizations (
			id, slug, display_name, default_locale, default_timezone, default_currency
		) VALUES ($1, 'example-venue', 'Example Venue', 'en-US', 'Asia/Karachi', 'PKR')
	`, organizationID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO organizations (
			id, slug, display_name, default_locale, default_timezone, default_currency
		) VALUES ($1, 'Invalid Slug', 'Invalid', 'en', 'UTC', 'USD')
	`, uuid.New())
	assertConstraintViolation(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO organizations (
			id, slug, display_name, default_locale, default_timezone, default_currency
		) VALUES ($1, 'too-long-name', repeat('x', 121), 'en', 'UTC', 'USD')
	`, uuid.New())
	assertConstraintViolation(t, err)

	idempotencyID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO idempotency_records (
			id, organization_id, principal_type, principal_id, operation,
			idempotency_key, request_fingerprint, state, locked_until, expires_at
		) VALUES ($1, $2, 'user', 'user-1', 'organization.create',
			'0123456789abcdef', decode(repeat('01', 32), 'hex'), 'processing',
			now() + interval '30 seconds', now() + interval '24 hours')
	`, idempotencyID, organizationID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO idempotency_records (
			id, organization_id, principal_type, principal_id, operation,
			idempotency_key, request_fingerprint, state, locked_until, expires_at
		) VALUES ($1, $2, 'user', 'user-1', 'organization.create',
			'0123456789abcdef', decode(repeat('02', 32), 'hex'), 'processing',
			now() + interval '30 seconds', now() + interval '24 hours')
	`, uuid.New(), organizationID)
	assertUniqueViolation(t, err)

	_, err = db.ExecContext(ctx, `
		INSERT INTO idempotency_records (
			id, organization_id, principal_type, principal_id, operation,
			idempotency_key, request_fingerprint, state, locked_until, expires_at
		) VALUES ($1, NULL, 'platform', 'onboarding-service', 'organization.create',
			'platform-key-0001', decode(repeat('03', 32), 'hex'), 'processing',
			now() + interval '30 seconds', now() + interval '24 hours')
	`, uuid.New())
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO idempotency_records (
			id, organization_id, principal_type, principal_id, operation,
			idempotency_key, request_fingerprint, state, locked_until, expires_at
		) VALUES ($1, NULL, 'platform', 'onboarding-service', 'organization.create',
			'platform-key-0001', decode(repeat('04', 32), 'hex'), 'processing',
			now() + interval '30 seconds', now() + interval '24 hours')
	`, uuid.New())
	assertNamedUniqueViolation(t, err, "uq_idempotency_records_scope_key")
	_, err = db.ExecContext(ctx, `
		INSERT INTO idempotency_records (
			id, organization_id, principal_type, principal_id, operation,
			idempotency_key, request_fingerprint, state, locked_until, expires_at
		) VALUES ($1, NULL, 'user', 'user-1', 'organization.create',
			'platform-key-0002', decode(repeat('05', 32), 'hex'), 'processing',
			now() + interval '30 seconds', now() + interval '24 hours')
	`, uuid.New())
	assertConstraintViolation(t, err)

	outboxID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO outbox_events (
			id, organization_id, aggregate_type, aggregate_id, aggregate_version,
			event_type, schema_version, payload
		) VALUES ($1, $2, 'organization', $2, 1, 'organization.created', 1, '{}')
	`, outboxID, organizationID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO outbox_events (
			id, organization_id, aggregate_type, aggregate_id, aggregate_version,
			event_type, schema_version, payload
		) VALUES ($1, $2, 'organization', $2, 2, 'organization.updated', 1, '[]')
	`, uuid.New(), organizationID)
	assertConstraintViolation(t, err)
	auditID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO audit_entries (
			id, organization_id, actor_type, actor_id, action, subject_type,
			subject_id, result, request_id, after_data
		) VALUES ($1, $2, 'user', 'user-1', 'organization.created',
			'organization', $3, 'success', 'request-1', '{"status":"active"}')
	`, auditID, organizationID, organizationID.String())
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO audit_entries (
			id, organization_id, actor_type, actor_id, action, subject_type,
			subject_id, result
		) VALUES ($1, $2, 'platform', 'onboarding-service', 'organization.created',
			'organization', $3, 'success')
	`, uuid.New(), organizationID, organizationID.String())
	require.NoError(t, err)

	userID := uuid.New()
	secondUserID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO users (id, email, name)
		VALUES ($1, 'owner@example.com', 'Owner'), ($2, 'admin@example.com', 'Admin')
	`, userID, secondUserID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO identity_links (id, user_id, issuer, subject)
		VALUES ($1, $2, 'https://issuer.example', 'owner-subject')
	`, uuid.New(), userID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO identity_links (id, user_id, issuer, subject)
		VALUES ($1, $2, 'https://issuer.example', 'owner-subject')
	`, uuid.New(), secondUserID)
	assertNamedUniqueViolation(t, err, "uq_identity_links_issuer_subject")

	membershipID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO memberships (id, organization_id, user_id, role)
		VALUES ($1, $2, $3, 'owner')
	`, membershipID, organizationID, userID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO memberships (id, organization_id, user_id, role)
		VALUES ($1, $2, $3, 'not-a-role')
	`, uuid.New(), organizationID, secondUserID)
	assertConstraintViolation(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO memberships (id, organization_id, user_id, role)
		VALUES ($1, $2, $3, 'admin')
	`, uuid.New(), organizationID, userID)
	assertNamedUniqueViolation(t, err, "uq_memberships_organization_user")

	invitationID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO invitations (
			id, organization_id, normalized_email, role, token_hash, expires_at,
			created_by_type, created_by_id
		) VALUES ($1, $2, 'staff@example.com', 'finance', decode(repeat('11', 32), 'hex'),
			now() + interval '7 days', 'user', 'owner-user')
	`, invitationID, organizationID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO invitations (
			id, organization_id, normalized_email, role, token_hash, expires_at,
			created_by_type, created_by_id
		) VALUES ($1, $2, 'staff@example.com', 'finance', decode(repeat('22', 32), 'hex'),
			now() + interval '7 days', 'user', 'owner-user')
	`, uuid.New(), organizationID)
	assertNamedUniqueViolation(t, err, "uq_invitations_active_email")
	_, err = db.ExecContext(ctx, `
		INSERT INTO invitations (
			id, organization_id, normalized_email, role, token_hash, status, expires_at,
			created_by_type, created_by_id
		) VALUES ($1, $2, 'bad@example.com', 'finance', decode(repeat('33', 32), 'hex'), 'accepted',
			now() + interval '7 days', 'user', 'owner-user')
	`, uuid.New(), organizationID)
	assertConstraintViolation(t, err)

	venueID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO venues (id, organization_id, slug, display_name, timezone)
		VALUES ($1, $2, 'main-hall', 'Main Hall', 'Asia/Karachi')
	`, venueID, organizationID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO venues (id, organization_id, slug, display_name, timezone)
		VALUES ($1, $2, 'main-hall', 'Duplicate Hall', 'UTC')
	`, uuid.New(), organizationID)
	assertNamedUniqueViolation(t, err, "uq_venues_organization_slug")
	spaceID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO spaces (id, organization_id, venue_id, slug, display_name, inventory_mode, physical_capacity)
		VALUES ($1, $2, $3, 'floor', 'Floor', 'general_admission', 500)
	`, spaceID, organizationID, venueID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO spaces (id, organization_id, venue_id, slug, display_name, inventory_mode, physical_capacity)
		VALUES ($1, $2, $3, 'bad', 'Bad', 'not-a-mode', 500)
	`, uuid.New(), organizationID, venueID)
	assertConstraintViolation(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO spaces (id, organization_id, venue_id, slug, display_name, inventory_mode, physical_capacity)
		VALUES ($1, $2, $3, 'cross-tenant', 'Cross Tenant', 'general_admission', 500)
	`, uuid.New(), uuid.New(), venueID)
	var foreignKeyError *pgconn.PgError
	require.True(t, errors.As(err, &foreignKeyError))
	require.Equal(t, "23503", foreignKeyError.Code)
	gateID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO gates (id, organization_id, venue_id, space_id, code, display_name)
		VALUES ($1, $2, $3, $4, 'north', 'North Gate')
	`, gateID, organizationID, venueID, spaceID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO gates (id, organization_id, venue_id, code, display_name)
		VALUES ($1, $2, $3, 'north', 'Duplicate Gate')
	`, uuid.New(), organizationID, venueID)
	assertNamedUniqueViolation(t, err, "uq_gates_organization_venue_code")
	poolID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO ga_pool_templates (id, organization_id, space_id, slug, display_name, physical_capacity, sellable_capacity)
		VALUES ($1, $2, $3, 'floor', 'Floor Pool', 500, 450)
	`, poolID, organizationID, spaceID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO ga_pool_templates (id, organization_id, space_id, slug, display_name, physical_capacity, sellable_capacity)
		VALUES ($1, $2, $3, 'invalid', 'Invalid Pool', 100, 101)
	`, uuid.New(), organizationID, spaceID)
	assertConstraintViolation(t, err)
	seatMapID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO seat_map_versions (
			id, organization_id, space_id, name, checksum, content, seat_count, sellable_count
		) VALUES ($1, $2, $3, 'Main Map', decode(repeat('aa', 32), 'hex'),
			'{"sections":[{"id":"main","label":"Main","rows":[]}]}'::jsonb, 1, 1)
	`, seatMapID, organizationID, spaceID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO seat_map_versions (
			id, organization_id, space_id, name, checksum, content, seat_count, sellable_count
		) VALUES ($1, $2, $3, 'Invalid Map', decode(repeat('bb', 32), 'hex'), '{}', 1, 2)
	`, uuid.New(), organizationID, spaceID)
	assertConstraintViolation(t, err)
	eventID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO events (id, organization_id, slug, status)
		VALUES ($1, $2, 'summer-show', 'draft')
	`, eventID, organizationID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO event_revisions (id, organization_id, event_id, title, checksum)
		VALUES ($1, $2, $3, 'Summer Show', decode(repeat('cc', 32), 'hex'))
	`, uuid.New(), organizationID, eventID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO events (id, organization_id, slug, status)
		VALUES ($1, $2, 'summer-show', 'draft')
	`, uuid.New(), organizationID)
	assertNamedUniqueViolation(t, err, "uq_events_organization_slug")
	_, err = db.ExecContext(ctx, `
		INSERT INTO event_revisions (id, organization_id, event_id, title, checksum)
		VALUES ($1, $2, $3, '', decode(repeat('dd', 32), 'hex'))
	`, uuid.New(), organizationID, eventID)
	assertConstraintViolation(t, err)
	sessionID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO sessions (
			id, organization_id, event_id, event_revision, venue_id, space_id,
			inventory_mode, starts_at, ends_at, timezone
		) VALUES ($1, $2, $3, 1, $4, $5, 'general_admission',
			now() + interval '2 hours', now() + interval '4 hours', 'UTC')
	`, sessionID, organizationID, eventID, venueID, spaceID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO sessions (
			id, organization_id, event_id, event_revision, venue_id, space_id,
			inventory_mode, starts_at, ends_at, timezone
		) VALUES ($1, $2, $3, 1, $4, $5, 'general_admission',
			now() + interval '4 hours', now() + interval '2 hours', 'UTC')
	`, uuid.New(), organizationID, eventID, venueID, spaceID)
	assertConstraintViolation(t, err)
	priceTierID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO price_tiers (
			id, organization_id, session_id, slug, display_name, currency, amount_minor
		) VALUES ($1, $2, $3, 'standard', 'Standard', 'USD', 2500)
	`, priceTierID, organizationID, sessionID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO price_tiers (
			id, organization_id, session_id, slug, display_name, currency, amount_minor
		) VALUES ($1, $2, $3, 'standard', 'Duplicate', 'USD', 3000)
	`, uuid.New(), organizationID, sessionID)
	assertNamedUniqueViolation(t, err, "uq_price_tiers_session_slug")
	_, err = db.ExecContext(ctx, `
		INSERT INTO price_tiers (
			id, organization_id, session_id, slug, display_name, currency, amount_minor
		) VALUES ($1, $2, $3, 'invalid', 'Invalid', 'USD', -1)
	`, uuid.New(), organizationID, sessionID)
	assertConstraintViolation(t, err)
	channelID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO sales_channels (
			id, organization_id, channel_key, display_name, channel_type
		) VALUES ($1, $2, 'public', 'Public', 'public')
	`, channelID, organizationID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO sales_channels (
			id, organization_id, channel_key, display_name, channel_type
		) VALUES ($1, $2, 'public', 'Duplicate', 'public')
	`, uuid.New(), organizationID)
	assertNamedUniqueViolation(t, err, "uq_sales_channels_organization_key")
	_, err = db.ExecContext(ctx, `
		INSERT INTO sales_channels (
			id, organization_id, channel_key, display_name, channel_type
		) VALUES ($1, $2, 'partner', 'Invalid', 'unknown')
	`, uuid.New(), organizationID)
	assertConstraintViolation(t, err)
	sessionPoolID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO session_ga_pools (
			id, organization_id, session_id, source_pool_id, slug, display_name,
			physical_capacity, sellable_capacity
		) VALUES ($1, $2, $3, $4, 'floor', 'Floor', 500, 450)
	`, sessionPoolID, organizationID, sessionID, poolID)
	require.NoError(t, err)
	holdID := uuid.New()
	_, err = db.ExecContext(ctx, `
		INSERT INTO holds (
			id, organization_id, session_id, owner_token_hash, owner_user_id,
			currency, expires_at
		) VALUES ($1, $2, $3, decode(repeat('aa', 32), 'hex'), $4, 'USD', now() + interval '10 minutes')
	`, holdID, organizationID, sessionID, userID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO hold_items (
			id, organization_id, hold_id, session_id, pool_id, quantity
		) VALUES ($1, $2, $3, $4, $5, 2)
	`, uuid.New(), organizationID, holdID, sessionID, sessionPoolID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `UPDATE audit_entries SET reason = 'changed' WHERE id = $1`, auditID)
	var postgresError *pgconn.PgError
	require.True(t, errors.As(err, &postgresError))
	require.Equal(t, "55000", postgresError.Code)

	_, err = db.ExecContext(ctx, `DELETE FROM audit_entries WHERE id = $1`, auditID)
	require.True(t, errors.As(err, &postgresError))
	require.Equal(t, "55000", postgresError.Code)

	_, err = db.ExecContext(ctx, `
		INSERT INTO inbox_messages (id, organization_id, source, message_id, payload_sha256)
		VALUES ($1, $2, 'stripe', 'event-1', decode(repeat('01', 32), 'hex'))
	`, uuid.New(), organizationID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO inbox_messages (id, organization_id, source, message_id, payload_sha256)
		VALUES ($1, $2, 'stripe', 'event-1', decode(repeat('02', 32), 'hex'))
	`, uuid.New(), organizationID)
	assertNamedUniqueViolation(t, err, "uq_inbox_messages_source_message")

	_, err = db.ExecContext(ctx, `
		INSERT INTO jobs (id, job_type, schema_version, dedupe_key, payload, max_attempts)
		VALUES ($1, 'platform.cleanup', 1, 'cleanup-1', '{}', 3)
	`, uuid.New())
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
		INSERT INTO jobs (id, job_type, schema_version, dedupe_key, payload, max_attempts)
		VALUES ($1, 'platform.cleanup', 1, 'cleanup-1', '{}', 3)
	`, uuid.New())
	assertNamedUniqueViolation(t, err, "uq_jobs_dedupe")
	_, err = db.ExecContext(ctx, `
		INSERT INTO jobs (
			id, job_type, schema_version, dedupe_key, payload, max_attempts,
			last_error_message
		) VALUES ($1, 'platform.cleanup', 1, 'cleanup-2', '{}', 3, 'missing class')
	`, uuid.New())
	assertConstraintViolation(t, err)

	for _, index := range []string{
		"ix_idempotency_records_processing_lease",
		"ix_outbox_events_claim",
		"ix_audit_entries_organization_occurred_id",
		"ix_inbox_messages_unprocessed",
		"ix_jobs_claim",
		"ix_jobs_expired_lease",
	} {
		var exists bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, index).Scan(&exists))
		require.Truef(t, exists, "index %s does not exist", index)
	}

	require.NoError(t, goose.DownToContext(ctx, db, ".", 1))
	var usersExist, organizationsExist, jobsExist, inboxExists, membershipsExist, identityLinksExist, invitationsExist, apiKeysExist, venuesExist, spacesExist, gatesExist, poolsExist, seatMapsExist, eventsExist, eventRevisionsExist, sessionsExist, priceTiersExist, salesChannelsExist, sessionPoolsExist, holdsExist, holdItemsExist, sessionSeatsExist, realtimeEventsExist, jobSchedulesExist bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('users') IS NOT NULL`).Scan(&usersExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('organizations') IS NOT NULL`).Scan(&organizationsExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('jobs') IS NOT NULL`).Scan(&jobsExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('inbox_messages') IS NOT NULL`).Scan(&inboxExists))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('memberships') IS NOT NULL`).Scan(&membershipsExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('identity_links') IS NOT NULL`).Scan(&identityLinksExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('invitations') IS NOT NULL`).Scan(&invitationsExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('api_keys') IS NOT NULL`).Scan(&apiKeysExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('venues') IS NOT NULL`).Scan(&venuesExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('spaces') IS NOT NULL`).Scan(&spacesExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('gates') IS NOT NULL`).Scan(&gatesExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('ga_pool_templates') IS NOT NULL`).Scan(&poolsExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('seat_map_versions') IS NOT NULL`).Scan(&seatMapsExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('events') IS NOT NULL`).Scan(&eventsExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('event_revisions') IS NOT NULL`).Scan(&eventRevisionsExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('sessions') IS NOT NULL`).Scan(&sessionsExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('price_tiers') IS NOT NULL`).Scan(&priceTiersExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('sales_channels') IS NOT NULL`).Scan(&salesChannelsExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('session_ga_pools') IS NOT NULL`).Scan(&sessionPoolsExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('holds') IS NOT NULL`).Scan(&holdsExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('hold_items') IS NOT NULL`).Scan(&holdItemsExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('session_seats') IS NOT NULL`).Scan(&sessionSeatsExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('realtime_events') IS NOT NULL`).Scan(&realtimeEventsExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('job_schedules') IS NOT NULL`).Scan(&jobSchedulesExist))
	require.True(t, usersExist)
	require.False(t, organizationsExist)
	require.False(t, jobsExist)
	require.False(t, inboxExists)
	require.False(t, membershipsExist)
	require.False(t, identityLinksExist)
	require.False(t, invitationsExist)
	require.False(t, apiKeysExist)
	require.False(t, venuesExist)
	require.False(t, spacesExist)
	require.False(t, gatesExist)
	require.False(t, poolsExist)
	require.False(t, seatMapsExist)
	require.False(t, eventsExist)
	require.False(t, eventRevisionsExist)
	require.False(t, sessionsExist)
	require.False(t, priceTiersExist)
	require.False(t, salesChannelsExist)
	require.False(t, sessionPoolsExist)
	require.False(t, holdsExist)
	require.False(t, holdItemsExist)
	require.False(t, sessionSeatsExist)
	require.False(t, realtimeEventsExist)
	require.False(t, jobSchedulesExist)
}

func assertConstraintViolation(t *testing.T, err error) {
	t.Helper()
	var postgresError *pgconn.PgError
	require.True(t, errors.As(err, &postgresError), "error = %v", err)
	require.Equal(t, "23514", postgresError.Code)
}

func assertUniqueViolation(t *testing.T, err error) {
	assertNamedUniqueViolation(t, err, "uq_idempotency_records_scope_key")
}

func assertNamedUniqueViolation(t *testing.T, err error, constraint string) {
	t.Helper()
	var postgresError *pgconn.PgError
	require.True(t, errors.As(err, &postgresError), "error = %v", err)
	require.Equal(t, "23505", postgresError.Code)
	require.Equal(t, constraint, postgresError.ConstraintName)
}
