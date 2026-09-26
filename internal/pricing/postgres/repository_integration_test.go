//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/pricing"
	pricingpostgres "github.com/HK9750/venueos/internal/pricing/postgres"
	"github.com/HK9750/venueos/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestPriceTierRepositoryEnforcesSessionCurrencyAndMutationRecords(t *testing.T) {
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
	repository := pricingpostgres.New(pool, runner)

	organizationID := newPricingID(t)
	venueID := newPricingID(t)
	spaceID := newPricingID(t)
	eventID := newPricingID(t)
	sessionID := newPricingID(t)
	_, err = pool.Exec(ctx, `INSERT INTO organizations (id, slug, display_name, default_locale, default_timezone, default_currency) VALUES ($1, 'pricing-catalog', 'Pricing Catalog', 'en', 'UTC', 'USD')`, organizationID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO venues (id, organization_id, slug, display_name, status, timezone) VALUES ($1, $2, 'main', 'Main', 'active', 'UTC')`, venueID.UUID(), organizationID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO spaces (id, organization_id, venue_id, slug, display_name, status, inventory_mode, physical_capacity) VALUES ($1, $2, $3, 'floor', 'Floor', 'active', 'general_admission', 100)`, spaceID.UUID(), organizationID.UUID(), venueID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO events (id, organization_id, slug, status, current_revision) VALUES ($1, $2, 'show', 'published', 1)`, eventID.UUID(), organizationID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO event_revisions (id, organization_id, event_id, revision, status, title, checksum, published_at) VALUES ($1, $2, $3, 1, 'published', 'Show', decode(repeat('aa', 32), 'hex'), now())`, newPricingID(t).UUID(), organizationID.UUID(), eventID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO sessions (id, organization_id, event_id, event_revision, venue_id, space_id, inventory_mode, starts_at, ends_at, timezone) VALUES ($1, $2, $3, 1, $4, $5, 'general_admission', now() + interval '1 day', now() + interval '1 day 2 hours', 'UTC')`, sessionID.UUID(), organizationID.UUID(), eventID.UUID(), venueID.UUID(), spaceID.UUID())
	require.NoError(t, err)

	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	firstID := newPricingID(t)
	created, err := repository.Create(ctx, pricing.CreateRecord{Tier: pricing.PriceTier{ID: firstID, OrganizationID: organizationID, SessionID: sessionID, Slug: "standard", DisplayName: "Standard", Currency: "USD", AmountMinor: 2500, MinimumQuantity: 1, MaximumQuantity: 10, Status: pricing.StatusDraft, Version: 1, CreatedAt: now, UpdatedAt: now}, EventID: eventID, AuditID: newPricingID(t), OutboxID: newPricingID(t), ActorType: "user", ActorID: "owner"})
	require.NoError(t, err)
	require.Equal(t, int64(2500), created.AmountMinor)

	_, err = repository.Create(ctx, pricing.CreateRecord{Tier: pricing.PriceTier{ID: newPricingID(t), OrganizationID: organizationID, SessionID: sessionID, Slug: "premium", DisplayName: "Premium", Currency: "EUR", AmountMinor: 5000, MinimumQuantity: 1, MaximumQuantity: 4, Status: pricing.StatusDraft, Version: 1, CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute)}, EventID: eventID, AuditID: newPricingID(t), OutboxID: newPricingID(t), ActorType: "user", ActorID: "owner"})
	require.ErrorIs(t, err, pricing.ErrCurrencyConflict)
	_, err = repository.Create(ctx, pricing.CreateRecord{Tier: pricing.PriceTier{ID: newPricingID(t), OrganizationID: organizationID, SessionID: sessionID, Slug: "standard", DisplayName: "Duplicate", Currency: "USD", AmountMinor: 3000, MinimumQuantity: 1, MaximumQuantity: 4, Status: pricing.StatusDraft, Version: 1, CreatedAt: now.Add(2 * time.Minute), UpdatedAt: now.Add(2 * time.Minute)}, EventID: eventID, AuditID: newPricingID(t), OutboxID: newPricingID(t), ActorType: "user", ActorID: "owner"})
	require.ErrorIs(t, err, pricing.ErrSlugConflict)

	second, err := repository.Create(ctx, pricing.CreateRecord{Tier: pricing.PriceTier{ID: newPricingID(t), OrganizationID: organizationID, SessionID: sessionID, Slug: "premium", DisplayName: "Premium", Currency: "USD", AmountMinor: 5000, MinimumQuantity: 1, MaximumQuantity: 4, Status: pricing.StatusDraft, Version: 1, CreatedAt: now.Add(time.Minute), UpdatedAt: now.Add(time.Minute)}, EventID: eventID, AuditID: newPricingID(t), OutboxID: newPricingID(t), ActorType: "user", ActorID: "owner"})
	require.NoError(t, err)
	require.Equal(t, "premium", second.Slug)

	page, err := repository.List(ctx, organizationID, sessionID, 1, nil)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.NotNil(t, page.NextCursor)
	page, err = repository.List(ctx, organizationID, sessionID, 1, page.NextCursor)
	require.NoError(t, err)
	require.Len(t, page.Items, 1)

	var auditCount, outboxCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM audit_entries`).Scan(&auditCount))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events`).Scan(&outboxCount))
	require.Equal(t, 2, auditCount)
	require.Equal(t, 2, outboxCount)
}

func newPricingID(t *testing.T) identifier.ID {
	t.Helper()
	id, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
