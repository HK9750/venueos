// Package postgres persists venues and spaces with tenant predicates on every query.
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
	"github.com/HK9750/venueos/internal/venue"
	"github.com/HK9750/venueos/internal/venue/postgres/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Repository struct {
	queries      *sqlc.Queries
	platform     *platformpostgres.Store
	transactions *database.TransactionRunner
}

func New(pool sqlc.DBTX, transactions *database.TransactionRunner) *Repository {
	return &Repository{queries: sqlc.New(pool), platform: platformpostgres.NewStore(pool), transactions: transactions}
}

func (repository *Repository) CreateVenue(ctx context.Context, record venue.CreateVenueRecord) (venue.Venue, error) {
	var result venue.Venue
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		row, err := repository.queries.WithTx(tx).InsertVenue(ctx, sqlc.InsertVenueParams{ID: record.Venue.ID.UUID(), OrganizationID: record.Venue.OrganizationID.UUID(), Slug: record.Venue.Slug, DisplayName: record.Venue.DisplayName, Status: string(record.Venue.Status), Timezone: record.Venue.Timezone, Version: record.Venue.Version, CreatedAt: record.Venue.CreatedAt})
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "uq_venues_organization_slug" {
				return venue.ErrSlugConflict
			}
			return fmt.Errorf("insert venue: %w", err)
		}
		result = mapVenue(row)
		return repository.recordMutation(ctx, tx, record.Venue.OrganizationID, record.Venue.ID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, "venue.created", 1,
			map[string]any{"venue_id": record.Venue.ID.String(), "slug": record.Venue.Slug, "status": string(record.Venue.Status)}, record.Venue.CreatedAt)
	})
	return result, err
}

func (repository *Repository) GetVenue(ctx context.Context, organizationID, venueID identifier.ID) (venue.Venue, error) {
	row, err := repository.queries.GetVenue(ctx, sqlc.GetVenueParams{OrganizationID: organizationID.UUID(), ID: venueID.UUID()})
	if errors.Is(err, pgx.ErrNoRows) {
		return venue.Venue{}, venue.ErrNotFound
	}
	if err != nil {
		return venue.Venue{}, fmt.Errorf("get venue: %w", err)
	}
	return mapVenue(row), nil
}

func (repository *Repository) ListVenues(ctx context.Context, organizationID identifier.ID, limit int32, after *venue.Cursor) (venue.Page[venue.Venue], error) {
	fetchLimit := limit + 1
	var rows []sqlc.Venue
	var err error
	if after == nil {
		rows, err = repository.queries.ListVenues(ctx, sqlc.ListVenuesParams{OrganizationID: organizationID.UUID(), Limit: fetchLimit})
	} else {
		rows, err = repository.queries.ListVenuesAfter(ctx, sqlc.ListVenuesAfterParams{OrganizationID: organizationID.UUID(), CursorCreatedAt: after.CreatedAt.UTC(), CursorID: after.ID.UUID(), PageLimit: fetchLimit})
	}
	if err != nil {
		return venue.Page[venue.Venue]{}, fmt.Errorf("list venues: %w", err)
	}
	page := venue.Page[venue.Venue]{Items: make([]venue.Venue, 0, len(rows))}
	for _, row := range rows {
		page.Items = append(page.Items, mapVenue(row))
	}
	if len(page.Items) > int(limit) {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &venue.Cursor{OrganizationID: organizationID, CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func (repository *Repository) UpdateVenue(ctx context.Context, record venue.UpdateVenueRecord) (venue.Venue, error) {
	var result venue.Venue
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		current, err := queries.GetVenueForUpdate(ctx, sqlc.GetVenueForUpdateParams{OrganizationID: record.Venue.OrganizationID.UUID(), ID: record.Venue.VenueID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return venue.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("load venue for update: %w", err)
		}
		if current.Status == string(venue.StatusArchived) {
			return venue.ErrInvalidState
		}
		if current.Version != record.Venue.Version {
			return venue.ErrVersionConflict
		}
		updated, err := queries.UpdateVenue(ctx, sqlc.UpdateVenueParams{OrganizationID: record.Venue.OrganizationID.UUID(), ID: record.Venue.VenueID.UUID(), Slug: record.Venue.Slug, DisplayName: record.Venue.DisplayName, Timezone: record.Venue.Timezone, UpdatedAt: record.OccurredAt.UTC(), Version: record.Venue.Version})
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "uq_venues_organization_slug" {
				return venue.ErrSlugConflict
			}
			return fmt.Errorf("update venue: %w", err)
		}
		result = mapVenue(updated)
		return repository.recordMutation(ctx, tx, record.Venue.OrganizationID, record.Venue.VenueID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, "venue.updated", result.Version,
			map[string]any{"venue_id": result.ID.String(), "slug": result.Slug, "status": string(result.Status), "version": result.Version}, record.OccurredAt)
	})
	return result, err
}

func (repository *Repository) ArchiveVenue(ctx context.Context, record venue.ArchiveVenueRecord) (venue.Venue, error) {
	var result venue.Venue
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		current, err := queries.GetVenueForUpdate(ctx, sqlc.GetVenueForUpdateParams{OrganizationID: record.OrganizationID.UUID(), ID: record.VenueID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return venue.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("load venue for archive: %w", err)
		}
		if current.Status == string(venue.StatusArchived) {
			return venue.ErrInvalidState
		}
		if current.Version != record.Version {
			return venue.ErrVersionConflict
		}
		updated, err := queries.ArchiveVenue(ctx, sqlc.ArchiveVenueParams{OrganizationID: record.OrganizationID.UUID(), ID: record.VenueID.UUID(), Version: record.Version, UpdatedAt: record.OccurredAt.UTC()})
		if err != nil {
			return fmt.Errorf("archive venue: %w", err)
		}
		result = mapVenue(updated)
		return repository.recordMutation(ctx, tx, record.OrganizationID, record.VenueID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, "venue.archived", result.Version,
			map[string]any{"venue_id": result.ID.String(), "status": string(result.Status), "version": result.Version}, record.OccurredAt)
	})
	return result, err
}

