package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/venue"
	"github.com/HK9750/venueos/internal/venue/postgres/sqlc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (repository *Repository) CreateSeatMap(ctx context.Context, record venue.CreateSeatMapRecord) (venue.SeatMap, error) {
	var result venue.SeatMap
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		space, err := queries.LockSpace(ctx, sqlc.LockSpaceParams{OrganizationID: record.SeatMap.OrganizationID.UUID(), ID: record.SeatMap.SpaceID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return venue.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock space for seat map: %w", err)
		}
		if space.Status == string(venue.StatusArchived) || space.InventoryMode != string(venue.InventoryAssigned) {
			return venue.ErrInvalidState
		}
		row, err := queries.InsertSeatMapVersion(ctx, sqlc.InsertSeatMapVersionParams{
			ID: record.SeatMap.ID.UUID(), OrganizationID: record.SeatMap.OrganizationID.UUID(), SpaceID: record.SeatMap.SpaceID.UUID(),
			Name: record.SeatMap.Name, Status: string(record.SeatMap.Status), Revision: record.SeatMap.Revision, Checksum: record.Checksum, Content: record.Content,
			SeatCount: record.SeatMap.SeatCount, SellableCount: record.SeatMap.SellableCount, Version: record.SeatMap.Version, CreatedAt: record.SeatMap.CreatedAt,
		})
		if err != nil {
			return fmt.Errorf("insert seat map: %w", err)
		}
		result, err = mapSeatMap(row)
		if err != nil {
			return err
		}
		return repository.recordMutation(ctx, tx, record.SeatMap.OrganizationID, record.SeatMap.ID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, "seat_map.created", 1,
			map[string]any{"seat_map_id": record.SeatMap.ID.String(), "space_id": record.SeatMap.SpaceID.String(), "status": string(record.SeatMap.Status), "seat_count": record.SeatMap.SeatCount, "sellable_count": record.SeatMap.SellableCount}, record.SeatMap.CreatedAt)
	})
	return result, err
}

func (repository *Repository) GetSeatMap(ctx context.Context, organizationID, spaceID, seatMapID identifier.ID) (venue.SeatMap, error) {
	row, err := repository.queries.GetSeatMapVersion(ctx, sqlc.GetSeatMapVersionParams{OrganizationID: organizationID.UUID(), SpaceID: spaceID.UUID(), ID: seatMapID.UUID()})
	if errors.Is(err, pgx.ErrNoRows) {
		return venue.SeatMap{}, venue.ErrNotFound
	}
	if err != nil {
		return venue.SeatMap{}, fmt.Errorf("get seat map: %w", err)
	}
	return mapSeatMap(row)
}

func (repository *Repository) ListSeatMaps(ctx context.Context, organizationID, spaceID identifier.ID, limit int32, after *venue.Cursor) (venue.Page[venue.SeatMap], error) {
	fetchLimit := limit + 1
	var rows []sqlc.SeatMapVersion
	var err error
	if after == nil {
		rows, err = repository.queries.ListSeatMapVersions(ctx, sqlc.ListSeatMapVersionsParams{OrganizationID: organizationID.UUID(), SpaceID: spaceID.UUID(), Limit: fetchLimit})
	} else {
		rows, err = repository.queries.ListSeatMapVersionsAfter(ctx, sqlc.ListSeatMapVersionsAfterParams{OrganizationID: organizationID.UUID(), SpaceID: spaceID.UUID(), CursorCreatedAt: after.CreatedAt.UTC(), CursorID: after.ID.UUID(), PageLimit: fetchLimit})
	}
	if err != nil {
		return venue.Page[venue.SeatMap]{}, fmt.Errorf("list seat maps: %w", err)
	}
	page := venue.Page[venue.SeatMap]{Items: make([]venue.SeatMap, 0, len(rows))}
	for _, row := range rows {
		mapped, mapErr := mapSeatMap(row)
		if mapErr != nil {
			return venue.Page[venue.SeatMap]{}, mapErr
		}
		page.Items = append(page.Items, mapped)
	}
	if len(page.Items) > int(limit) {
		page.Items = page.Items[:limit]
		last := page.Items[len(page.Items)-1]
		page.NextCursor = &venue.Cursor{OrganizationID: organizationID, ParentID: spaceID, CreatedAt: last.CreatedAt, ID: last.ID}
	}
	return page, nil
}

