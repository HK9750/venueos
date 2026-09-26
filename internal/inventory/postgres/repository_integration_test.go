//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/inventory"
	inventorypostgres "github.com/HK9750/venueos/internal/inventory/postgres"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestGAHoldRepositoryReservesAtomicallyAndAdvancesRevision(t *testing.T) {
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
	repository := inventorypostgres.New(pool, runner)
	organizationID, venueID, spaceID, eventID, sessionID, sourcePoolID, poolID := newInventoryID(t), newInventoryID(t), newInventoryID(t), newInventoryID(t), newInventoryID(t), newInventoryID(t), newInventoryID(t)
	_, err = pool.Exec(ctx, `INSERT INTO organizations (id, slug, display_name, default_locale, default_timezone, default_currency) VALUES ($1, 'inventory-catalog', 'Inventory Catalog', 'en', 'UTC', 'USD')`, organizationID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO venues (id, organization_id, slug, display_name, status, timezone) VALUES ($1, $2, 'main', 'Main', 'active', 'UTC')`, venueID.UUID(), organizationID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO spaces (id, organization_id, venue_id, slug, display_name, status, inventory_mode, physical_capacity) VALUES ($1, $2, $3, 'floor', 'Floor', 'active', 'general_admission', 100)`, spaceID.UUID(), organizationID.UUID(), venueID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO events (id, organization_id, slug, status, current_revision) VALUES ($1, $2, 'show', 'published', 1)`, eventID.UUID(), organizationID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO sessions (id, organization_id, event_id, event_revision, venue_id, space_id, inventory_mode, starts_at, ends_at, timezone) VALUES ($1, $2, $3, 1, $4, $5, 'general_admission', now() + interval '1 day', now() + interval '1 day 2 hours', 'UTC')`, sessionID.UUID(), organizationID.UUID(), eventID.UUID(), venueID.UUID(), spaceID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO ga_pool_templates (id, organization_id, space_id, slug, display_name, status, physical_capacity, sellable_capacity) VALUES ($1, $2, $3, 'floor', 'Floor', 'active', 100, 100)`, sourcePoolID.UUID(), organizationID.UUID(), spaceID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO session_ga_pools (id, organization_id, session_id, source_pool_id, slug, display_name, physical_capacity, sellable_capacity) VALUES ($1, $2, $3, $4, 'floor', 'Floor', 100, 100)`, poolID.UUID(), organizationID.UUID(), sessionID.UUID(), sourcePoolID.UUID())
	require.NoError(t, err)
	assignedSpaceID, assignedSessionID, seatMapID, seatOneID, seatTwoID := newInventoryID(t), newInventoryID(t), newInventoryID(t), newInventoryID(t), newInventoryID(t)
	_, err = pool.Exec(ctx, `INSERT INTO spaces (id, organization_id, venue_id, slug, display_name, status, inventory_mode, physical_capacity) VALUES ($1, $2, $3, 'assigned', 'Assigned', 'active', 'assigned_seating', 2)`, assignedSpaceID.UUID(), organizationID.UUID(), venueID.UUID())
	require.NoError(t, err)
	now := time.Now().UTC()
	_, err = pool.Exec(ctx, `INSERT INTO seat_map_versions (id, organization_id, space_id, name, status, revision, checksum, content, seat_count, sellable_count, published_at) VALUES ($1, $2, $3, 'Assigned Map', 'published', 1, decode(repeat('aa', 32), 'hex'), '{"sections":[]}', 2, 2, $4)`, seatMapID.UUID(), organizationID.UUID(), assignedSpaceID.UUID(), now)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO sessions (id, organization_id, event_id, event_revision, venue_id, space_id, seat_map_version_id, inventory_mode, starts_at, ends_at, timezone) VALUES ($1, $2, $3, 1, $4, $5, $6, 'assigned_seating', now() + interval '1 day', now() + interval '1 day 2 hours', 'UTC')`, assignedSessionID.UUID(), organizationID.UUID(), eventID.UUID(), venueID.UUID(), assignedSpaceID.UUID(), seatMapID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO session_seats (id, organization_id, session_id, seat_map_version_id, section_key, row_key, seat_key, label, sellable) VALUES ($1, $2, $3, $4, 'main', 'row-a', 'a-1', '1', true), ($5, $2, $3, $4, 'main', 'row-a', 'a-2', '2', true)`, seatOneID.UUID(), organizationID.UUID(), assignedSessionID.UUID(), seatMapID.UUID(), seatTwoID.UUID())
	require.NoError(t, err)
	var token [32]byte
	token[0] = 1
	holdID := newInventoryID(t)
	created, err := repository.CreateGAHold(ctx, inventory.CreateGAHoldRecord{Hold: inventory.Hold{ID: holdID, OrganizationID: organizationID, SessionID: sessionID, OwnerTokenHash: token, Currency: "USD", State: inventory.HoldActive, ExpiresAt: now.Add(10 * time.Minute), Version: 1, CreatedAt: now, UpdatedAt: now}, Item: inventory.HoldItem{ID: newInventoryID(t), OrganizationID: organizationID, HoldID: holdID, SessionID: sessionID, PoolID: poolID, Quantity: 60, CreatedAt: now}, AuditID: newInventoryID(t), OutboxID: newInventoryID(t), ActorType: "user", ActorID: "hold-owner"})
	require.NoError(t, err)
	require.Equal(t, inventory.HoldActive, created.State)
	renewed, err := repository.RenewHold(ctx, inventory.RenewHoldRecord{OrganizationID: organizationID, HoldID: holdID, OwnerTokenHash: token, ExpectedVersion: created.Version, AuditID: newInventoryID(t), OutboxID: newInventoryID(t), ActorType: "user", ActorID: "hold-owner"})
	require.NoError(t, err)
	require.Equal(t, inventory.HoldActive, renewed.State)
	require.Equal(t, int32(1), renewed.RenewalCount)
	require.Equal(t, int64(2), renewed.Version)
	_, err = repository.CreateGAHold(ctx, inventory.CreateGAHoldRecord{Hold: inventory.Hold{ID: newInventoryID(t), OrganizationID: organizationID, SessionID: sessionID, OwnerTokenHash: token, Currency: "USD", State: inventory.HoldActive, ExpiresAt: now.Add(10 * time.Minute), Version: 1, CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute)}, Item: inventory.HoldItem{ID: newInventoryID(t), OrganizationID: organizationID, HoldID: newInventoryID(t), SessionID: sessionID, PoolID: poolID, Quantity: 50, CreatedAt: now.Add(time.Minute)}, AuditID: newInventoryID(t), OutboxID: newInventoryID(t), ActorType: "guest", ActorID: "hold-owner"})
	require.ErrorIs(t, err, inventory.ErrInsufficient)
	modified, err := repository.ModifyHold(ctx, inventory.ModifyHoldRecord{
		OrganizationID:  organizationID,
		HoldID:          holdID,
		OwnerTokenHash:  token,
		ExpectedVersion: renewed.Version,
		Items:           []inventory.HoldItem{{ID: newInventoryID(t), OrganizationID: organizationID, HoldID: holdID, SessionID: sessionID, PoolID: poolID, Quantity: 50, CreatedAt: now}},
		AuditID:         newInventoryID(t),
		OutboxID:        newInventoryID(t),
		ActorType:       "user",
		ActorID:         "hold-owner",
	})
	require.NoError(t, err)
	require.Equal(t, inventory.HoldActive, modified.State)
	require.Equal(t, int64(3), modified.Version)
	var held, revision int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT held_qty FROM session_ga_pools WHERE id = $1`, poolID.UUID()).Scan(&held))
	require.Equal(t, int64(50), held)
	require.NoError(t, pool.QueryRow(ctx, `SELECT inventory_revision FROM sessions WHERE id = $1`, sessionID.UUID()).Scan(&revision))
	require.Equal(t, int64(2), revision)
	released, err := repository.ReleaseHold(ctx, inventory.ReleaseHoldRecord{OrganizationID: organizationID, HoldID: holdID, OwnerTokenHash: token, AuditID: newInventoryID(t), OutboxID: newInventoryID(t), ActorType: "user", ActorID: "hold-owner"})
	require.NoError(t, err)
	require.Equal(t, inventory.HoldReleased, released.State)
	require.NoError(t, pool.QueryRow(ctx, `SELECT held_qty FROM session_ga_pools WHERE id = $1`, poolID.UUID()).Scan(&held))
	require.NoError(t, pool.QueryRow(ctx, `SELECT inventory_revision FROM sessions WHERE id = $1`, sessionID.UUID()).Scan(&revision))
	require.Equal(t, int64(0), held)
	require.Equal(t, int64(3), revision)

	secondHoldID := newInventoryID(t)
	secondToken := [32]byte{2}
	secondCreated, err := repository.CreateGAHold(ctx, inventory.CreateGAHoldRecord{Hold: inventory.Hold{ID: secondHoldID, OrganizationID: organizationID, SessionID: sessionID, OwnerTokenHash: secondToken, Currency: "USD", State: inventory.HoldActive, ExpiresAt: now.Add(10 * time.Minute), Version: 1, CreatedAt: now, UpdatedAt: now}, Item: inventory.HoldItem{ID: newInventoryID(t), OrganizationID: organizationID, HoldID: secondHoldID, SessionID: sessionID, PoolID: poolID, Quantity: 20, CreatedAt: now}, AuditID: newInventoryID(t), OutboxID: newInventoryID(t), ActorType: "user", ActorID: "hold-owner"})
	require.NoError(t, err)
	require.Equal(t, inventory.HoldActive, secondCreated.State)
	_, err = pool.Exec(ctx, `UPDATE holds SET expires_at = $2 WHERE id = $1`, secondHoldID.UUID(), now.Add(-time.Second))
	require.NoError(t, err)
	expired, err := repository.ExpireDue(ctx, inventory.ExpireDueRecord{Now: now, Limit: 10, ActorType: "system", ActorID: "hold-expiry-worker"})
	require.NoError(t, err)
	require.Equal(t, int64(1), expired)
	var expiredState string
	require.NoError(t, pool.QueryRow(ctx, `SELECT state FROM holds WHERE id = $1`, secondHoldID.UUID()).Scan(&expiredState))
	require.Equal(t, string(inventory.HoldExpired), expiredState)
	require.NoError(t, pool.QueryRow(ctx, `SELECT held_qty FROM session_ga_pools WHERE id = $1`, poolID.UUID()).Scan(&held))
	require.NoError(t, pool.QueryRow(ctx, `SELECT inventory_revision FROM sessions WHERE id = $1`, sessionID.UUID()).Scan(&revision))
	require.Equal(t, int64(0), held)
	require.Equal(t, int64(5), revision)
	lazyHoldID := newInventoryID(t)
	lazyToken := [32]byte{4}
	_, err = repository.CreateGAHold(ctx, inventory.CreateGAHoldRecord{Hold: inventory.Hold{ID: lazyHoldID, OrganizationID: organizationID, SessionID: sessionID, OwnerTokenHash: lazyToken, Currency: "USD", State: inventory.HoldActive, ExpiresAt: now.Add(10 * time.Minute), Version: 1, CreatedAt: now, UpdatedAt: now}, Item: inventory.HoldItem{ID: newInventoryID(t), OrganizationID: organizationID, HoldID: lazyHoldID, SessionID: sessionID, PoolID: poolID, Quantity: 10, CreatedAt: now}, AuditID: newInventoryID(t), OutboxID: newInventoryID(t), ActorType: "user", ActorID: "hold-owner"})
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE holds SET expires_at = $2 WHERE id = $1`, lazyHoldID.UUID(), now.Add(-time.Second))
	require.NoError(t, err)
	lazyReleased, err := repository.ReleaseHold(ctx, inventory.ReleaseHoldRecord{OrganizationID: organizationID, HoldID: lazyHoldID, OwnerTokenHash: lazyToken, AuditID: newInventoryID(t), OutboxID: newInventoryID(t), ActorType: "user", ActorID: "hold-owner"})
	require.NoError(t, err)
	require.Equal(t, inventory.HoldExpired, lazyReleased.State)
	require.NoError(t, pool.QueryRow(ctx, `SELECT held_qty FROM session_ga_pools WHERE id = $1`, poolID.UUID()).Scan(&held))
	require.NoError(t, pool.QueryRow(ctx, `SELECT inventory_revision FROM sessions WHERE id = $1`, sessionID.UUID()).Scan(&revision))
	require.Equal(t, int64(0), held)
	require.Equal(t, int64(7), revision)
	reservedHoldID := newInventoryID(t)
	reservedToken := [32]byte{3}
	seatOne, seatTwo := seatOneID, seatTwoID
	reservedCreated, err := repository.CreateReservedSeatHold(ctx, inventory.CreateReservedSeatHoldRecord{Hold: inventory.Hold{ID: reservedHoldID, OrganizationID: organizationID, SessionID: assignedSessionID, OwnerTokenHash: reservedToken, Currency: "USD", State: inventory.HoldActive, ExpiresAt: now.Add(10 * time.Minute), Version: 1, CreatedAt: now, UpdatedAt: now}, Items: []inventory.HoldItem{{ID: newInventoryID(t), OrganizationID: organizationID, HoldID: reservedHoldID, SessionID: assignedSessionID, SeatID: &seatOne, Quantity: 1, CreatedAt: now}, {ID: newInventoryID(t), OrganizationID: organizationID, HoldID: reservedHoldID, SessionID: assignedSessionID, SeatID: &seatTwo, Quantity: 1, CreatedAt: now}}, AuditID: newInventoryID(t), OutboxID: newInventoryID(t), ActorType: "user", ActorID: "hold-owner"})
	require.NoError(t, err)
	require.Equal(t, inventory.HoldActive, reservedCreated.State)
	var heldSeatCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM session_seats WHERE session_id = $1 AND state = 'held'`, assignedSessionID.UUID()).Scan(&heldSeatCount))
	require.Equal(t, 2, heldSeatCount)
	_, err = pool.Exec(ctx, `UPDATE holds SET expires_at = $2 WHERE id = $1`, reservedHoldID.UUID(), now.Add(-time.Second))
	require.NoError(t, err)
	expired, err = repository.ExpireDue(ctx, inventory.ExpireDueRecord{Now: now, Limit: 10, ActorType: "system", ActorID: "hold-expiry-worker"})
	require.NoError(t, err)
	require.Equal(t, int64(1), expired)
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM session_seats WHERE session_id = $1 AND state = 'available'`, assignedSessionID.UUID()).Scan(&heldSeatCount))
	require.Equal(t, 2, heldSeatCount)
	require.NoError(t, pool.QueryRow(ctx, `SELECT inventory_revision FROM sessions WHERE id = $1`, assignedSessionID.UUID()).Scan(&revision))
	require.Equal(t, int64(2), revision)
	gaSnapshot, err := repository.GetAvailabilitySnapshot(ctx, organizationID, sessionID)
	require.NoError(t, err)
	require.Equal(t, int64(7), gaSnapshot.Revision)
	require.Len(t, gaSnapshot.GAPools, 1)
	require.Equal(t, int64(100), gaSnapshot.GAPools[0].Available)
	assignedSnapshot, err := repository.GetAvailabilitySnapshot(ctx, organizationID, assignedSessionID)
	require.NoError(t, err)
	require.Equal(t, int64(2), assignedSnapshot.Revision)
	require.Len(t, assignedSnapshot.Seats, 2)
	require.Equal(t, "available", assignedSnapshot.Seats[0].State)
	gaReplay, err := repository.ListAvailabilityEvents(ctx, organizationID, sessionID, 0, 3)
	require.NoError(t, err)
	require.Equal(t, int64(7), gaReplay.CurrentRevision)
	require.True(t, gaReplay.HasMore)
	require.Len(t, gaReplay.Events, 3)
	require.Equal(t, int64(1), gaReplay.Events[0].Sequence)
	gaReplayTail, err := repository.ListAvailabilityEvents(ctx, organizationID, sessionID, 3, 3)
	require.NoError(t, err)
	require.Equal(t, int64(7), gaReplayTail.CurrentRevision)
	require.True(t, gaReplayTail.HasMore)
	require.Len(t, gaReplayTail.Events, 3)
	require.Equal(t, int64(4), gaReplayTail.Events[0].Sequence)
	require.Equal(t, int64(5), gaReplayTail.Events[1].Sequence)
	require.Equal(t, int64(6), gaReplayTail.Events[2].Sequence)
	gaReplayLast, err := repository.ListAvailabilityEvents(ctx, organizationID, sessionID, 6, 3)
	require.NoError(t, err)
	require.Equal(t, int64(7), gaReplayLast.CurrentRevision)
	require.False(t, gaReplayLast.HasMore)
	require.Len(t, gaReplayLast.Events, 1)
	require.Equal(t, int64(7), gaReplayLast.Events[0].Sequence)
	assignedReplay, err := repository.ListAvailabilityEvents(ctx, organizationID, assignedSessionID, 0, 10)
	require.NoError(t, err)
	require.Equal(t, int64(2), assignedReplay.CurrentRevision)
	require.Len(t, assignedReplay.Events, 2)
	var auditCount, outboxCount, replayCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM audit_entries`).Scan(&auditCount))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&outboxCount))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM realtime_events`).Scan(&replayCount))
	require.Equal(t, 10, auditCount)
	require.Equal(t, 10, outboxCount)
	require.Equal(t, 9, replayCount)
	_, err = pool.Exec(ctx, `DELETE FROM realtime_events WHERE organization_id = $1 AND session_id = $2 AND topic = 'availability' AND sequence = 1`, organizationID.UUID(), sessionID.UUID())
	require.NoError(t, err)
	_, err = repository.ListAvailabilityEvents(ctx, organizationID, sessionID, 0, 10)
	require.ErrorIs(t, err, inventory.ErrReplayUnavailable)
}

func newInventoryID(t *testing.T) identifier.ID {
	t.Helper()
	id, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
