//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/event"
	eventpostgres "github.com/HK9750/venueos/internal/event/postgres"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestEventRepositoryRevisionLifecycle(t *testing.T) {
	ctx := context.Background()
	container, err := postgrescontainer.Run(ctx, "postgres:18-alpine", postgrescontainer.WithDatabase("app"), postgrescontainer.WithUsername("app"), postgrescontainer.WithPassword("app"), postgrescontainer.BasicWaitStrategies())
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
	repository := eventpostgres.New(pool, runner)
	organizationID := newEventID(t)
	_, err = pool.Exec(ctx, `INSERT INTO organizations (id, slug, display_name, default_locale, default_timezone, default_currency) VALUES ($1, 'event-catalog', 'Event Catalog', 'en', 'UTC', 'USD')`, organizationID.UUID())
	require.NoError(t, err)
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	eventID := newEventID(t)
	created, err := repository.Create(ctx, event.CreateRecord{Event: event.Event{ID: eventID, OrganizationID: organizationID, Slug: "summer-show", Title: "Summer Show", ShortDescription: "Short", LongDescription: "Long", Status: event.StatusDraft, Version: 1, CreatedAt: now, UpdatedAt: now}, Checksum: make([]byte, 32), AuditID: newEventID(t), OutboxID: newEventID(t), ActorType: "user", ActorID: "owner"})
	require.NoError(t, err)
	require.Equal(t, int64(0), created.CurrentRevision)
	duplicateID := newEventID(t)
	_, err = repository.Create(ctx, event.CreateRecord{Event: event.Event{ID: duplicateID, OrganizationID: organizationID, Slug: "summer-show", Title: "Duplicate", Status: event.StatusDraft, Version: 1, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}, Checksum: make([]byte, 32), AuditID: newEventID(t), OutboxID: newEventID(t), ActorType: "user", ActorID: "owner"})
	require.ErrorIs(t, err, event.ErrSlugConflict)
	updated, err := repository.Update(ctx, event.UpdateRecord{Input: event.UpdateInput{OrganizationID: organizationID, EventID: eventID, Slug: "summer-show", Title: "Updated Show", ShortDescription: "Updated", LongDescription: "Long", Version: 1}, Checksum: make([]byte, 32), AuditID: newEventID(t), OutboxID: newEventID(t), ActorType: "user", ActorID: "owner", OccurredAt: now.Add(time.Minute)})
	require.NoError(t, err)
	require.Equal(t, int64(2), updated.Version)
	published, err := repository.Publish(ctx, event.PublishRecord{Input: event.PublishInput{OrganizationID: organizationID, EventID: eventID, Version: 2}, AuditID: newEventID(t), OutboxID: newEventID(t), ActorType: "user", ActorID: "owner", OccurredAt: now.Add(2 * time.Minute)})
	require.NoError(t, err)
	require.Equal(t, int64(1), published.CurrentRevision)
	require.Equal(t, event.StatusPublished, published.Status)
	cloned, err := repository.Clone(ctx, event.CloneRecord{OrganizationID: organizationID, EventID: eventID, CloneID: newEventID(t), AuditID: newEventID(t), OutboxID: newEventID(t), ActorType: "user", ActorID: "owner", OccurredAt: now.Add(3 * time.Minute)})
	require.NoError(t, err)
	require.Equal(t, int64(4), cloned.Version)
	updatedAgain, err := repository.Update(ctx, event.UpdateRecord{Input: event.UpdateInput{OrganizationID: organizationID, EventID: eventID, Slug: "summer-show", Title: "Second Revision", ShortDescription: "Second", LongDescription: "Long", Version: 4}, Checksum: make([]byte, 32), AuditID: newEventID(t), OutboxID: newEventID(t), ActorType: "user", ActorID: "owner", OccurredAt: now.Add(4 * time.Minute)})
	require.NoError(t, err)
	require.Equal(t, int64(5), updatedAgain.Version)
	secondPublished, err := repository.Publish(ctx, event.PublishRecord{Input: event.PublishInput{OrganizationID: organizationID, EventID: eventID, Version: 5}, AuditID: newEventID(t), OutboxID: newEventID(t), ActorType: "user", ActorID: "owner", OccurredAt: now.Add(5 * time.Minute)})
	require.NoError(t, err)
	require.Equal(t, int64(2), secondPublished.CurrentRevision)
	found, err := repository.Get(ctx, organizationID, eventID)
	require.NoError(t, err)
	require.Equal(t, "Second Revision", found.Title)
	var auditCount, eventCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM audit_entries`).Scan(&auditCount))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&eventCount))
	require.Equal(t, 6, auditCount)
	require.Equal(t, 6, eventCount)
}

func newEventID(t *testing.T) identifier.ID {
	t.Helper()
	id, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
