// Package postgres persists session price tiers with tenant-scoped references.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/platform/identifier"
	platformpostgres "github.com/HK9750/venueos/internal/platform/postgres"
	"github.com/HK9750/venueos/internal/pricing"
	"github.com/HK9750/venueos/internal/pricing/postgres/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type Repository struct {
	queries      *sqlc.Queries
	platform     *platformpostgres.Store
	transactions *database.TransactionRunner
}

func New(pool sqlc.DBTX, transactions *database.TransactionRunner) *Repository {
	return &Repository{queries: sqlc.New(pool), platform: platformpostgres.NewStore(pool), transactions: transactions}
}

func (repository *Repository) Create(ctx context.Context, record pricing.CreateRecord) (pricing.PriceTier, error) {
	var result pricing.PriceTier
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		sessionRow, err := queries.LockSessionForPricing(ctx, sqlc.LockSessionForPricingParams{OrganizationID: record.Tier.OrganizationID.UUID(), EventID: record.EventID.UUID(), ID: record.Tier.SessionID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return pricing.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock session for price tier: %w", err)
		}
		if sessionRow.Status == "cancelled" || sessionRow.Status == "completed" {
			return pricing.ErrInvalidState
		}
		if currentCurrency, err := queries.GetSessionPriceCurrency(ctx, sqlc.GetSessionPriceCurrencyParams{OrganizationID: record.Tier.OrganizationID.UUID(), SessionID: record.Tier.SessionID.UUID()}); err == nil && currentCurrency != record.Tier.Currency {
			return pricing.ErrCurrencyConflict
		} else if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("load session currency: %w", err)
		}
		row, err := queries.InsertPriceTier(ctx, sqlc.InsertPriceTierParams{ID: record.Tier.ID.UUID(), OrganizationID: record.Tier.OrganizationID.UUID(), SessionID: record.Tier.SessionID.UUID(), Slug: record.Tier.Slug, DisplayName: record.Tier.DisplayName, Currency: record.Tier.Currency, AmountMinor: record.Tier.AmountMinor, MinimumQuantity: record.Tier.MinimumQuantity, MaximumQuantity: record.Tier.MaximumQuantity, SalesStartAt: nullableTime(record.Tier.SalesStartAt), SalesEndAt: nullableTime(record.Tier.SalesEndAt), Status: string(record.Tier.Status), Version: record.Tier.Version, CreatedAt: record.Tier.CreatedAt})
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "uq_price_tiers_session_slug" {
				return pricing.ErrSlugConflict
			}
			return fmt.Errorf("insert price tier: %w", err)
		}
		result = mapPriceTier(row)
		return repository.recordMutation(ctx, tx, record.Tier.OrganizationID, record.Tier.ID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, "price_tier.created", result.Version, map[string]any{"price_tier_id": result.ID.String(), "session_id": result.SessionID.String(), "currency": result.Currency, "amount_minor": result.AmountMinor, "status": string(result.Status)}, result.CreatedAt)
	})
	return result, err
}

func (repository *Repository) List(ctx context.Context, organizationID, sessionID identifier.ID, limit int32, after *pricing.Cursor) (pricing.Page, error) {
	fetchLimit := limit + 1
	var rows []sqlc.PriceTier
	var err error
	if after == nil {
		rows, err = repository.queries.ListPriceTiers(ctx, sqlc.ListPriceTiersParams{OrganizationID: organizationID.UUID(), SessionID: sessionID.UUID(), Limit: fetchLimit})
	} else {
		rows, err = repository.queries.ListPriceTiersAfter(ctx, sqlc.ListPriceTiersAfterParams{OrganizationID: organizationID.UUID(), SessionID: sessionID.UUID(), CursorCreatedAt: after.CreatedAt.UTC(), CursorID: after.ID.UUID(), PageLimit: fetchLimit})
	}
	if err != nil {
		return pricing.Page{}, fmt.Errorf("list price tiers: %w", err)
	}
	page := pricing.Page{Items: make([]pricing.PriceTier, 0, len(rows))}
	for _, row := range rows {
		page.Items = append(page.Items, mapPriceTier(row))
	}
	if len(page.Items) > int(limit) {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &pricing.Cursor{OrganizationID: organizationID, SessionID: sessionID, CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func (repository *Repository) recordMutation(ctx context.Context, tx pgx.Tx, organizationID, aggregateID, auditID, outboxID identifier.ID, actorType, actorID, action string, version int64, after map[string]any, occurredAt time.Time) error {
	platform := repository.platform.WithTx(tx)
	afterData, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("marshal pricing audit data: %w", err)
	}
	aggregateType := strings.SplitN(action, ".", 2)[0]
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: auditID, OrganizationID: organizationID, ActorType: actorType, ActorID: actorID, Action: action, SubjectType: aggregateType, SubjectID: aggregateID.String(), Result: "success", AfterData: afterData, OccurredAt: occurredAt}); err != nil {
		return err
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: outboxID, OrganizationID: organizationID, AggregateType: aggregateType, AggregateID: aggregateID, AggregateVersion: version, EventType: action, SchemaVersion: 1, Payload: afterData, AvailableAt: occurredAt, OccurredAt: occurredAt})
}

func mapPriceTier(row sqlc.PriceTier) pricing.PriceTier {
	organizationID, _ := identifier.FromUUID(row.OrganizationID)
	sessionID, _ := identifier.FromUUID(row.SessionID)
	priceTierID, _ := identifier.FromUUID(row.ID)
	value := pricing.PriceTier{ID: priceTierID, OrganizationID: organizationID, SessionID: sessionID, Slug: row.Slug, DisplayName: row.DisplayName, Currency: row.Currency, AmountMinor: row.AmountMinor, MinimumQuantity: row.MinimumQuantity, MaximumQuantity: row.MaximumQuantity, Status: pricing.Status(row.Status), Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if row.SalesStartAt.Valid {
		at := row.SalesStartAt.Time.UTC()
		value.SalesStartAt = &at
	}
	if row.SalesEndAt.Valid {
		at := row.SalesEndAt.Time.UTC()
		value.SalesEndAt = &at
	}
	return value
}

func nullableTime(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}
