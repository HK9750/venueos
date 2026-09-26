//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/session"
	sessionpostgres "github.com/HK9750/venueos/internal/session/postgres"
	"github.com/HK9750/venueos/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestSessionRepositoryValidatesReferencesAndOverlap(t *testing.T) {
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
	repository := sessionpostgres.New(pool, runner)
	organizationID := newSessionID(t)
	_, err = pool.Exec(ctx, `INSERT INTO organizations (id, slug, display_name, default_locale, default_timezone, default_currency) VALUES ($1, 'session-catalog', 'Session Catalog', 'en', 'UTC', 'USD')`, organizationID.UUID())
	require.NoError(t, err)
	venueID := newSessionID(t)
	assignedSpaceID := newSessionID(t)
	gaSpaceID := newSessionID(t)
	_, err = pool.Exec(ctx, `INSERT INTO venues (id, organization_id, slug, display_name, status, timezone) VALUES ($1, $2, 'main', 'Main', 'active', 'UTC')`, venueID.UUID(), organizationID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO spaces (id, organization_id, venue_id, slug, display_name, status, inventory_mode, physical_capacity) VALUES ($1, $2, $3, 'assigned', 'Assigned', 'active', 'assigned_seating', 2), ($4, $2, $3, 'floor', 'Floor', 'active', 'general_admission', 100)`, assignedSpaceID.UUID(), organizationID.UUID(), venueID.UUID(), gaSpaceID.UUID())
	require.NoError(t, err)
	eventID := newSessionID(t)
	revisionID := newSessionID(t)
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	_, err = pool.Exec(ctx, `INSERT INTO events (id, organization_id, slug, status, current_revision) VALUES ($1, $2, 'show', 'published', 1)`, eventID.UUID(), organizationID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO event_revisions (id, organization_id, event_id, revision, status, title, checksum, published_at) VALUES ($1, $2, $3, 1, 'published', 'Show', decode(repeat('aa', 32), 'hex'), $4)`, revisionID.UUID(), organizationID.UUID(), eventID.UUID(), now)
	require.NoError(t, err)
	seatMapID := newSessionID(t)
	_, err = pool.Exec(ctx, `INSERT INTO seat_map_versions (id, organization_id, space_id, name, status, revision, checksum, content, seat_count, sellable_count, published_at) VALUES ($1, $2, $3, 'Map', 'published', 1, decode(repeat('bb', 32), 'hex'), '{"sections":[{"id":"main","label":"Main","rows":[{"id":"row-a","label":"A","seats":[{"id":"a-1","label":"1","sellable":true},{"id":"a-2","label":"2","sellable":true}]}]}]}', 2, 2, $4)`, seatMapID.UUID(), organizationID.UUID(), assignedSpaceID.UUID(), now)
	require.NoError(t, err)
	starts := now.Add(24 * time.Hour)
	ends := starts.Add(2 * time.Hour)
	created, err := repository.Create(ctx, session.CreateRecord{Session: session.Session{ID: newSessionID(t), OrganizationID: organizationID, EventID: eventID, EventRevision: 1, VenueID: venueID, SpaceID: assignedSpaceID, SeatMapVersionID: &seatMapID, InventoryMode: session.InventoryAssigned, StartsAt: starts, EndsAt: ends, Timezone: "UTC", Status: session.StatusScheduled, Version: 1, CreatedAt: now, UpdatedAt: now}, AuditID: newSessionID(t), OutboxID: newSessionID(t), ActorType: "user", ActorID: "owner"})
	require.NoError(t, err)
	require.Equal(t, session.InventoryAssigned, created.InventoryMode)
	var assignedSeatCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM session_seats WHERE organization_id = $1 AND session_id = $2`, organizationID.UUID(), created.ID.UUID()).Scan(&assignedSeatCount))
	require.Equal(t, 2, assignedSeatCount)
	_, err = repository.Create(ctx, session.CreateRecord{Session: session.Session{ID: newSessionID(t), OrganizationID: organizationID, EventID: eventID, EventRevision: 1, VenueID: venueID, SpaceID: assignedSpaceID, SeatMapVersionID: &seatMapID, InventoryMode: session.InventoryAssigned, StartsAt: starts.Add(30 * time.Minute), EndsAt: ends.Add(30 * time.Minute), Timezone: "UTC", Status: session.StatusScheduled, Version: 1, CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute)}, AuditID: newSessionID(t), OutboxID: newSessionID(t), ActorType: "user", ActorID: "owner"})
	require.ErrorIs(t, err, session.ErrOverlap)
	gaTemplateID := newSessionID(t)
	_, err = pool.Exec(ctx, `INSERT INTO ga_pool_templates (id, organization_id, space_id, slug, display_name, status, physical_capacity, sellable_capacity) VALUES ($1, $2, $3, 'floor', 'Floor', 'active', 100, 75)`, gaTemplateID.UUID(), organizationID.UUID(), gaSpaceID.UUID())
	require.NoError(t, err)
	ga, err := repository.Create(ctx, session.CreateRecord{Session: session.Session{ID: newSessionID(t), OrganizationID: organizationID, EventID: eventID, EventRevision: 1, VenueID: venueID, SpaceID: gaSpaceID, InventoryMode: session.InventoryGA, StartsAt: starts, EndsAt: ends, Timezone: "UTC", Status: session.StatusScheduled, Version: 1, CreatedAt: now.Add(2 * time.Minute), UpdatedAt: now.Add(2 * time.Minute)}, AuditID: newSessionID(t), OutboxID: newSessionID(t), ActorType: "user", ActorID: "owner"})
	require.NoError(t, err)
	require.Equal(t, session.InventoryGA, ga.InventoryMode)
	var poolCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM session_ga_pools WHERE organization_id = $1 AND session_id = $2`, organizationID.UUID(), ga.ID.UUID()).Scan(&poolCount))
	require.Equal(t, 1, poolCount)
	var sellableCapacity int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT sellable_capacity FROM session_ga_pools WHERE organization_id = $1 AND session_id = $2`, organizationID.UUID(), ga.ID.UUID()).Scan(&sellableCapacity))
	require.Equal(t, int64(75), sellableCapacity)
	var auditCount, eventCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM audit_entries`).Scan(&auditCount))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&eventCount))
	require.Equal(t, 2, auditCount)
	require.Equal(t, 2, eventCount)
}

func newSessionID(t *testing.T) identifier.ID {
	t.Helper()
	id, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
