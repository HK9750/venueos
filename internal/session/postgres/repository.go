// Package postgres persists tenant-scoped sessions and scheduling references.
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
	"github.com/HK9750/venueos/internal/session"
	"github.com/HK9750/venueos/internal/session/postgres/sqlc"
	"github.com/HK9750/venueos/internal/venue"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

func (repository *Repository) Create(ctx context.Context, record session.CreateRecord) (session.Session, error) {
	var result session.Session
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		space, err := queries.LockSpaceForSession(ctx, sqlc.LockSpaceForSessionParams{OrganizationID: record.Session.OrganizationID.UUID(), VenueID: record.Session.VenueID.UUID(), ID: record.Session.SpaceID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return session.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock space for session: %w", err)
		}
		if space.Status == "archived" || space.InventoryMode != string(session.InventoryAssigned) && space.InventoryMode != string(session.InventoryGA) {
			return session.ErrInvalidState
		}
		var gaTemplates []sqlc.ListActiveGAPoolTemplatesForSessionRow
		if space.InventoryMode == string(session.InventoryGA) {
			gaTemplates, err = queries.ListActiveGAPoolTemplatesForSession(ctx, sqlc.ListActiveGAPoolTemplatesForSessionParams{OrganizationID: record.Session.OrganizationID.UUID(), SpaceID: record.Session.SpaceID.UUID()})
			if err != nil {
				return fmt.Errorf("list active general-admission pools: %w", err)
			}
			if len(gaTemplates) == 0 {
				return session.ErrInvalidState
			}
		}
		var publishedSeatMap sqlc.GetPublishedSeatMapForSessionRow
		var seatMapContent venue.SeatMapContent
		if _, err := queries.GetPublishedEventRevision(ctx, sqlc.GetPublishedEventRevisionParams{OrganizationID: record.Session.OrganizationID.UUID(), EventID: record.Session.EventID.UUID(), Revision: record.Session.EventRevision}); errors.Is(err, pgx.ErrNoRows) {
			return session.ErrInvalidState
		} else if err != nil {
			return fmt.Errorf("check published event revision: %w", err)
		}
		if space.InventoryMode == string(session.InventoryAssigned) {
			if record.Session.SeatMapVersionID == nil {
				return session.ErrInvalidState
			}
			publishedSeatMap, err = queries.GetPublishedSeatMapForSession(ctx, sqlc.GetPublishedSeatMapForSessionParams{OrganizationID: record.Session.OrganizationID.UUID(), SpaceID: record.Session.SpaceID.UUID(), ID: record.Session.SeatMapVersionID.UUID()})
			if errors.Is(err, pgx.ErrNoRows) {
				return session.ErrInvalidState
			}
			if err != nil {
				return fmt.Errorf("check published seat map: %w", err)
			}
			if err := json.Unmarshal(publishedSeatMap.Content, &seatMapContent); err != nil {
				return fmt.Errorf("decode published seat map: %w", err)
			}
			if countSeatMapSeats(seatMapContent) == 0 {
				return session.ErrInvalidState
			}
		} else if record.Session.SeatMapVersionID != nil {
			return session.ErrInvalidState
		}
		overlaps, err := queries.SessionOverlap(ctx, sqlc.SessionOverlapParams{OrganizationID: record.Session.OrganizationID.UUID(), VenueID: record.Session.VenueID.UUID(), SpaceID: record.Session.SpaceID.UUID(), NewEndsAt: record.Session.EndsAt.UTC(), NewStartsAt: record.Session.StartsAt.UTC()})
		if err != nil {
			return fmt.Errorf("check session overlap: %w", err)
		}
		if overlaps {
			return session.ErrOverlap
		}
		row, err := queries.InsertSession(ctx, sqlc.InsertSessionParams{ID: record.Session.ID.UUID(), OrganizationID: record.Session.OrganizationID.UUID(), EventID: record.Session.EventID.UUID(), EventRevision: record.Session.EventRevision, VenueID: record.Session.VenueID.UUID(), SpaceID: record.Session.SpaceID.UUID(), SeatMapVersionID: nullableUUID(record.Session.SeatMapVersionID), InventoryMode: sessionMode(space.InventoryMode), DoorsAt: nullableTime(record.Session.DoorsAt), StartsAt: record.Session.StartsAt.UTC(), EndsAt: record.Session.EndsAt.UTC(), SalesStartAt: nullableTime(record.Session.SalesStartAt), SalesEndAt: nullableTime(record.Session.SalesEndAt), Timezone: record.Session.Timezone, Status: string(record.Session.Status), Version: record.Session.Version, CreatedAt: record.Session.CreatedAt})
		if err != nil {
			return fmt.Errorf("insert session: %w", err)
		}
		materializedSeatCount := 0
		if space.InventoryMode == string(session.InventoryAssigned) {
			for _, section := range seatMapContent.Sections {
				for _, row := range section.Rows {
					for _, seat := range row.Seats {
						seatID, err := identifier.New()
						if err != nil {
							return fmt.Errorf("create session seat ID: %w", err)
						}
						if err := queries.InsertSessionSeat(ctx, sqlc.InsertSessionSeatParams{
							ID: seatID.UUID(), OrganizationID: record.Session.OrganizationID.UUID(), SessionID: record.Session.ID.UUID(), SeatMapVersionID: publishedSeatMap.ID,
							SectionKey: section.ID, RowKey: row.ID, SeatKey: seat.ID, Label: seat.Label, Category: seat.Category, Sellable: seat.Sellable,
							Wheelchair: seat.Wheelchair, CompanionTo: nullableText(seat.CompanionTo), CreatedAt: record.Session.CreatedAt.UTC(),
						}); err != nil {
							return fmt.Errorf("materialize session seat: %w", err)
						}
						materializedSeatCount++
					}
				}
			}
		}
		for _, template := range gaTemplates {
			poolID, err := identifier.New()
			if err != nil {
				return fmt.Errorf("create session general-admission pool ID: %w", err)
			}
			if err := queries.InsertSessionGAPool(ctx, sqlc.InsertSessionGAPoolParams{
				ID: poolID.UUID(), OrganizationID: record.Session.OrganizationID.UUID(), SessionID: record.Session.ID.UUID(), SourcePoolID: template.ID,
				Slug: template.Slug, DisplayName: template.DisplayName, PhysicalCapacity: template.PhysicalCapacity, SellableCapacity: template.SellableCapacity,
				CreatedAt: record.Session.CreatedAt.UTC(),
			}); err != nil {
				return fmt.Errorf("materialize general-admission pool: %w", err)
			}
		}
		result = mapSession(row)
		return repository.recordMutation(ctx, tx, record.Session.OrganizationID, record.Session.ID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, "session.created", result.Version, map[string]any{"session_id": result.ID.String(), "event_id": result.EventID.String(), "event_revision": result.EventRevision, "venue_id": result.VenueID.String(), "space_id": result.SpaceID.String(), "status": string(result.Status), "general_admission_pool_count": len(gaTemplates), "assigned_seat_count": materializedSeatCount}, result.CreatedAt)
	})
	return result, err
}

