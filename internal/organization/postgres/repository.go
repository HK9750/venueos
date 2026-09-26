// Package postgres persists the organization domain in PostgreSQL.
package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/organization"
	"github.com/HK9750/venueos/internal/organization/postgres/sqlc"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/money"
	platformpostgres "github.com/HK9750/venueos/internal/platform/postgres"
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

func (repository *Repository) Create(ctx context.Context, record organization.CreateRecord) (organization.Organization, bool, error) {
	var result organization.Organization
	var replayed bool
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		platform := repository.platform.WithTx(tx)
		rows, err := queries.InsertPlatformIdempotencyRecord(ctx, sqlc.InsertPlatformIdempotencyRecordParams{
			ID: record.IdempotencyID.UUID(), PrincipalID: record.PrincipalID,
			IdempotencyKey: record.IdempotencyKey, RequestFingerprint: record.Fingerprint[:],
			LockedUntil: pgtype.Timestamptz{Time: record.Organization.CreatedAt.Add(time.Minute), Valid: true},
			ExpiresAt:   record.IdempotencyExpiry, CreatedAt: record.Organization.CreatedAt,
		})
		if err != nil {
			return fmt.Errorf("insert organization idempotency record: %w", err)
		}
		if rows == 0 {
			existing, getErr := queries.GetPlatformIdempotencyRecordForUpdate(ctx, sqlc.GetPlatformIdempotencyRecordForUpdateParams{
				PrincipalID: record.PrincipalID, IdempotencyKey: record.IdempotencyKey,
			})
			if getErr != nil {
				return fmt.Errorf("get organization idempotency record: %w", getErr)
			}
			if !bytes.Equal(existing.RequestFingerprint, record.Fingerprint[:]) {
				return organization.ErrKeyReused
			}
			if existing.State != "completed" || !existing.ResourceID.Valid {
				return errors.New("organization idempotency record has no completed resource")
			}
			resourceID, parseErr := identifier.Parse(existing.ResourceID.String)
			if parseErr != nil {
				return fmt.Errorf("parse replay organization ID: %w", parseErr)
			}
			row, getErr := queries.GetOrganization(ctx, resourceID.UUID())
			if getErr != nil {
				return fmt.Errorf("get replay organization: %w", getErr)
			}
			result, replayed = mapOrganization(row), true
			return nil
		}

		row, err := queries.InsertOrganization(ctx, sqlc.InsertOrganizationParams{
			ID: record.Organization.ID.UUID(), Slug: record.Organization.Slug,
			DisplayName: record.Organization.DisplayName, DefaultLocale: record.Organization.DefaultLocale,
			DefaultTimezone: record.Organization.DefaultTimezone,
			DefaultCurrency: record.Organization.DefaultCurrency.String(), CreatedAt: record.Organization.CreatedAt,
		})
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "uq_organizations_slug" {
				return organization.ErrSlugConflict
			}
			return fmt.Errorf("insert organization: %w", err)
		}
		after, _ := json.Marshal(map[string]any{"slug": row.Slug, "status": row.Status})
		if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{
			ID: record.AuditID, OrganizationID: record.Organization.ID,
			ActorType: record.PrincipalType, ActorID: record.PrincipalID,
			Action: "organization.created", SubjectType: "organization",
			SubjectID: record.Organization.ID.String(), Result: "success",
			AfterData: after, OccurredAt: record.Organization.CreatedAt,
		}); err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{
			"organization_id": record.Organization.ID.String(), "slug": row.Slug,
		})
		if err := platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{
			ID: record.OutboxID, OrganizationID: record.Organization.ID,
			AggregateType: "organization", AggregateID: record.Organization.ID,
			AggregateVersion: 1, EventType: "organization.created", SchemaVersion: 1,
			Payload: payload, AvailableAt: record.Organization.CreatedAt, OccurredAt: record.Organization.CreatedAt,
		}); err != nil {
			return err
		}
		completed, err := queries.CompletePlatformIdempotencyRecord(ctx, sqlc.CompletePlatformIdempotencyRecordParams{
			ID: record.IdempotencyID.UUID(), ResourceID: pgtype.Text{String: record.Organization.ID.String(), Valid: true},
			UpdatedAt: record.Organization.CreatedAt,
		})
		if err != nil {
			return fmt.Errorf("complete organization idempotency record: %w", err)
		}
		if completed != 1 {
			return fmt.Errorf("complete organization idempotency record: updated %d rows", completed)
		}
		result = mapOrganization(row)
		return nil
	})
	return result, replayed, err
}

func (repository *Repository) Get(ctx context.Context, id identifier.ID) (organization.Organization, error) {
	row, err := repository.queries.GetOrganization(ctx, id.UUID())
	if errors.Is(err, pgx.ErrNoRows) {
		return organization.Organization{}, organization.ErrNotFound
	}
	if err != nil {
		return organization.Organization{}, fmt.Errorf("select organization: %w", err)
	}
	return mapOrganization(row), nil
}

func mapOrganization(row sqlc.Organization) organization.Organization {
	id, _ := identifier.FromUUID(row.ID)
	currency, _ := money.NewCurrency(row.DefaultCurrency)
	return organization.Organization{ID: id, Slug: row.Slug, DisplayName: row.DisplayName,
		Status: row.Status, DefaultLocale: row.DefaultLocale, DefaultTimezone: row.DefaultTimezone,
		DefaultCurrency: currency, SettingsVersion: row.SettingsVersion,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