func (repository *Repository) UpdateSeatMap(ctx context.Context, record venue.UpdateSeatMapRecord) (venue.SeatMap, error) {
	var result venue.SeatMap
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		space, err := queries.LockSpace(ctx, sqlc.LockSpaceParams{OrganizationID: record.Input.OrganizationID.UUID(), ID: record.Input.SpaceID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return venue.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock space for seat-map update: %w", err)
		}
		if space.Status == string(venue.StatusArchived) || space.InventoryMode != string(venue.InventoryAssigned) {
			return venue.ErrInvalidState
		}
		current, err := queries.GetSeatMapVersionForUpdate(ctx, sqlc.GetSeatMapVersionForUpdateParams{OrganizationID: record.Input.OrganizationID.UUID(), SpaceID: record.Input.SpaceID.UUID(), ID: record.Input.SeatMapID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return venue.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("load seat map for update: %w", err)
		}
		if current.Status != string(venue.SeatMapDraft) {
			return venue.ErrInvalidState
		}
		if current.Version != record.Input.Version {
			return venue.ErrVersionConflict
		}
		row, err := queries.UpdateSeatMapDraft(ctx, sqlc.UpdateSeatMapDraftParams{OrganizationID: record.Input.OrganizationID.UUID(), SpaceID: record.Input.SpaceID.UUID(), ID: record.Input.SeatMapID.UUID(), Name: record.Input.Name, Checksum: record.Checksum, Content: record.Content, SeatCount: record.SeatCount, SellableCount: record.Sellable, UpdatedAt: record.OccurredAt.UTC(), Version: record.Input.Version})
		if errors.Is(err, pgx.ErrNoRows) {
			return venue.ErrVersionConflict
		}
		if err != nil {
			return fmt.Errorf("update seat-map draft: %w", err)
		}
		result, err = mapSeatMap(row)
		if err != nil {
			return err
		}
		return repository.recordMutation(ctx, tx, record.Input.OrganizationID, record.Input.SeatMapID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, "seat_map.updated", result.Version,
			map[string]any{"seat_map_id": result.ID.String(), "space_id": result.SpaceID.String(), "status": string(result.Status), "version": result.Version, "checksum": result.Checksum}, record.OccurredAt)
	})
	return result, err
}

