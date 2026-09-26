// Package postgres loads catalog state for publication validation.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/publication"
	"github.com/HK9750/venueos/internal/publication/postgres/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type Repository struct{ queries *sqlc.Queries }

func New(pool sqlc.DBTX, _ *database.TransactionRunner) *Repository {
	return &Repository{queries: sqlc.New(pool)}
}

func (repository *Repository) Load(ctx context.Context, organizationID, eventID identifier.ID) (publication.EventSnapshot, error) {
	eventRow, err := repository.queries.GetPublicationEvent(ctx, sqlc.GetPublicationEventParams{OrganizationID: organizationID.UUID(), ID: eventID.UUID()})
	if errors.Is(err, pgx.ErrNoRows) {
		return publication.EventSnapshot{}, publication.ErrNotFound
	}
	if err != nil {
		return publication.EventSnapshot{}, fmt.Errorf("load publication event: %w", err)
	}
	rows, err := repository.queries.ListPublicationSessions(ctx, sqlc.ListPublicationSessionsParams{OrganizationID: organizationID.UUID(), EventID: eventID.UUID()})
	if err != nil {
		return publication.EventSnapshot{}, fmt.Errorf("load publication sessions: %w", err)
	}
	snapshot := publication.EventSnapshot{Status: eventRow.Status, CurrentRevision: eventRow.CurrentRevision, Sessions: make([]publication.SessionSnapshot, 0, len(rows))}
	for _, row := range rows {
		var seatMapID *identifier.ID
		if row.SeatMapVersionID.Valid {
			parsed, parseErr := identifier.FromUUID(uuid.UUID(row.SeatMapVersionID.Bytes))
			if parseErr != nil {
				return publication.EventSnapshot{}, fmt.Errorf("map publication seat map identifier: %w", parseErr)
			}
			seatMapID = &parsed
		}
		sessionID, parseErr := identifier.FromUUID(row.ID)
		if parseErr != nil {
			return publication.EventSnapshot{}, fmt.Errorf("map publication session identifier: %w", parseErr)
		}
		snapshot.Sessions = append(snapshot.Sessions, publication.SessionSnapshot{ID: sessionID, Status: row.Status, InventoryMode: row.InventoryMode, SeatMapVersionID: seatMapID, VenueStatus: row.VenueStatus, SpaceStatus: row.SpaceStatus, SeatMapStatus: row.SeatMapStatus, PriceTierCount: row.PriceTierCount, SeatSellableCapacity: row.SeatSellableCapacity, GAPoolCount: row.GaPoolCount, GASellableCapacity: row.GaSellableCapacity, HardAllocationQuantity: row.HardAllocationQuantity})
	}
	return snapshot, nil
}
