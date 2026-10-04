//go:build integration

package postgres_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/audit"
	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/organization"
	organizationpostgres "github.com/HK9750/venueos/internal/organization/postgres"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/inbox"
	"github.com/HK9750/venueos/internal/platform/jobqueue"
	"github.com/HK9750/venueos/internal/platform/money"
	platformpostgres "github.com/HK9750/venueos/internal/platform/postgres"
	"github.com/HK9750/venueos/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestAtomicPlatformRecords(t *testing.T) {
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

	pool, err := pgxpool.New(ctx, databaseURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	runner, err := database.NewTransactionRunner(pool, database.DefaultRetryPolicy())
	require.NoError(t, err)
	store := platformpostgres.NewStore(pool)

	organizationID := newID(t)
	_, err = pool.Exec(ctx, `
		INSERT INTO organizations (
			id, slug, display_name, default_locale, default_timezone, default_currency
		) VALUES ($1, 'atomic-venue', 'Atomic Venue', 'en', 'UTC', 'USD')
	`, organizationID.UUID())
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Microsecond)
	auditID := newID(t)
	eventID := newID(t)
	claimID := newID(t)
	fingerprint := sha256.Sum256([]byte(`{"slug":"atomic-venue"}`))

	err = runner.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		txStore := store.WithTx(tx)
		if _, execErr := tx.Exec(ctx, `
			UPDATE organizations SET settings_version = 2, updated_at = $2 WHERE id = $1
		`, organizationID.UUID(), now); execErr != nil {
			return execErr
		}
		inserted, insertErr := txStore.InsertIdempotencyClaim(ctx, platformpostgres.IdempotencyClaim{
			ID: claimID, OrganizationID: organizationID, PrincipalType: "user",
			PrincipalID: "user-1", Operation: "organization.update",
			Key: "0123456789abcdef", RequestFingerprint: fingerprint,
			LockedUntil: now.Add(time.Minute), ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now,
		})
		if insertErr != nil {
			return insertErr
		}
		if !inserted {
			return platformpostgres.ErrIdempotencyNotProcessing
		}
		if insertErr := txStore.InsertAuditEntry(ctx, platformpostgres.AuditEntry{
			ID: auditID, OrganizationID: organizationID, ActorType: "user", ActorID: "user-1",
			Action: "organization.updated", SubjectType: "organization",
			SubjectID: organizationID.String(), Result: "success", RequestID: "request-1",
			AfterData: json.RawMessage(`{"settings_version":2}`), OccurredAt: now,
		}); insertErr != nil {
			return insertErr
		}
		if insertErr := txStore.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{
			ID: eventID, OrganizationID: organizationID, AggregateType: "organization",
			AggregateID: organizationID, AggregateVersion: 2, EventType: "organization.updated",
			SchemaVersion: 1, Payload: json.RawMessage(`{"settings_version":2}`),
			CorrelationID: "request-1", AvailableAt: now, OccurredAt: now,
		}); insertErr != nil {
			return insertErr
		}
		return txStore.CompleteIdempotencyRecord(ctx, platformpostgres.IdempotencyCompletion{
			ID: claimID, OrganizationID: organizationID, State: "completed", ResponseStatus: 200,
			ResponseBody: json.RawMessage(`{"settings_version":2}`),
			ResourceType: "organization", ResourceID: organizationID.String(), UpdatedAt: now,
		})
	})
	require.NoError(t, err)
	requireTableCount(t, ctx, pool, "audit_entries", 1)
	requireTableCount(t, ctx, pool, "outbox_events", 1)
	requireTableCount(t, ctx, pool, "idempotency_records", 1)
	requireOrganizationVersion(t, ctx, pool, organizationID, 2)
	auditPage, err := store.ListAuditEntries(ctx, audit.ListInput{OrganizationID: organizationID, RequestID: "request-1", Limit: 10})
	require.NoError(t, err)
	require.Len(t, auditPage.Items, 1)
	require.Equal(t, "organization.updated", auditPage.Items[0].Action)
	require.Equal(t, organizationID, auditPage.Items[0].OrganizationID)

	err = runner.Run(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		txStore := store.WithTx(tx)
		inserted, insertErr := txStore.InsertIdempotencyClaim(ctx, platformpostgres.IdempotencyClaim{
			ID: newID(t), OrganizationID: organizationID, PrincipalType: "user",
			PrincipalID: "user-1", Operation: "organization.update",
			Key: "0123456789abcdef", RequestFingerprint: fingerprint,
			LockedUntil: now.Add(time.Minute), ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now,
		})
		if insertErr != nil {
			return insertErr
		}
		require.False(t, inserted)
		record, getErr := txStore.GetIdempotencyRecordForUpdate(
			ctx, organizationID, "user", "user-1", "organization.update", "0123456789abcdef",
		)
		if getErr != nil {
			return getErr
		}
		require.Equal(t, fingerprint, record.RequestFingerprint)
		require.Equal(t, "completed", record.State)
		return nil
	})
	require.NoError(t, err)

	err = runner.Run(ctx, pgx.TxOptions{}, func(ctx context.Context, tx pgx.Tx) error {
		txStore := store.WithTx(tx)
		if _, execErr := tx.Exec(ctx, `
			UPDATE organizations SET settings_version = 3, updated_at = $2 WHERE id = $1
		`, organizationID.UUID(), now.Add(time.Second)); execErr != nil {
			return execErr
		}
		if insertErr := txStore.InsertAuditEntry(ctx, platformpostgres.AuditEntry{
			ID: newID(t), OrganizationID: organizationID, ActorType: "user", ActorID: "user-1",
			Action: "organization.updated", SubjectType: "organization",
			SubjectID: organizationID.String(), Result: "success", OccurredAt: now.Add(time.Second),
		}); insertErr != nil {
			return insertErr
		}
		return txStore.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{
			ID: newID(t), OrganizationID: organizationID, AggregateType: "organization",
			AggregateID: organizationID, AggregateVersion: 2, EventType: "organization.updated",
			SchemaVersion: 1, Payload: json.RawMessage(`{"settings_version":3}`),
			AvailableAt: now.Add(time.Second), OccurredAt: now.Add(time.Second),
		})
	})
	require.Error(t, err)
	requireOrganizationVersion(t, ctx, pool, organizationID, 2)
	requireTableCount(t, ctx, pool, "audit_entries", 1)

	jobID := newID(t)
	job := jobqueue.Enqueue{
		ID: jobID, OrganizationID: &organizationID, Type: "ticket.issue", SchemaVersion: 1,
		DedupeKey: "order-1", Payload: json.RawMessage(`{"order_id":"order-1"}`),
		RunAt: now, MaxAttempts: 3, CreatedAt: now,
	}
	inserted, err := store.EnqueueJob(ctx, job)
	require.NoError(t, err)
	require.True(t, inserted)
	job.ID = newID(t)
	inserted, err = store.EnqueueJob(ctx, job)
	require.NoError(t, err)
	require.False(t, inserted, "duplicate organization/type/dedupe key must be ignored")

	claimed, err := store.Claim(ctx, "worker-a", 10, time.Minute)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, int32(1), claimed[0].AttemptCount)
	claimedByOther, err := store.Claim(ctx, "worker-b", 10, time.Minute)
	require.NoError(t, err)
	require.Empty(t, claimedByOther)
	require.ErrorIs(t, store.Succeed(ctx, jobID, "worker-b"), jobqueue.ErrLeaseLost)
	require.NoError(t, store.Retry(ctx, jobID, "worker-a", 0, jobqueue.Failure{Class: "transient", Message: "Temporary failure."}))

	claimed, err = store.Claim(ctx, "worker-b", 10, time.Minute)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, int32(2), claimed[0].AttemptCount)
	require.NoError(t, store.Succeed(ctx, jobID, "worker-b"))
	var jobState string
	require.NoError(t, pool.QueryRow(ctx, `SELECT state FROM jobs WHERE id = $1`, jobID.UUID()).Scan(&jobState))
	require.Equal(t, "succeeded", jobState)

	exhaustedID := newID(t)
	inserted, err = store.EnqueueJob(ctx, jobqueue.Enqueue{
		ID: exhaustedID, Type: "platform.cleanup", SchemaVersion: 1, DedupeKey: "cleanup-1",
		Payload: json.RawMessage(`{}`), RunAt: now, MaxAttempts: 1, CreatedAt: now,
	})
	require.NoError(t, err)
	require.True(t, inserted)
	claimed, err = store.Claim(ctx, "worker-a", 10, time.Minute)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	_, err = pool.Exec(ctx, `UPDATE jobs SET lease_expires_at = now() - interval '1 second' WHERE id = $1`, exhaustedID.UUID())
	require.NoError(t, err)
	dead, err := store.DeadLetterExhaustedLeases(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), dead)
	require.NoError(t, pool.QueryRow(ctx, `SELECT state FROM jobs WHERE id = $1`, exhaustedID.UUID()).Scan(&jobState))
	require.Equal(t, "dead_letter", jobState)

	inboxHash := sha256.Sum256([]byte("provider-event-payload"))
	inboxID := newID(t)
	inserted, err = store.InsertInboxMessage(ctx, inbox.Message{
		ID: inboxID, OrganizationID: &organizationID, Source: "stripe", MessageID: "evt-integration-1",
		PayloadReference: "payloads/evt-integration-1", PayloadSHA256: inboxHash,
		AvailableAt: now, ReceivedAt: now, UpdatedAt: now,
	})
	require.NoError(t, err)
	require.True(t, inserted)
	inserted, err = store.InsertInboxMessage(ctx, inbox.Message{
		ID: newID(t), OrganizationID: &organizationID, Source: "stripe", MessageID: "evt-integration-1",
		PayloadReference: "payloads/evt-integration-1", PayloadSHA256: inboxHash,
		AvailableAt: now, ReceivedAt: now, UpdatedAt: now,
	})
	require.NoError(t, err)
	require.False(t, inserted, "duplicate provider event with the same hash must be acknowledged")
	conflictingHash := sha256.Sum256([]byte("different-provider-event-payload"))
	_, err = store.InsertInboxMessage(ctx, inbox.Message{
		ID: newID(t), OrganizationID: &organizationID, Source: "stripe", MessageID: "evt-integration-1",
		PayloadReference: "payloads/evt-integration-1", PayloadSHA256: conflictingHash,
		AvailableAt: now, ReceivedAt: now, UpdatedAt: now,
	})
	require.ErrorIs(t, err, inbox.ErrPayloadConflict)

	claimedMessages, err := store.ClaimInboxMessages(ctx, "inbox-worker-a", 10, 3, time.Minute, now)
	require.NoError(t, err)
	require.Len(t, claimedMessages, 1)
	require.Equal(t, int32(1), claimedMessages[0].AttemptCount)
	claimedInboxByOther, err := store.ClaimInboxMessages(ctx, "inbox-worker-b", 10, 3, time.Minute, now)
	require.NoError(t, err)
	require.Empty(t, claimedInboxByOther)
	require.ErrorIs(t, store.MarkInboxMessageProcessed(ctx, inboxID, "inbox-worker-b", now), inbox.ErrLeaseLost)
	require.NoError(t, store.RetryInboxMessage(ctx, inboxID, "inbox-worker-a", now, inbox.FailureTransient, "Temporary provider failure."))
	claimedMessages, err = store.ClaimInboxMessages(ctx, "inbox-worker-b", 10, 3, time.Minute, now)
	require.NoError(t, err)
	require.Len(t, claimedMessages, 1)
	require.Equal(t, int32(2), claimedMessages[0].AttemptCount)
	require.NoError(t, store.MarkInboxMessageProcessed(ctx, inboxID, "inbox-worker-b", now))
	var inboxState string
	require.NoError(t, pool.QueryRow(ctx, `SELECT state FROM inbox_messages WHERE id = $1`, inboxID.UUID()).Scan(&inboxState))
	require.Equal(t, inbox.StateProcessed, inboxState)

	exhaustedInboxID := newID(t)
	exhaustedHash := sha256.Sum256([]byte("exhausted-provider-event"))
	inserted, err = store.InsertInboxMessage(ctx, inbox.Message{
		ID: exhaustedInboxID, Source: "stripe", MessageID: "evt-integration-2", PayloadSHA256: exhaustedHash,
		AvailableAt: now, ReceivedAt: now, UpdatedAt: now,
	})
	require.NoError(t, err)
	require.True(t, inserted)
	claimedMessages, err = store.ClaimInboxMessages(ctx, "inbox-worker-a", 10, 1, time.Minute, now)
	require.NoError(t, err)
	require.Len(t, claimedMessages, 1)
	_, err = pool.Exec(ctx, `UPDATE inbox_messages SET lease_expires_at = now() - interval '1 second' WHERE id = $1`, exhaustedInboxID.UUID())
	require.NoError(t, err)
	dead, err = store.DeadLetterExhaustedInboxLeases(ctx, 1, now)
	require.NoError(t, err)
	require.Equal(t, int64(1), dead)
	require.NoError(t, pool.QueryRow(ctx, `SELECT state FROM inbox_messages WHERE id = $1`, exhaustedInboxID.UUID()).Scan(&inboxState))
	require.Equal(t, inbox.StateDeadLetter, inboxState)

	organizationRepository := organizationpostgres.New(pool, runner)
	currency, err := money.NewCurrency("USD")
	require.NoError(t, err)
	createRecord := organization.CreateRecord{
		Organization: organization.Organization{
			ID: newID(t), Slug: "onboarded-venue", DisplayName: "Onboarded Venue", Status: "active",
			DefaultLocale: "en-US", DefaultTimezone: "UTC", DefaultCurrency: currency,
			SettingsVersion: 1, CreatedAt: now, UpdatedAt: now,
		},
		IdempotencyID: newID(t), AuditID: newID(t), OutboxID: newID(t),
		PrincipalType: "platform", PrincipalID: "onboarding-service",
		IdempotencyKey: "organization-key-1", Fingerprint: sha256.Sum256([]byte("request-one")),
		IdempotencyExpiry: now.Add(72 * time.Hour),
	}
	createdOrganization, replayed, err := organizationRepository.Create(ctx, createRecord)
	require.NoError(t, err)
	require.False(t, replayed)
	require.Equal(t, createRecord.Organization.ID, createdOrganization.ID)

	replayRecord := createRecord
	replayRecord.Organization.ID = newID(t)
	replayRecord.IdempotencyID, replayRecord.AuditID, replayRecord.OutboxID = newID(t), newID(t), newID(t)
	replayedOrganization, replayed, err := organizationRepository.Create(ctx, replayRecord)
	require.NoError(t, err)
	require.True(t, replayed)
	require.Equal(t, createRecord.Organization.ID, replayedOrganization.ID)

	reusedRecord := replayRecord
	reusedRecord.Fingerprint = sha256.Sum256([]byte("different-request"))
	_, _, err = organizationRepository.Create(ctx, reusedRecord)
	require.ErrorIs(t, err, organization.ErrKeyReused)

	conflictingRecord := createRecord
	conflictingRecord.Organization.ID = newID(t)
	conflictingRecord.IdempotencyID, conflictingRecord.AuditID, conflictingRecord.OutboxID = newID(t), newID(t), newID(t)
	conflictingRecord.IdempotencyKey = "organization-key-2"
	conflictingRecord.Fingerprint = sha256.Sum256([]byte("request-two"))
	_, _, err = organizationRepository.Create(ctx, conflictingRecord)
	require.ErrorIs(t, err, organization.ErrSlugConflict)

	var platformIdempotency, createdAudits, createdEvents int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM idempotency_records WHERE organization_id IS NULL`).Scan(&platformIdempotency))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM audit_entries WHERE action = 'organization.created'`).Scan(&createdAudits))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE event_type = 'organization.created'`).Scan(&createdEvents))
	require.Equal(t, 1, platformIdempotency)
	require.Equal(t, 1, createdAudits)
	require.Equal(t, 1, createdEvents)
}

func newID(t *testing.T) identifier.ID {
	t.Helper()
	id, err := identifier.New()
	require.NoError(t, err)
	return id
}

func requireTableCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, expected int) {
	t.Helper()
	var count int
	require.NoError(t, pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count)) // #nosec G202 -- test-only allowlist at call sites
	require.Equal(t, expected, count)
}

func requireOrganizationVersion(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	organizationID identifier.ID,
	expected int64,
) {
	t.Helper()
	var version int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT settings_version FROM organizations WHERE id = $1`, organizationID.UUID()).Scan(&version))
	require.Equal(t, expected, version)
}
