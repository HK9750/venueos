//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/cart"
	cartpostgres "github.com/HK9750/venueos/internal/cart/postgres"
	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/money"
	"github.com/HK9750/venueos/internal/pricing"
	"github.com/HK9750/venueos/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	postgrescontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestCartRepositoryLocksHoldAndPersistsVersionedQuote(t *testing.T) {
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
	repository := cartpostgres.New(pool, runner)

	organizationID, venueID, spaceID, eventID, sessionID, holdID := newCartID(t), newCartID(t), newCartID(t), newCartID(t), newCartID(t), newCartID(t)
	_, err = pool.Exec(ctx, `INSERT INTO organizations (id, slug, display_name, default_locale, default_timezone, default_currency) VALUES ($1, 'cart-catalog', 'Cart Catalog', 'en', 'UTC', 'USD')`, organizationID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO venues (id, organization_id, slug, display_name, status, timezone) VALUES ($1, $2, 'main', 'Main', 'active', 'UTC')`, venueID.UUID(), organizationID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO spaces (id, organization_id, venue_id, slug, display_name, status, inventory_mode, physical_capacity) VALUES ($1, $2, $3, 'floor', 'Floor', 'active', 'general_admission', 100)`, spaceID.UUID(), organizationID.UUID(), venueID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO events (id, organization_id, slug, status, current_revision) VALUES ($1, $2, 'show', 'published', 1)`, eventID.UUID(), organizationID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO event_revisions (id, organization_id, event_id, revision, status, title, checksum, published_at) VALUES ($1, $2, $3, 1, 'published', 'Show', decode(repeat('aa', 32), 'hex'), now())`, newCartID(t).UUID(), organizationID.UUID(), eventID.UUID())
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO sessions (id, organization_id, event_id, event_revision, venue_id, space_id, inventory_mode, starts_at, ends_at, timezone) VALUES ($1, $2, $3, 1, $4, $5, 'general_admission', now() + interval '1 day', now() + interval '1 day 2 hours', 'UTC')`, sessionID.UUID(), organizationID.UUID(), eventID.UUID(), venueID.UUID(), spaceID.UUID())
	require.NoError(t, err)
	var owner [32]byte
	owner[0] = 7
	_, err = pool.Exec(ctx, `INSERT INTO holds (id, organization_id, session_id, owner_token_hash, currency, expires_at) VALUES ($1, $2, $3, $4, 'USD', now() + interval '10 minutes')`, holdID.UUID(), organizationID.UUID(), sessionID.UUID(), owner[:])
	require.NoError(t, err)

	usd, _ := money.NewCurrency("USD")
	unit, _ := money.New(2500, usd)
	quote, err := pricing.CalculateQuote(pricing.QuoteInput{Lines: []pricing.LineInput{{PriceTierID: newCartID(t), Quantity: 1, UnitPrice: unit}}})
	require.NoError(t, err)
	now := time.Now().UTC()
	created, err := repository.Create(ctx, cart.CreateRecord{Cart: mustCart(t, organizationID, sessionID, holdID, owner, quote, now), AuditID: newCartID(t), OutboxID: newCartID(t), ActorType: "user", ActorID: "cart-owner"})
	require.NoError(t, err)
	require.Equal(t, cart.StatusActive, created.Status)
	require.Equal(t, int64(1), created.Version)
	fetched, err := repository.Get(ctx, organizationID, created.ID, owner)
	require.NoError(t, err)
	require.Equal(t, created.QuoteSHA256, fetched.QuoteSHA256)
	_, err = repository.Create(ctx, cart.CreateRecord{Cart: mustCart(t, organizationID, sessionID, holdID, owner, quote, now.Add(time.Second)), AuditID: newCartID(t), OutboxID: newCartID(t), ActorType: "user", ActorID: "cart-owner"})
	require.ErrorIs(t, err, cart.ErrActiveCartExists)
	updated, err := repository.UpdateQuote(ctx, cart.UpdateQuoteRecord{OrganizationID: organizationID, CartID: created.ID, OwnerTokenHash: owner, ExpectedVersion: 1, Quote: quote, AuditID: newCartID(t), OutboxID: newCartID(t), ActorType: "user", ActorID: "cart-owner", OccurredAt: now.Add(time.Second)})
	require.NoError(t, err)
	require.Equal(t, int64(2), updated.Version)
	_, err = repository.Get(ctx, newCartID(t), created.ID, owner)
	require.ErrorIs(t, err, cart.ErrNotFound)
}

func mustCart(t *testing.T, organizationID, sessionID, holdID identifier.ID, owner [32]byte, quote pricing.Quote, now time.Time) cart.Cart {
	t.Helper()
	value, err := cart.Build(cart.CreateInput{ID: newCartID(t), OrganizationID: organizationID, SessionID: sessionID, HoldID: holdID, OwnerTokenHash: owner, Currency: "USD", Quote: quote, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func newCartID(t *testing.T) identifier.ID {
	t.Helper()
	id, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
