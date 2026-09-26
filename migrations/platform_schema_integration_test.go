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
	var usersExist, organizationsExist, jobsExist, inboxExists bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('users') IS NOT NULL`).Scan(&usersExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('organizations') IS NOT NULL`).Scan(&organizationsExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('jobs') IS NOT NULL`).Scan(&jobsExist))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('inbox_messages') IS NOT NULL`).Scan(&inboxExists))
	require.True(t, usersExist)
	require.False(t, organizationsExist)
	require.False(t, jobsExist)
	require.False(t, inboxExists)
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