func (repository *Repository) PublishSeatMap(ctx context.Context, record venue.PublishSeatMapRecord) (venue.SeatMap, error) {
	var result venue.SeatMap
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		space, err := queries.LockSpace(ctx, sqlc.LockSpaceParams{OrganizationID: record.Input.OrganizationID.UUID(), ID: record.Input.SpaceID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return venue.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock space for seat-map publish: %w", err)
		}
		if space.Status == string(venue.StatusArchived) || space.InventoryMode != string(venue.InventoryAssigned) {
			return venue.ErrInvalidState
		}
		current, err := queries.GetSeatMapVersionForUpdate(ctx, sqlc.GetSeatMapVersionForUpdateParams{OrganizationID: record.Input.OrganizationID.UUID(), SpaceID: record.Input.SpaceID.UUID(), ID: record.Input.SeatMapID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return venue.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("load seat map for publish: %w", err)
		}
		if current.Status != string(venue.SeatMapDraft) {
			return venue.ErrInvalidState
		}
		if current.Version != record.Input.Version {
			return venue.ErrVersionConflict
		}
		next, err := queries.NextSeatMapRevision(ctx, sqlc.NextSeatMapRevisionParams{OrganizationID: record.Input.OrganizationID.UUID(), SpaceID: record.Input.SpaceID.UUID()})
		if err != nil {
			return fmt.Errorf("get next seat-map revision: %w", err)
		}
		if err := queries.RetirePublishedSeatMaps(ctx, sqlc.RetirePublishedSeatMapsParams{OrganizationID: record.Input.OrganizationID.UUID(), SpaceID: record.Input.SpaceID.UUID(), UpdatedAt: record.OccurredAt.UTC()}); err != nil {
			return fmt.Errorf("retire previous seat maps: %w", err)
		}
		row, err := queries.PublishSeatMapVersion(ctx, sqlc.PublishSeatMapVersionParams{OrganizationID: record.Input.OrganizationID.UUID(), SpaceID: record.Input.SpaceID.UUID(), ID: record.Input.SeatMapID.UUID(), Revision: int64(next), PublishedAt: pgtype.Timestamptz{Time: record.OccurredAt.UTC(), Valid: true}, Version: record.Input.Version})
		if errors.Is(err, pgx.ErrNoRows) {
			return venue.ErrVersionConflict
		}
		if err != nil {
			return fmt.Errorf("publish seat map: %w", err)
		}
		result, err = mapSeatMap(row)
		if err != nil {
			return err
		}
		return repository.recordMutation(ctx, tx, record.Input.OrganizationID, record.Input.SeatMapID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, "seat_map.published", result.Version,
			map[string]any{"seat_map_id": result.ID.String(), "space_id": result.SpaceID.String(), "status": string(result.Status), "revision": result.Revision, "checksum": result.Checksum}, record.OccurredAt)
	})
	return result, err
}

func (repository *Repository) CloneSeatMap(ctx context.Context, record venue.CloneSeatMapRecord) (venue.SeatMap, error) {
	var result venue.SeatMap
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		space, err := queries.LockSpace(ctx, sqlc.LockSpaceParams{OrganizationID: record.OrganizationID.UUID(), ID: record.SpaceID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return venue.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock space for seat-map clone: %w", err)
		}
		if space.Status == string(venue.StatusArchived) || space.InventoryMode != string(venue.InventoryAssigned) {
			return venue.ErrInvalidState
		}
		row, err := queries.CloneSeatMapVersion(ctx, sqlc.CloneSeatMapVersionParams{ID: record.CloneID.UUID(), OrganizationID: record.OrganizationID.UUID(), SpaceID: record.SpaceID.UUID(), Name: record.Name, CreatedAt: record.OccurredAt.UTC(), ID_2: record.SeatMapID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return venue.ErrInvalidState
		}
		if err != nil {
			return fmt.Errorf("clone seat map: %w", err)
		}
		result, err = mapSeatMap(row)
		if err != nil {
			return err
		}
		return repository.recordMutation(ctx, tx, record.OrganizationID, record.CloneID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, "seat_map.cloned", 1,
			map[string]any{"seat_map_id": record.CloneID.String(), "source_seat_map_id": record.SeatMapID.String(), "space_id": record.SpaceID.String(), "status": string(result.Status)}, record.OccurredAt)
	})
	return result, err
}

func mapSeatMap(row sqlc.SeatMapVersion) (venue.SeatMap, error) {
	organizationID, _ := identifier.FromUUID(row.OrganizationID)
	spaceID, _ := identifier.FromUUID(row.SpaceID)
	seatMapID, _ := identifier.FromUUID(row.ID)
	var content venue.SeatMapContent
	if err := json.Unmarshal(row.Content, &content); err != nil {
		return venue.SeatMap{}, fmt.Errorf("decode seat-map content: %w", err)
	}
	checksum := hex.EncodeToString(row.Checksum)
	result := venue.SeatMap{ID: seatMapID, OrganizationID: organizationID, SpaceID: spaceID, Name: row.Name, Status: venue.SeatMapStatus(row.Status), Revision: row.Revision, Checksum: checksum, SeatCount: row.SeatCount, SellableCount: row.SellableCount, Content: content, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if row.PublishedAt.Valid {
		value := row.PublishedAt.Time.UTC()
		result.PublishedAt = &value
	}
	return result, nil
}