func (repository *Repository) Get(ctx context.Context, organizationID, eventID, sessionID identifier.ID) (session.Session, error) {
	row, err := repository.queries.GetSession(ctx, sqlc.GetSessionParams{OrganizationID: organizationID.UUID(), EventID: eventID.UUID(), ID: sessionID.UUID()})
	if errors.Is(err, pgx.ErrNoRows) {
		return session.Session{}, session.ErrNotFound
	}
	if err != nil {
		return session.Session{}, fmt.Errorf("get session: %w", err)
	}
	return mapSession(row), nil
}

func (repository *Repository) List(ctx context.Context, organizationID, eventID identifier.ID, limit int32, after *session.Cursor) (session.Page, error) {
	fetchLimit := limit + 1
	var rows []sqlc.Session
	var err error
	if after == nil {
		rows, err = repository.queries.ListSessions(ctx, sqlc.ListSessionsParams{OrganizationID: organizationID.UUID(), EventID: eventID.UUID(), Limit: fetchLimit})
	} else {
		rows, err = repository.queries.ListSessionsAfter(ctx, sqlc.ListSessionsAfterParams{OrganizationID: organizationID.UUID(), EventID: eventID.UUID(), CursorCreatedAt: after.CreatedAt.UTC(), CursorID: after.ID.UUID(), PageLimit: fetchLimit})
	}
	if err != nil {
		return session.Page{}, fmt.Errorf("list sessions: %w", err)
	}
	page := session.Page{Items: make([]session.Session, 0, len(rows))}
	for _, row := range rows {
		page.Items = append(page.Items, mapSession(row))
	}
	if len(page.Items) > int(limit) {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &session.Cursor{OrganizationID: organizationID, EventID: eventID, CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func (repository *Repository) recordMutation(ctx context.Context, tx pgx.Tx, organizationID, aggregateID, auditID, outboxID identifier.ID, actorType, actorID, action string, version int64, after map[string]any, occurredAt time.Time) error {
	platform := repository.platform.WithTx(tx)
	afterData, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("marshal session audit data: %w", err)
	}
	aggregateType := strings.SplitN(action, ".", 2)[0]
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: auditID, OrganizationID: organizationID, ActorType: actorType, ActorID: actorID, Action: action, SubjectType: aggregateType, SubjectID: aggregateID.String(), Result: "success", AfterData: afterData, OccurredAt: occurredAt}); err != nil {
		return err
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: outboxID, OrganizationID: organizationID, AggregateType: aggregateType, AggregateID: aggregateID, AggregateVersion: version, EventType: action, SchemaVersion: 1, Payload: afterData, AvailableAt: occurredAt, OccurredAt: occurredAt})
}

func mapSession(row sqlc.Session) session.Session {
	organizationID, _ := identifier.FromUUID(row.OrganizationID)
	eventID, _ := identifier.FromUUID(row.EventID)
	venueID, _ := identifier.FromUUID(row.VenueID)
	spaceID, _ := identifier.FromUUID(row.SpaceID)
	value := session.Session{ID: mustID(row.ID), OrganizationID: organizationID, EventID: eventID, EventRevision: row.EventRevision, VenueID: venueID, SpaceID: spaceID, InventoryMode: session.InventoryMode(row.InventoryMode), StartsAt: row.StartsAt.UTC(), EndsAt: row.EndsAt.UTC(), Timezone: row.Timezone, Status: session.Status(row.Status), Version: row.Version, InventoryRevision: row.InventoryRevision, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if row.SeatMapVersionID.Valid {
		id, _ := identifier.FromUUID(row.SeatMapVersionID.Bytes)
		value.SeatMapVersionID = &id
	}
	if row.DoorsAt.Valid {
		at := row.DoorsAt.Time.UTC()
		value.DoorsAt = &at
	}
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

func mustID(value uuid.UUID) identifier.ID {
	id, _ := identifier.FromUUID(value)
	return id
}

func nullableUUID(value *identifier.ID) pgtype.UUID {
	if value == nil || value.IsZero() {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: [16]byte(value.UUID()), Valid: true}
}

func nullableTime(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func nullableText(value string) pgtype.Text {
	if value == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: value, Valid: true}
}

func countSeatMapSeats(content venue.SeatMapContent) int {
	count := 0
	for _, section := range content.Sections {
		for _, row := range section.Rows {
			count += len(row.Seats)
		}
	}
	return count
}

func sessionMode(value string) string { return value }
