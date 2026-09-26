//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/venue"
	venuepostgres "github.com/HK9750/venueos/internal/venue/postgres"
	"github.com/HK9750/venueos/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestVenueRepositoryScopesAndVersions(t *testing.T) {
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
	repository := venuepostgres.New(pool, runner)
	organizationID, err := identifier.New()
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO organizations (id, slug, display_name, default_locale, default_timezone, default_currency) VALUES ($1, 'catalog-venue', 'Catalog Venue', 'en', 'UTC', 'USD')`, organizationID.UUID())
	require.NoError(t, err)
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	venueID, err := identifier.New()
	require.NoError(t, err)
	created, err := repository.CreateVenue(ctx, venue.CreateVenueRecord{Venue: venue.Venue{ID: venueID, OrganizationID: organizationID, Slug: "main-hall", DisplayName: "Main Hall", Status: venue.StatusDraft, Timezone: "UTC", Version: 1, CreatedAt: now, UpdatedAt: now}, AuditID: newID(t), OutboxID: newID(t), ActorType: "user", ActorID: "owner"})
	require.NoError(t, err)
	require.Equal(t, venueID, created.ID)

	duplicateID, err := identifier.New()
	require.NoError(t, err)
	_, err = repository.CreateVenue(ctx, venue.CreateVenueRecord{Venue: venue.Venue{ID: duplicateID, OrganizationID: organizationID, Slug: "main-hall", DisplayName: "Duplicate", Status: venue.StatusDraft, Timezone: "UTC", Version: 1, CreatedAt: now.Add(time.Second), UpdatedAt: now.Add(time.Second)}, AuditID: newID(t), OutboxID: newID(t), ActorType: "user", ActorID: "owner"})
	require.ErrorIs(t, err, venue.ErrSlugConflict)

	updated, err := repository.UpdateVenue(ctx, venue.UpdateVenueRecord{Venue: venue.UpdateVenueInput{OrganizationID: organizationID, VenueID: venueID, Slug: "main-stage", DisplayName: "Main Stage", Timezone: "UTC", Version: 1}, AuditID: newID(t), OutboxID: newID(t), ActorType: "user", ActorID: "owner", OccurredAt: now.Add(time.Minute)})
	require.NoError(t, err)
	require.Equal(t, int64(2), updated.Version)
	_, err = repository.UpdateVenue(ctx, venue.UpdateVenueRecord{Venue: venue.UpdateVenueInput{OrganizationID: organizationID, VenueID: venueID, Slug: "stale", DisplayName: "Stale", Timezone: "UTC", Version: 1}, AuditID: newID(t), OutboxID: newID(t), ActorType: "user", ActorID: "owner", OccurredAt: now.Add(2 * time.Minute)})
	require.ErrorIs(t, err, venue.ErrVersionConflict)

	spaceID, err := identifier.New()
	require.NoError(t, err)
	space, err := repository.CreateSpace(ctx, venue.CreateSpaceRecord{Space: venue.Space{ID: spaceID, OrganizationID: organizationID, VenueID: venueID, Slug: "floor", DisplayName: "Floor", Status: venue.StatusDraft, InventoryMode: venue.InventoryGA, PhysicalCapacity: 500, Version: 1, CreatedAt: now.Add(3 * time.Minute), UpdatedAt: now.Add(3 * time.Minute)}, AuditID: newID(t), OutboxID: newID(t), ActorType: "user", ActorID: "owner"})
	require.NoError(t, err)
	require.Equal(t, spaceID, space.ID)
	otherVenue, err := identifier.New()
	require.NoError(t, err)
	_, err = repository.CreateSpace(ctx, venue.CreateSpaceRecord{Space: venue.Space{ID: otherVenue, OrganizationID: organizationID, VenueID: otherVenue, Slug: "wrong", DisplayName: "Wrong", Status: venue.StatusDraft, InventoryMode: venue.InventoryGA, PhysicalCapacity: 1, Version: 1, CreatedAt: now, UpdatedAt: now}, AuditID: newID(t), OutboxID: newID(t), ActorType: "user", ActorID: "owner"})
	require.ErrorIs(t, err, venue.ErrNotFound)
	gateID, err := identifier.New()
	require.NoError(t, err)
	gate, err := repository.CreateGate(ctx, venue.CreateGateRecord{Gate: venue.Gate{ID: gateID, OrganizationID: organizationID, VenueID: venueID, SpaceID: &spaceID, Code: "north", DisplayName: "North Gate", Status: venue.StatusDraft, Version: 1, CreatedAt: now.Add(4 * time.Minute), UpdatedAt: now.Add(4 * time.Minute)}, AuditID: newID(t), OutboxID: newID(t), ActorType: "user", ActorID: "owner"})
	require.NoError(t, err)
	require.Equal(t, gateID, gate.ID)
	poolID, err := identifier.New()
	require.NoError(t, err)
	gaPool, err := repository.CreateGAPool(ctx, venue.CreateGAPoolRecord{Pool: venue.GAPoolTemplate{ID: poolID, OrganizationID: organizationID, SpaceID: spaceID, Slug: "floor", DisplayName: "Floor Pool", Status: venue.StatusDraft, PhysicalCapacity: 500, SellableCapacity: 450, Version: 1, CreatedAt: now.Add(5 * time.Minute), UpdatedAt: now.Add(5 * time.Minute)}, AuditID: newID(t), OutboxID: newID(t), ActorType: "user", ActorID: "owner"})
	require.NoError(t, err)
	require.Equal(t, poolID, gaPool.ID)
	assignedSpaceID, err := identifier.New()
	require.NoError(t, err)
	_, err = repository.CreateSpace(ctx, venue.CreateSpaceRecord{Space: venue.Space{ID: assignedSpaceID, OrganizationID: organizationID, VenueID: venueID, Slug: "theatre", DisplayName: "Theatre", Status: venue.StatusDraft, InventoryMode: venue.InventoryAssigned, PhysicalCapacity: 2, Version: 1, CreatedAt: now.Add(6 * time.Minute), UpdatedAt: now.Add(6 * time.Minute)}, AuditID: newID(t), OutboxID: newID(t), ActorType: "user", ActorID: "owner"})
	require.NoError(t, err)
	seatMapID, err := identifier.New()
	require.NoError(t, err)
	mapContent := venue.SeatMapContent{Sections: []venue.SeatMapSection{{ID: "main", Label: "Main", Rows: []venue.SeatMapRow{{ID: "a", Label: "A", Seats: []venue.SeatMapSeat{{ID: "a1", Label: "1", Sellable: true}, {ID: "a2", Label: "2", Sellable: false}}}}}}}
	seatMap, err := repository.CreateSeatMap(ctx, venue.CreateSeatMapRecord{SeatMap: venue.SeatMap{ID: seatMapID, OrganizationID: organizationID, SpaceID: assignedSpaceID, Name: "Main Map", Status: venue.SeatMapDraft, Version: 1, SeatCount: 2, SellableCount: 1, Content: mapContent, CreatedAt: now.Add(7 * time.Minute), UpdatedAt: now.Add(7 * time.Minute)}, Checksum: make([]byte, 32), Content: []byte(`{"sections":[{"id":"main","label":"Main","rows":[{"id":"a","label":"A","seats":[{"id":"a1","label":"1","sellable":true},{"id":"a2","label":"2","sellable":false}]}]}]}`), AuditID: newID(t), OutboxID: newID(t), ActorType: "user", ActorID: "owner"})
	require.NoError(t, err)
	require.Equal(t, int64(0), seatMap.Revision)
	updatedMap, err := repository.UpdateSeatMap(ctx, venue.UpdateSeatMapRecord{Input: venue.UpdateSeatMapInput{OrganizationID: organizationID, SpaceID: assignedSpaceID, SeatMapID: seatMapID, Name: "Main Map Updated", Version: 1}, Checksum: make([]byte, 32), Content: []byte(`{"sections":[{"id":"main","label":"Main","rows":[{"id":"a","label":"A","seats":[{"id":"a1","label":"1","sellable":true},{"id":"a2","label":"2","sellable":true}]}]}]}`), SeatCount: 2, Sellable: 2, AuditID: newID(t), OutboxID: newID(t), ActorType: "user", ActorID: "owner", OccurredAt: now.Add(8 * time.Minute)})
	require.NoError(t, err)
	require.Equal(t, int64(2), updatedMap.Version)
	publishedMap, err := repository.PublishSeatMap(ctx, venue.PublishSeatMapRecord{Input: venue.PublishSeatMapInput{OrganizationID: organizationID, SpaceID: assignedSpaceID, SeatMapID: seatMapID, Version: 2}, AuditID: newID(t), OutboxID: newID(t), ActorType: "user", ActorID: "owner", OccurredAt: now.Add(9 * time.Minute)})
	require.NoError(t, err)
	require.Equal(t, int64(1), publishedMap.Revision)
	cloneMapID, err := identifier.New()
	require.NoError(t, err)
	clonedMap, err := repository.CloneSeatMap(ctx, venue.CloneSeatMapRecord{OrganizationID: organizationID, SpaceID: assignedSpaceID, SeatMapID: seatMapID, CloneID: cloneMapID, Name: "Main Map Copy", AuditID: newID(t), OutboxID: newID(t), ActorType: "user", ActorID: "owner", OccurredAt: now.Add(10 * time.Minute)})
	require.NoError(t, err)
	require.Equal(t, venue.SeatMapDraft, clonedMap.Status)
	require.Equal(t, publishedMap.Checksum, clonedMap.Checksum)

	archived, err := repository.ArchiveVenue(ctx, venue.ArchiveVenueRecord{OrganizationID: organizationID, VenueID: venueID, Version: 2, AuditID: newID(t), OutboxID: newID(t), ActorType: "user", ActorID: "owner", OccurredAt: now.Add(11 * time.Minute)})
	require.NoError(t, err)
	require.Equal(t, venue.StatusArchived, archived.Status)
	restored, err := repository.RestoreVenue(ctx, venue.RestoreVenueRecord{OrganizationID: organizationID, VenueID: venueID, Version: 3, AuditID: newID(t), OutboxID: newID(t), ActorType: "user", ActorID: "owner", OccurredAt: now.Add(12 * time.Minute)})
	require.NoError(t, err)
	require.Equal(t, venue.StatusDraft, restored.Status)

	var auditCount, eventCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM audit_entries`).Scan(&auditCount))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&eventCount))
	require.Equal(t, 12, auditCount)
	require.Equal(t, 12, eventCount)
}

func newID(t *testing.T) identifier.ID {
	t.Helper()
	id, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