func (repository *Repository) RestoreVenue(ctx context.Context, record venue.RestoreVenueRecord) (venue.Venue, error) {
	var result venue.Venue
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		current, err := queries.GetVenueForUpdate(ctx, sqlc.GetVenueForUpdateParams{OrganizationID: record.OrganizationID.UUID(), ID: record.VenueID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return venue.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("load venue for restore: %w", err)
		}
		if current.Status != string(venue.StatusArchived) {
			return venue.ErrInvalidState
		}
		if current.Version != record.Version {
			return venue.ErrVersionConflict
		}
		updated, err := queries.RestoreVenue(ctx, sqlc.RestoreVenueParams{OrganizationID: record.OrganizationID.UUID(), ID: record.VenueID.UUID(), Version: record.Version, UpdatedAt: record.OccurredAt.UTC()})
		if err != nil {
			return fmt.Errorf("restore venue: %w", err)
		}
		result = mapVenue(updated)
		return repository.recordMutation(ctx, tx, record.OrganizationID, record.VenueID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, "venue.restored", result.Version,
			map[string]any{"venue_id": result.ID.String(), "status": string(result.Status), "version": result.Version}, record.OccurredAt)
	})
	return result, err
}

func (repository *Repository) CreateSpace(ctx context.Context, record venue.CreateSpaceRecord) (venue.Space, error) {
	var result venue.Space
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		parent, err := queries.LockVenue(ctx, sqlc.LockVenueParams{OrganizationID: record.Space.OrganizationID.UUID(), ID: record.Space.VenueID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return venue.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock venue for space: %w", err)
		}
		if parent.Status == string(venue.StatusArchived) {
			return venue.ErrInvalidState
		}
		row, err := queries.InsertSpace(ctx, sqlc.InsertSpaceParams{ID: record.Space.ID.UUID(), OrganizationID: record.Space.OrganizationID.UUID(), VenueID: record.Space.VenueID.UUID(), Slug: record.Space.Slug, DisplayName: record.Space.DisplayName, Status: string(record.Space.Status), InventoryMode: string(record.Space.InventoryMode), PhysicalCapacity: record.Space.PhysicalCapacity, Version: record.Space.Version, CreatedAt: record.Space.CreatedAt})
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "uq_spaces_venue_slug" {
				return venue.ErrSlugConflict
			}
			return fmt.Errorf("insert space: %w", err)
		}
		result = mapSpace(row)
		return repository.recordMutation(ctx, tx, record.Space.OrganizationID, record.Space.ID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, "space.created", 1,
			map[string]any{"space_id": record.Space.ID.String(), "venue_id": record.Space.VenueID.String(), "slug": record.Space.Slug, "inventory_mode": string(record.Space.InventoryMode), "physical_capacity": record.Space.PhysicalCapacity}, record.Space.CreatedAt)
	})
	return result, err
}

