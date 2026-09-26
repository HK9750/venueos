//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/publication"
	publicationpostgres "github.com/HK9750/venueos/internal/publication/postgres"
	"github.com/HK9750/venueos/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestPublicationRepositoryLoadsTenantCatalogSnapshot(t *testing.T) {
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
	repository := publicationpostgres.New(pool, runner)
	organizationID, venueID, spaceID, eventID, sessionID := newPublicationID(t), newPublicationID(t), newPublicationID(t), newPublicationID(t), newPublicationID(t)
	_, err = pool.Exec(ctx, `INSERT INTO organizations (id, slug, display_name, default_locale, default_timezone, default_currency) VALUES ($1, 'publication-catalog', 'Publication Catalog', 'en', 'UTC', 'USD')`, organizationID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO venues (id, organization_id, slug, display_name, status, timezone) VALUES ($1, $2, 'main', 'Main', 'inactive', 'UTC')`, venueID.UUID(), organizationID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO spaces (id, organization_id, venue_id, slug, display_name, status, inventory_mode, physical_capacity) VALUES ($1, $2, $3, 'floor', 'Floor', 'active', 'general_admission', 100)`, spaceID.UUID(), organizationID.UUID(), venueID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO events (id, organization_id, slug, status, current_revision) VALUES ($1, $2, 'show', 'draft', 0)`, eventID.UUID(), organizationID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO sessions (id, organization_id, event_id, event_revision, venue_id, space_id, inventory_mode, starts_at, ends_at, timezone) VALUES ($1, $2, $3, 1, $4, $5, 'general_admission', now() + interval '1 day', now() + interval '1 day 2 hours', 'UTC')`, sessionID.UUID(), organizationID.UUID(), eventID.UUID(), venueID.UUID(), spaceID.UUID())
	require.NoError(t, err)
	snapshot, err := repository.Load(ctx, organizationID, eventID)
	require.NoError(t, err)
	require.Equal(t, "draft", snapshot.Status)
	require.Len(t, snapshot.Sessions, 1)
	require.Equal(t, "inactive", snapshot.Sessions[0].VenueStatus)
	require.Equal(t, int64(0), snapshot.Sessions[0].PriceTierCount)
	_, err = repository.Load(ctx, organizationID, newPublicationID(t))
	require.ErrorIs(t, err, publication.ErrNotFound)
}

func newPublicationID(t *testing.T) identifier.ID {
	t.Helper()
	id, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
