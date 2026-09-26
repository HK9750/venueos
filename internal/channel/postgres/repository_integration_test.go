//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/channel"
	channelpostgres "github.com/HK9750/venueos/internal/channel/postgres"
	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestSalesChannelRepositoryPersistsConfigurationAndMutationRecords(t *testing.T) {
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
	repository := channelpostgres.New(pool, runner)
	organizationID := newChannelID(t)
	_, err = pool.Exec(ctx, `INSERT INTO organizations (id, slug, display_name, default_locale, default_timezone, default_currency) VALUES ($1, 'channel-catalog', 'Channel Catalog', 'en', 'UTC', 'USD')`, organizationID.UUID())
	require.NoError(t, err)
	venueID, spaceID, eventID, sessionID := newChannelID(t), newChannelID(t), newChannelID(t), newChannelID(t)
	_, err = pool.Exec(ctx, `INSERT INTO venues (id, organization_id, slug, display_name, status, timezone) VALUES ($1, $2, 'main', 'Main', 'active', 'UTC')`, venueID.UUID(), organizationID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO spaces (id, organization_id, venue_id, slug, display_name, status, inventory_mode, physical_capacity) VALUES ($1, $2, $3, 'floor', 'Floor', 'active', 'general_admission', 100)`, spaceID.UUID(), organizationID.UUID(), venueID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO events (id, organization_id, slug, status, current_revision) VALUES ($1, $2, 'show', 'published', 1)`, eventID.UUID(), organizationID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO event_revisions (id, organization_id, event_id, revision, status, title, checksum, published_at) VALUES ($1, $2, $3, 1, 'published', 'Show', decode(repeat('aa', 32), 'hex'), now())`, newChannelID(t).UUID(), organizationID.UUID(), eventID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO sessions (id, organization_id, event_id, event_revision, venue_id, space_id, inventory_mode, starts_at, ends_at, timezone) VALUES ($1, $2, $3, 1, $4, $5, 'general_admission', now() + interval '1 day', now() + interval '1 day 2 hours', 'UTC')`, sessionID.UUID(), organizationID.UUID(), eventID.UUID(), venueID.UUID(), spaceID.UUID())
	require.NoError(t, err)
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	channelID := newChannelID(t)
	created, err := repository.Create(ctx, channel.CreateRecord{Channel: channel.SalesChannel{ID: channelID, OrganizationID: organizationID, Key: "public", DisplayName: "Public", Type: channel.TypePublic, Configuration: map[string]any{"audience": "customer"}, Status: channel.StatusActive, Version: 1, CreatedAt: now, UpdatedAt: now}, AuditID: newChannelID(t), OutboxID: newChannelID(t), ActorType: "user", ActorID: "owner"})
	require.NoError(t, err)
	require.Equal(t, "customer", created.Configuration["audience"])
	_, err = repository.Create(ctx, channel.CreateRecord{Channel: channel.SalesChannel{ID: newChannelID(t), OrganizationID: organizationID, Key: "public", DisplayName: "Duplicate", Type: channel.TypePublic, Configuration: map[string]any{}, Status: channel.StatusActive, Version: 1, CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute)}, AuditID: newChannelID(t), OutboxID: newChannelID(t), ActorType: "user", ActorID: "owner"})
	require.ErrorIs(t, err, channel.ErrKeyConflict)
	allocation, err := repository.CreateAllocation(ctx, channel.CreateAllocationRecord{Allocation: channel.Allocation{ID: newChannelID(t), OrganizationID: organizationID, SessionID: sessionID, ChannelID: channelID, ScopeKey: "all", Mode: channel.AllocationHardReserved, Quantity: 60, Version: 1, CreatedAt: now, UpdatedAt: now}, EventID: eventID, AuditID: newChannelID(t), OutboxID: newChannelID(t), ActorType: "user", ActorID: "owner"})
	require.NoError(t, err)
	require.Equal(t, int64(60), allocation.Quantity)
	_, err = repository.CreateAllocation(ctx, channel.CreateAllocationRecord{Allocation: channel.Allocation{ID: newChannelID(t), OrganizationID: organizationID, SessionID: sessionID, ChannelID: channelID, ScopeKey: "all", Mode: channel.AllocationHardReserved, Quantity: 10, Version: 1, CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute)}, EventID: eventID, AuditID: newChannelID(t), OutboxID: newChannelID(t), ActorType: "user", ActorID: "owner"})
	require.ErrorIs(t, err, channel.ErrAllocationExists)
	_, err = repository.CreateAllocation(ctx, channel.CreateAllocationRecord{Allocation: channel.Allocation{ID: newChannelID(t), OrganizationID: organizationID, SessionID: sessionID, ChannelID: channelID, ScopeKey: "box-office", Mode: channel.AllocationHardReserved, Quantity: 50, Version: 1, CreatedAt: now.Add(2 * time.Minute), UpdatedAt: now.Add(2 * time.Minute)}, EventID: eventID, AuditID: newChannelID(t), OutboxID: newChannelID(t), ActorType: "user", ActorID: "owner"})
	require.ErrorIs(t, err, channel.ErrAllocationConflict)
	allocationPage, err := repository.ListAllocations(ctx, organizationID, sessionID, 10, nil)
	require.NoError(t, err)
	require.Len(t, allocationPage.Items, 1)
	page, err := repository.List(ctx, organizationID, 10, nil)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, channel.TypePublic, page.Items[0].Type)
	var auditCount, outboxCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM audit_entries`).Scan(&auditCount))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&outboxCount))
	require.Equal(t, 2, auditCount)
	require.Equal(t, 2, outboxCount)
}

func newChannelID(t *testing.T) identifier.ID {
	t.Helper()
	id, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