func (repository *Repository) GetSpace(ctx context.Context, organizationID, venueID, spaceID identifier.ID) (venue.Space, error) {
	row, err := repository.queries.GetSpace(ctx, sqlc.GetSpaceParams{OrganizationID: organizationID.UUID(), VenueID: venueID.UUID(), ID: spaceID.UUID()})
	if errors.Is(err, pgx.ErrNoRows) {
		return venue.Space{}, venue.ErrNotFound
	}
	if err != nil {
		return venue.Space{}, fmt.Errorf("get space: %w", err)
	}
	return mapSpace(row), nil
}

func (repository *Repository) ListSpaces(ctx context.Context, organizationID, venueID identifier.ID, limit int32, after *venue.Cursor) (venue.Page[venue.Space], error) {
	fetchLimit := limit + 1
	var rows []sqlc.Space
	var err error
	if after == nil {
		rows, err = repository.queries.ListSpaces(ctx, sqlc.ListSpacesParams{OrganizationID: organizationID.UUID(), VenueID: venueID.UUID(), Limit: fetchLimit})
	} else {
		rows, err = repository.queries.ListSpacesAfter(ctx, sqlc.ListSpacesAfterParams{OrganizationID: organizationID.UUID(), VenueID: venueID.UUID(), CursorCreatedAt: after.CreatedAt.UTC(), CursorID: after.ID.UUID(), PageLimit: fetchLimit})
	}
	if err != nil {
		return venue.Page[venue.Space]{}, fmt.Errorf("list spaces: %w", err)
	}
	page := venue.Page[venue.Space]{Items: make([]venue.Space, 0, len(rows))}
	for _, row := range rows {
		page.Items = append(page.Items, mapSpace(row))
	}
	if len(page.Items) > int(limit) {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &venue.Cursor{OrganizationID: organizationID, ParentID: venueID, CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func (repository *Repository) CreateGate(ctx context.Context, record venue.CreateGateRecord) (venue.Gate, error) {
	var result venue.Gate
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		parent, err := queries.LockVenue(ctx, sqlc.LockVenueParams{OrganizationID: record.Gate.OrganizationID.UUID(), ID: record.Gate.VenueID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return venue.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock venue for gate: %w", err)
		}
		if parent.Status == string(venue.StatusArchived) {
			return venue.ErrInvalidState
		}
		spaceID := uuid.Nil
		if record.Gate.SpaceID != nil {
			spaceID = record.Gate.SpaceID.UUID()
			if _, err := queries.GetSpace(ctx, sqlc.GetSpaceParams{OrganizationID: record.Gate.OrganizationID.UUID(), VenueID: record.Gate.VenueID.UUID(), ID: spaceID}); errors.Is(err, pgx.ErrNoRows) {
				return venue.ErrNotFound
			} else if err != nil {
				return fmt.Errorf("check gate space: %w", err)
			}
		}
		row, err := queries.InsertGate(ctx, sqlc.InsertGateParams{ID: record.Gate.ID.UUID(), OrganizationID: record.Gate.OrganizationID.UUID(), VenueID: record.Gate.VenueID.UUID(), Column4: spaceID.String(), Code: record.Gate.Code, DisplayName: record.Gate.DisplayName, Status: string(record.Gate.Status), Version: record.Gate.Version, CreatedAt: record.Gate.CreatedAt})
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "uq_gates_organization_venue_code" {
				return venue.ErrSlugConflict
			}
			return fmt.Errorf("insert gate: %w", err)
		}
		result = mapGate(row)
		return repository.recordMutation(ctx, tx, record.Gate.OrganizationID, record.Gate.ID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, "gate.created", 1,
			map[string]any{"gate_id": record.Gate.ID.String(), "venue_id": record.Gate.VenueID.String(), "code": record.Gate.Code, "status": string(record.Gate.Status)}, record.Gate.CreatedAt)
	})
	return result, err
}

func (repository *Repository) GetGate(ctx context.Context, organizationID, venueID, gateID identifier.ID) (venue.Gate, error) {
	row, err := repository.queries.GetGate(ctx, sqlc.GetGateParams{OrganizationID: organizationID.UUID(), VenueID: venueID.UUID(), ID: gateID.UUID()})
	if errors.Is(err, pgx.ErrNoRows) {
		return venue.Gate{}, venue.ErrNotFound
	}
	if err != nil {
		return venue.Gate{}, fmt.Errorf("get gate: %w", err)
	}
	return mapGate(row), nil
}

func (repository *Repository) ListGates(ctx context.Context, organizationID, venueID identifier.ID, limit int32, after *venue.Cursor) (venue.Page[venue.Gate], error) {
	fetchLimit := limit + 1
	var rows []sqlc.Gate
	var err error
	if after == nil {
		rows, err = repository.queries.ListGates(ctx, sqlc.ListGatesParams{OrganizationID: organizationID.UUID(), VenueID: venueID.UUID(), Limit: fetchLimit})
	} else {
		rows, err = repository.queries.ListGatesAfter(ctx, sqlc.ListGatesAfterParams{OrganizationID: organizationID.UUID(), VenueID: venueID.UUID(), CursorCreatedAt: after.CreatedAt.UTC(), CursorID: after.ID.UUID(), PageLimit: fetchLimit})
	}
	if err != nil {
		return venue.Page[venue.Gate]{}, fmt.Errorf("list gates: %w", err)
	}
	page := venue.Page[venue.Gate]{Items: make([]venue.Gate, 0, len(rows))}
	for _, row := range rows {
		page.Items = append(page.Items, mapGate(row))
	}
	if len(page.Items) > int(limit) {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &venue.Cursor{OrganizationID: organizationID, ParentID: venueID, CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func (repository *Repository) CreateGAPool(ctx context.Context, record venue.CreateGAPoolRecord) (venue.GAPoolTemplate, error) {
	var result venue.GAPoolTemplate
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		spaceRow, err := queries.GetGAPoolSpace(ctx, sqlc.GetGAPoolSpaceParams{OrganizationID: record.Pool.OrganizationID.UUID(), ID: record.Pool.SpaceID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return venue.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("load space for general-admission pool: %w", err)
		}
		if spaceRow.InventoryMode != string(venue.InventoryGA) {
			return venue.ErrInvalidState
		}
		row, err := queries.InsertGAPoolTemplate(ctx, sqlc.InsertGAPoolTemplateParams{ID: record.Pool.ID.UUID(), OrganizationID: record.Pool.OrganizationID.UUID(), SpaceID: record.Pool.SpaceID.UUID(), Slug: record.Pool.Slug, DisplayName: record.Pool.DisplayName, Status: string(record.Pool.Status), PhysicalCapacity: record.Pool.PhysicalCapacity, SellableCapacity: record.Pool.SellableCapacity, Version: record.Pool.Version, CreatedAt: record.Pool.CreatedAt})
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "uq_ga_pool_templates_space_slug" {
				return venue.ErrSlugConflict
			}
			return fmt.Errorf("insert general-admission pool: %w", err)
		}
		result = mapGAPool(row)
		return repository.recordMutation(ctx, tx, record.Pool.OrganizationID, record.Pool.ID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, "ga_pool.created", 1,
			map[string]any{"pool_id": record.Pool.ID.String(), "space_id": record.Pool.SpaceID.String(), "slug": record.Pool.Slug, "physical_capacity": record.Pool.PhysicalCapacity, "sellable_capacity": record.Pool.SellableCapacity}, record.Pool.CreatedAt)
	})
	return result, err
}

func (repository *Repository) GetGAPool(ctx context.Context, organizationID, spaceID, poolID identifier.ID) (venue.GAPoolTemplate, error) {
	row, err := repository.queries.GetGAPoolTemplate(ctx, sqlc.GetGAPoolTemplateParams{OrganizationID: organizationID.UUID(), SpaceID: spaceID.UUID(), ID: poolID.UUID()})
	if errors.Is(err, pgx.ErrNoRows) {
		return venue.GAPoolTemplate{}, venue.ErrNotFound
	}
	if err != nil {
		return venue.GAPoolTemplate{}, fmt.Errorf("get general-admission pool: %w", err)
	}
	return mapGAPool(row), nil
}

func (repository *Repository) ListGAPools(ctx context.Context, organizationID, spaceID identifier.ID, limit int32, after *venue.Cursor) (venue.Page[venue.GAPoolTemplate], error) {
	fetchLimit := limit + 1
	var rows []sqlc.GaPoolTemplate
	var err error
	if after == nil {
		rows, err = repository.queries.ListGAPoolTemplates(ctx, sqlc.ListGAPoolTemplatesParams{OrganizationID: organizationID.UUID(), SpaceID: spaceID.UUID(), Limit: fetchLimit})
	} else {
		rows, err = repository.queries.ListGAPoolTemplatesAfter(ctx, sqlc.ListGAPoolTemplatesAfterParams{OrganizationID: organizationID.UUID(), SpaceID: spaceID.UUID(), CursorCreatedAt: after.CreatedAt.UTC(), CursorID: after.ID.UUID(), PageLimit: fetchLimit})
	}
	if err != nil {
		return venue.Page[venue.GAPoolTemplate]{}, fmt.Errorf("list general-admission pools: %w", err)
	}
	page := venue.Page[venue.GAPoolTemplate]{Items: make([]venue.GAPoolTemplate, 0, len(rows))}
	for _, row := range rows {
		page.Items = append(page.Items, mapGAPool(row))
	}
	if len(page.Items) > int(limit) {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &venue.Cursor{OrganizationID: organizationID, ParentID: spaceID, CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func (repository *Repository) recordMutation(ctx context.Context, tx pgx.Tx, organizationID, aggregateID, auditID, outboxID identifier.ID, actorType, actorID, action string, version int64, after map[string]any, occurredAt time.Time) error {
	platform := repository.platform.WithTx(tx)
	afterData, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("marshal venue audit data: %w", err)
	}
	aggregateType := strings.SplitN(action, ".", 2)[0]
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: auditID, OrganizationID: organizationID, ActorType: actorType, ActorID: actorID, Action: action, SubjectType: aggregateType, SubjectID: aggregateID.String(), Result: "success", AfterData: afterData, OccurredAt: occurredAt}); err != nil {
		return err
	}
	payload, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("marshal venue event: %w", err)
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: outboxID, OrganizationID: organizationID, AggregateType: aggregateType, AggregateID: aggregateID, AggregateVersion: version, EventType: action, SchemaVersion: 1, Payload: payload, AvailableAt: occurredAt, OccurredAt: occurredAt})
}

func mapVenue(row sqlc.Venue) venue.Venue {
	organizationID, _ := identifier.FromUUID(row.OrganizationID)
	venueID, _ := identifier.FromUUID(row.ID)
	return venue.Venue{ID: venueID, OrganizationID: organizationID, Slug: row.Slug, DisplayName: row.DisplayName, Status: venue.Status(row.Status), Timezone: row.Timezone, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func mapSpace(row sqlc.Space) venue.Space {
	organizationID, _ := identifier.FromUUID(row.OrganizationID)
	venueID, _ := identifier.FromUUID(row.VenueID)
	spaceID, _ := identifier.FromUUID(row.ID)
	return venue.Space{ID: spaceID, OrganizationID: organizationID, VenueID: venueID, Slug: row.Slug, DisplayName: row.DisplayName, Status: venue.Status(row.Status), InventoryMode: venue.InventoryMode(row.InventoryMode), PhysicalCapacity: row.PhysicalCapacity, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func mapGate(row sqlc.Gate) venue.Gate {
	organizationID, _ := identifier.FromUUID(row.OrganizationID)
	venueID, _ := identifier.FromUUID(row.VenueID)
	gateID, _ := identifier.FromUUID(row.ID)
	value := venue.Gate{ID: gateID, OrganizationID: organizationID, VenueID: venueID, Code: row.Code, DisplayName: row.DisplayName, Status: venue.Status(row.Status), Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if row.SpaceID.Valid {
		spaceID, _ := identifier.FromUUID(row.SpaceID.Bytes)
		value.SpaceID = &spaceID
	}
	return value
}

func mapGAPool(row sqlc.GaPoolTemplate) venue.GAPoolTemplate {
	organizationID, _ := identifier.FromUUID(row.OrganizationID)
	spaceID, _ := identifier.FromUUID(row.SpaceID)
	poolID, _ := identifier.FromUUID(row.ID)
	return venue.GAPoolTemplate{ID: poolID, OrganizationID: organizationID, SpaceID: spaceID, Slug: row.Slug, DisplayName: row.DisplayName, Status: venue.Status(row.Status), PhysicalCapacity: row.PhysicalCapacity, SellableCapacity: row.SellableCapacity, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
