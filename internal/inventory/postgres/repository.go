// Package postgres persists session inventory holds.
package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/inventory"
	"github.com/HK9750/venueos/internal/inventory/postgres/sqlc"
	"github.com/HK9750/venueos/internal/platform/identifier"
	platformpostgres "github.com/HK9750/venueos/internal/platform/postgres"
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

func (repository *Repository) CreateGAHold(ctx context.Context, record inventory.CreateGAHoldRecord) (inventory.Hold, error) {
	var result inventory.Hold
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		sessionRow, err := queries.LockSessionForGAHold(ctx, sqlc.LockSessionForGAHoldParams{OrganizationID: record.Hold.OrganizationID.UUID(), ID: record.Hold.SessionID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return inventory.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock session for hold: %w", err)
		}
		if sessionRow.Status == "cancelled" || sessionRow.Status == "completed" || sessionRow.Status == "sales_closed" {
			return inventory.ErrInvalidState
		}
		if sessionRow.InventoryMode != "general_admission" {
			return inventory.ErrInvalidState
		}
		_, err = queries.UpdateGAPoolForHold(ctx, sqlc.UpdateGAPoolForHoldParams{Quantity: record.Item.Quantity, UpdatedAt: record.Hold.UpdatedAt, OrganizationID: record.Hold.OrganizationID.UUID(), SessionID: record.Hold.SessionID.UUID(), PoolID: record.Item.PoolID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return inventory.ErrInsufficient
		}
		if err != nil {
			return fmt.Errorf("reserve general admission inventory: %w", err)
		}
		holdRow, err := queries.InsertHold(ctx, sqlc.InsertHoldParams{ID: record.Hold.ID.UUID(), OrganizationID: record.Hold.OrganizationID.UUID(), SessionID: record.Hold.SessionID.UUID(), OwnerTokenHash: record.Hold.OwnerTokenHash[:], OwnerUserID: nullableUUID(record.Hold.OwnerUserID), ChannelID: nullableUUID(record.Hold.ChannelID), Currency: record.Hold.Currency, State: string(record.Hold.State), ExpiresAt: record.Hold.ExpiresAt, RenewalCount: record.Hold.RenewalCount, Version: record.Hold.Version, CreatedAt: record.Hold.CreatedAt})
		if err != nil {
			return fmt.Errorf("insert hold: %w", err)
		}
		itemRow, err := queries.InsertHoldItem(ctx, sqlc.InsertHoldItemParams{ID: record.Item.ID.UUID(), OrganizationID: record.Item.OrganizationID.UUID(), HoldID: record.Item.HoldID.UUID(), SessionID: record.Item.SessionID.UUID(), PoolID: requiredPoolUUID(record.Item.PoolID.UUID()), SeatID: pgtype.UUID{}, Quantity: record.Item.Quantity, CreatedAt: record.Item.CreatedAt})
		if err != nil {
			return fmt.Errorf("insert hold item: %w", err)
		}
		revision, err := queries.IncrementSessionInventoryRevision(ctx, sqlc.IncrementSessionInventoryRevisionParams{UpdatedAt: record.Hold.UpdatedAt, OrganizationID: record.Hold.OrganizationID.UUID(), SessionID: record.Hold.SessionID.UUID()})
		if err != nil {
			return fmt.Errorf("increment inventory revision: %w", err)
		}
		result = mapHold(holdRow)
		_ = itemRow
		if err := repository.recordMutation(ctx, tx, record, result, "hold.created", record.Item.Quantity, record.Item.PoolID); err != nil {
			return err
		}
		return repository.recordAvailabilityEvent(ctx, tx, result.OrganizationID, result.SessionID, revision, "general_admission", result.CreatedAt)
	})
	return result, err
}

func (repository *Repository) CreateReservedSeatHold(ctx context.Context, record inventory.CreateReservedSeatHoldRecord) (inventory.Hold, error) {
	var result inventory.Hold
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		sessionRow, err := queries.LockSessionForGAHold(ctx, sqlc.LockSessionForGAHoldParams{OrganizationID: record.Hold.OrganizationID.UUID(), ID: record.Hold.SessionID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return inventory.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock session for reserved-seat hold: %w", err)
		}
		if sessionRow.Status == "cancelled" || sessionRow.Status == "completed" || sessionRow.Status == "sales_closed" {
			return inventory.ErrInvalidState
		}
		if sessionRow.InventoryMode != "assigned_seating" {
			return inventory.ErrInvalidState
		}
		seatIDs := make([]uuid.UUID, 0, len(record.Items))
		itemsBySeat := make(map[uuid.UUID]inventory.HoldItem, len(record.Items))
		for _, item := range record.Items {
			if item.SeatID == nil || item.SeatID.IsZero() || item.OrganizationID != record.Hold.OrganizationID || item.SessionID != record.Hold.SessionID {
				return inventory.ErrNotFound
			}
			seatID := item.SeatID.UUID()
			if _, exists := itemsBySeat[seatID]; exists {
				return inventory.ErrHoldConflict
			}
			itemsBySeat[seatID] = item
			seatIDs = append(seatIDs, seatID)
		}
		if len(seatIDs) == 0 {
			return inventory.ErrHoldConflict
		}
		sort.Slice(seatIDs, func(left, right int) bool { return bytes.Compare(seatIDs[left][:], seatIDs[right][:]) < 0 })
		seats, err := queries.LockSessionSeatsForHold(ctx, sqlc.LockSessionSeatsForHoldParams{OrganizationID: record.Hold.OrganizationID.UUID(), SessionID: record.Hold.SessionID.UUID(), SeatIds: seatIDs})
		if err != nil {
			return fmt.Errorf("lock requested session seats: %w", err)
		}
		if len(seats) != len(seatIDs) {
			return inventory.ErrNotFound
		}
		for _, seat := range seats {
			if !seat.Sellable || seat.State != "available" {
				return inventory.ErrInsufficient
			}
		}

		holdRow, err := queries.InsertHold(ctx, sqlc.InsertHoldParams{ID: record.Hold.ID.UUID(), OrganizationID: record.Hold.OrganizationID.UUID(), SessionID: record.Hold.SessionID.UUID(), OwnerTokenHash: record.Hold.OwnerTokenHash[:], OwnerUserID: nullableUUID(record.Hold.OwnerUserID), ChannelID: nullableUUID(record.Hold.ChannelID), Currency: record.Hold.Currency, State: string(record.Hold.State), ExpiresAt: record.Hold.ExpiresAt, RenewalCount: record.Hold.RenewalCount, Version: record.Hold.Version, CreatedAt: record.Hold.CreatedAt})
		if err != nil {
			return fmt.Errorf("insert reserved-seat hold: %w", err)
		}
		for _, seat := range seats {
			item := itemsBySeat[seat.ID]
			if _, err := queries.InsertHoldItem(ctx, sqlc.InsertHoldItemParams{ID: item.ID.UUID(), OrganizationID: item.OrganizationID.UUID(), HoldID: item.HoldID.UUID(), SessionID: item.SessionID.UUID(), PoolID: pgtype.UUID{}, SeatID: requiredSeatUUID(seat.ID), Quantity: 1, CreatedAt: item.CreatedAt}); err != nil {
				return fmt.Errorf("insert reserved-seat hold item: %w", err)
			}
			rows, err := queries.HoldSessionSeat(ctx, sqlc.HoldSessionSeatParams{HoldID: requiredSeatUUID(record.Hold.ID.UUID()), HoldExpiresAt: pgtype.Timestamptz{Time: record.Hold.ExpiresAt.UTC(), Valid: true}, UpdatedAt: record.Hold.UpdatedAt.UTC(), OrganizationID: record.Hold.OrganizationID.UUID(), SessionID: record.Hold.SessionID.UUID(), SeatID: seat.ID})
			if err != nil {
				return fmt.Errorf("hold session seat: %w", err)
			}
			if rows != 1 {
				return inventory.ErrHoldConflict
			}
		}
		revision, err := queries.IncrementSessionInventoryRevision(ctx, sqlc.IncrementSessionInventoryRevisionParams{UpdatedAt: record.Hold.UpdatedAt.UTC(), OrganizationID: record.Hold.OrganizationID.UUID(), SessionID: record.Hold.SessionID.UUID()})
		if err != nil {
			return fmt.Errorf("increment inventory revision after reserved-seat hold: %w", err)
		}
		result = mapHold(holdRow)
		if err := repository.recordReservedMutation(ctx, tx, record, result, len(seats)); err != nil {
			return err
		}
		return repository.recordAvailabilityEvent(ctx, tx, result.OrganizationID, result.SessionID, revision, "assigned_seating", result.CreatedAt)
	})
	return result, err
}

func (repository *Repository) ReleaseHold(ctx context.Context, record inventory.ReleaseHoldRecord) (inventory.Hold, error) {
	var result inventory.Hold
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		holdRow, err := queries.LockHoldForOwner(ctx, sqlc.LockHoldForOwnerParams{OrganizationID: record.OrganizationID.UUID(), ID: record.HoldID.UUID(), OwnerTokenHash: record.OwnerTokenHash[:]})
		if errors.Is(err, pgx.ErrNoRows) {
			return inventory.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock hold for release: %w", err)
		}
		result = mapHold(holdRow)
		if result.State != inventory.HoldActive {
			return nil
		}
		databaseNow, err := queries.GetDatabaseTime(ctx)
		if err != nil {
			return fmt.Errorf("get database time for hold release: %w", err)
		}
		holdExpired := !holdRow.ExpiresAt.After(databaseNow)
		items, err := queries.ListHoldItems(ctx, sqlc.ListHoldItemsParams{OrganizationID: record.OrganizationID.UUID(), HoldID: record.HoldID.UUID()})
		if err != nil {
			return fmt.Errorf("list hold items: %w", err)
		}
		inventoryKind := holdItemsKind(items)
		quantity := int64(0)
		for _, item := range items {
			quantity += item.Quantity
			if item.SeatID.Valid {
				rows, err := queries.ReleaseSessionSeat(ctx, sqlc.ReleaseSessionSeatParams{UpdatedAt: databaseNow.UTC(), OrganizationID: record.OrganizationID.UUID(), SessionID: item.SessionID, SeatID: uuid.UUID(item.SeatID.Bytes), HoldID: requiredSeatUUID(record.HoldID.UUID())})
				if err != nil {
					return fmt.Errorf("release reserved session seat: %w", err)
				}
				if rows != 1 {
					return inventory.ErrHoldConflict
				}
				continue
			}
			poolID, err := poolUUID(item.PoolID)
			if err != nil {
				return err
			}
			rows, err := queries.ReleaseGAPool(ctx, sqlc.ReleaseGAPoolParams{Quantity: item.Quantity, UpdatedAt: databaseNow.UTC(), OrganizationID: record.OrganizationID.UUID(), SessionID: item.SessionID, PoolID: poolID})
			if err != nil {
				return fmt.Errorf("release general admission inventory: %w", err)
			}
			if rows != 1 {
				return inventory.ErrHoldConflict
			}
		}
		nextState := inventory.HoldReleased
		if holdExpired {
			nextState = inventory.HoldExpired
		}
		releasedRow, err := queries.UpdateHoldState(ctx, sqlc.UpdateHoldStateParams{State: string(nextState), UpdatedAt: databaseNow.UTC(), OrganizationID: record.OrganizationID.UUID(), ID: record.HoldID.UUID()})
		if err != nil {
			return fmt.Errorf("release hold: %w", err)
		}
		result = mapHold(releasedRow)
		revision, err := queries.IncrementSessionInventoryRevision(ctx, sqlc.IncrementSessionInventoryRevisionParams{UpdatedAt: result.UpdatedAt, OrganizationID: record.OrganizationID.UUID(), SessionID: result.SessionID.UUID()})
		if err != nil {
			return fmt.Errorf("increment inventory revision after release: %w", err)
		}
		if holdExpired {
			if err := repository.recordExpirationMutation(ctx, tx, inventory.ExpireDueRecord{Now: databaseNow.UTC(), Limit: 1, ActorType: record.ActorType, ActorID: record.ActorID}, sqlc.ExpireHoldRow{ID: result.ID.UUID(), OrganizationID: result.OrganizationID.UUID(), SessionID: result.SessionID.UUID(), Version: result.Version, UpdatedAt: result.UpdatedAt}, quantity); err != nil {
				return err
			}
		} else if err := repository.recordReleaseMutation(ctx, tx, record, result); err != nil {
			return err
		}
		return repository.recordAvailabilityEvent(ctx, tx, result.OrganizationID, result.SessionID, revision, inventoryKind, result.UpdatedAt)
	})
	return result, err
}

func (repository *Repository) RenewHold(ctx context.Context, record inventory.RenewHoldRecord) (inventory.Hold, error) {
	var result inventory.Hold
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		currentRow, err := queries.LockHoldForOwner(ctx, sqlc.LockHoldForOwnerParams{OrganizationID: record.OrganizationID.UUID(), ID: record.HoldID.UUID(), OwnerTokenHash: record.OwnerTokenHash[:]})
		if errors.Is(err, pgx.ErrNoRows) {
			return inventory.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock hold for renewal: %w", err)
		}
		current := mapHold(currentRow)
		if current.Version != record.ExpectedVersion {
			return inventory.ErrVersionConflict
		}
		if current.State != inventory.HoldActive {
			return inventory.ErrInvalidState
		}
		databaseNow, err := queries.GetDatabaseTime(ctx)
		if err != nil {
			return fmt.Errorf("get database time for hold renewal: %w", err)
		}
		if !current.ExpiresAt.After(databaseNow) || current.RenewalCount >= inventory.MaxHoldRenewals {
			return inventory.ErrInvalidState
		}
		row, err := queries.RenewHold(ctx, sqlc.RenewHoldParams{NewExpiresAt: databaseNow.UTC().Add(inventory.DefaultHoldDuration), UpdatedAt: databaseNow.UTC(), OrganizationID: record.OrganizationID.UUID(), ID: record.HoldID.UUID(), ExpectedVersion: record.ExpectedVersion, DatabaseNow: databaseNow.UTC()})
		if errors.Is(err, pgx.ErrNoRows) {
			return inventory.ErrHoldConflict
		}
		if err != nil {
			return fmt.Errorf("renew hold: %w", err)
		}
		result = mapHold(row)
		return repository.recordRenewalMutation(ctx, tx, record, result)
	})
	return result, err
}

func (repository *Repository) ModifyHold(ctx context.Context, record inventory.ModifyHoldRecord) (inventory.Hold, error) {
	var result inventory.Hold
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		currentRow, err := queries.LockHoldForOwner(ctx, sqlc.LockHoldForOwnerParams{OrganizationID: record.OrganizationID.UUID(), ID: record.HoldID.UUID(), OwnerTokenHash: record.OwnerTokenHash[:]})
		if errors.Is(err, pgx.ErrNoRows) {
			return inventory.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock hold for modification: %w", err)
		}
		current := mapHold(currentRow)
		if current.Version != record.ExpectedVersion {
			return inventory.ErrVersionConflict
		}
		if current.State != inventory.HoldActive {
			return inventory.ErrInvalidState
		}
		if len(record.Items) == 0 {
			return inventory.ErrHoldConflict
		}
		databaseNow, err := queries.GetDatabaseTime(ctx)
		if err != nil {
			return fmt.Errorf("get database time for hold modification: %w", err)
		}
		if !current.ExpiresAt.After(databaseNow) {
			return inventory.ErrInvalidState
		}
		sessionRow, err := queries.LockSessionForGAHold(ctx, sqlc.LockSessionForGAHoldParams{OrganizationID: record.OrganizationID.UUID(), ID: current.SessionID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return inventory.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock session for hold modification: %w", err)
		}
		if sessionRow.Status == "cancelled" || sessionRow.Status == "completed" || sessionRow.Status == "sales_closed" {
			return inventory.ErrInvalidState
		}
		reserved := record.Items[0].SeatID != nil
		for _, item := range record.Items {
			if (item.SeatID != nil) != reserved || item.OrganizationID != record.OrganizationID {
				return inventory.ErrHoldConflict
			}
		}
		if (reserved && sessionRow.InventoryMode != "assigned_seating") || (!reserved && sessionRow.InventoryMode != "general_admission") {
			return inventory.ErrInvalidState
		}
		existing, err := queries.ListHoldItems(ctx, sqlc.ListHoldItemsParams{OrganizationID: record.OrganizationID.UUID(), HoldID: record.HoldID.UUID()})
		if err != nil {
			return fmt.Errorf("list hold items for modification: %w", err)
		}
		for _, item := range existing {
			if item.SeatID.Valid {
				rows, releaseErr := queries.ReleaseSessionSeat(ctx, sqlc.ReleaseSessionSeatParams{UpdatedAt: databaseNow.UTC(), OrganizationID: record.OrganizationID.UUID(), SessionID: item.SessionID, SeatID: uuid.UUID(item.SeatID.Bytes), HoldID: requiredSeatUUID(record.HoldID.UUID())})
				if releaseErr != nil {
					return fmt.Errorf("release modified reserved session seat: %w", releaseErr)
				}
				if rows != 1 {
					return inventory.ErrHoldConflict
				}
				continue
			}
			poolID, poolErr := poolUUID(item.PoolID)
			if poolErr != nil {
				return poolErr
			}
			rows, releaseErr := queries.ReleaseGAPool(ctx, sqlc.ReleaseGAPoolParams{Quantity: item.Quantity, UpdatedAt: databaseNow.UTC(), OrganizationID: record.OrganizationID.UUID(), SessionID: item.SessionID, PoolID: poolID})
			if releaseErr != nil {
				return fmt.Errorf("release modified general admission inventory: %w", releaseErr)
			}
			if rows != 1 {
				return inventory.ErrHoldConflict
			}
		}
		if _, err := queries.DeleteHoldItems(ctx, sqlc.DeleteHoldItemsParams{OrganizationID: record.OrganizationID.UUID(), HoldID: record.HoldID.UUID()}); err != nil {
			return fmt.Errorf("delete previous hold items: %w", err)
		}
		quantity := int64(0)
		if reserved {
			seatIDs := make([]uuid.UUID, 0, len(record.Items))
			seen := make(map[uuid.UUID]struct{}, len(record.Items))
			for _, item := range record.Items {
				seatID := item.SeatID.UUID()
				if _, exists := seen[seatID]; exists {
					return inventory.ErrHoldConflict
				}
				seen[seatID] = struct{}{}
				seatIDs = append(seatIDs, seatID)
			}
			sort.Slice(seatIDs, func(left, right int) bool { return bytes.Compare(seatIDs[left][:], seatIDs[right][:]) < 0 })
			seats, err := queries.LockSessionSeatsForHold(ctx, sqlc.LockSessionSeatsForHoldParams{OrganizationID: record.OrganizationID.UUID(), SessionID: current.SessionID.UUID(), SeatIds: seatIDs})
			if err != nil {
				return fmt.Errorf("lock replacement session seats: %w", err)
			}
			if len(seats) != len(seatIDs) {
				return inventory.ErrNotFound
			}
			for _, seat := range seats {
				if !seat.Sellable || seat.State != "available" {
					return inventory.ErrInsufficient
				}
			}
			for _, seat := range seats {
				item, found := findSeatItem(record.Items, seat.ID)
				if !found {
					return inventory.ErrHoldConflict
				}
				if _, err := queries.InsertHoldItem(ctx, sqlc.InsertHoldItemParams{ID: item.ID.UUID(), OrganizationID: record.OrganizationID.UUID(), HoldID: record.HoldID.UUID(), SessionID: current.SessionID.UUID(), PoolID: pgtype.UUID{}, SeatID: requiredSeatUUID(seat.ID), Quantity: 1, CreatedAt: databaseNow.UTC()}); err != nil {
					return fmt.Errorf("insert replacement reserved hold item: %w", err)
				}
				rows, err := queries.HoldSessionSeat(ctx, sqlc.HoldSessionSeatParams{HoldID: requiredSeatUUID(record.HoldID.UUID()), HoldExpiresAt: pgtype.Timestamptz{Time: current.ExpiresAt.UTC(), Valid: true}, UpdatedAt: databaseNow.UTC(), OrganizationID: record.OrganizationID.UUID(), SessionID: current.SessionID.UUID(), SeatID: seat.ID})
				if err != nil {
					return fmt.Errorf("hold replacement session seat: %w", err)
				}
				if rows != 1 {
					return inventory.ErrHoldConflict
				}
				quantity++
			}
		} else {
			item := record.Items[0]
			if item.PoolID.IsZero() || item.Quantity < 1 || item.Quantity > inventory.MaxHoldQuantity {
				return inventory.ErrHoldConflict
			}
			if _, err := queries.UpdateGAPoolForHold(ctx, sqlc.UpdateGAPoolForHoldParams{Quantity: item.Quantity, UpdatedAt: databaseNow.UTC(), OrganizationID: record.OrganizationID.UUID(), SessionID: current.SessionID.UUID(), PoolID: item.PoolID.UUID()}); errors.Is(err, pgx.ErrNoRows) {
				return inventory.ErrInsufficient
			} else if err != nil {
				return fmt.Errorf("reserve replacement general admission inventory: %w", err)
			}
			if _, err := queries.InsertHoldItem(ctx, sqlc.InsertHoldItemParams{ID: item.ID.UUID(), OrganizationID: record.OrganizationID.UUID(), HoldID: record.HoldID.UUID(), SessionID: current.SessionID.UUID(), PoolID: requiredPoolUUID(item.PoolID.UUID()), SeatID: pgtype.UUID{}, Quantity: item.Quantity, CreatedAt: databaseNow.UTC()}); err != nil {
				return fmt.Errorf("insert replacement general admission hold item: %w", err)
			}
			quantity = item.Quantity
		}
		updatedRow, err := queries.UpdateHoldForModification(ctx, sqlc.UpdateHoldForModificationParams{UpdatedAt: databaseNow.UTC(), OrganizationID: record.OrganizationID.UUID(), ID: record.HoldID.UUID(), ExpectedVersion: record.ExpectedVersion})
		if errors.Is(err, pgx.ErrNoRows) {
			return inventory.ErrVersionConflict
		}
		if err != nil {
			return fmt.Errorf("update modified hold: %w", err)
		}
		result = mapHold(updatedRow)
		revision, err := queries.IncrementSessionInventoryRevision(ctx, sqlc.IncrementSessionInventoryRevisionParams{UpdatedAt: result.UpdatedAt, OrganizationID: record.OrganizationID.UUID(), SessionID: current.SessionID.UUID()})
		if err != nil {
			return fmt.Errorf("increment inventory revision after modification: %w", err)
		}
		kind := "general_admission"
		if reserved {
			kind = "assigned_seating"
		}
		if err := repository.recordModificationMutation(ctx, tx, record, result, kind, quantity); err != nil {
			return err
		}
		return repository.recordAvailabilityEvent(ctx, tx, result.OrganizationID, result.SessionID, revision, kind, result.UpdatedAt)
	})
	return result, err
}

func (repository *Repository) ExpireDue(ctx context.Context, record inventory.ExpireDueRecord) (int64, error) {
	if record.Now.IsZero() || record.Limit < 1 || record.Limit > 100 {
		return 0, fmt.Errorf("invalid hold expiration record")
	}
	var expired int64
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		holds, err := queries.LockDueHolds(ctx, sqlc.LockDueHoldsParams{Now: record.Now.UTC(), BatchLimit: record.Limit})
		if err != nil {
			return fmt.Errorf("lock due holds: %w", err)
		}
		if len(holds) == 0 {
			return nil
		}

		// Hold expiry locks sessions before pool rows, matching hold creation's
		// session-then-pool order. The deterministic order prevents two expiry
		// workers from deadlocking when a batch spans multiple sessions.
		type sessionLock struct {
			organizationID uuid.UUID
			sessionID      uuid.UUID
		}
		sessionLocks := make(map[string]sessionLock, len(holds))
		for _, hold := range holds {
			key := hold.OrganizationID.String() + ":" + hold.SessionID.String()
			sessionLocks[key] = sessionLock{organizationID: hold.OrganizationID, sessionID: hold.SessionID}
		}
		keys := make([]string, 0, len(sessionLocks))
		for key := range sessionLocks {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			lock := sessionLocks[key]
			if _, err := queries.LockSessionForGAHold(ctx, sqlc.LockSessionForGAHoldParams{OrganizationID: lock.organizationID, ID: lock.sessionID}); err != nil {
				return fmt.Errorf("lock session for hold expiration: %w", err)
			}
		}

		for _, hold := range holds {
			items, err := queries.ListHoldItems(ctx, sqlc.ListHoldItemsParams{OrganizationID: hold.OrganizationID, HoldID: hold.ID})
			if err != nil {
				return fmt.Errorf("list expired hold items: %w", err)
			}
			inventoryKind := holdItemsKind(items)
			var quantity int64
			for _, item := range items {
				if item.SeatID.Valid {
					rows, err := queries.ReleaseSessionSeat(ctx, sqlc.ReleaseSessionSeatParams{UpdatedAt: record.Now.UTC(), OrganizationID: hold.OrganizationID, SessionID: item.SessionID, SeatID: uuid.UUID(item.SeatID.Bytes), HoldID: requiredSeatUUID(hold.ID)})
					if err != nil {
						return fmt.Errorf("release expired reserved session seat: %w", err)
					}
					if rows != 1 {
						return inventory.ErrHoldConflict
					}
					quantity += item.Quantity
					continue
				}
				poolID, err := poolUUID(item.PoolID)
				if err != nil {
					return err
				}
				rows, err := queries.ReleaseGAPool(ctx, sqlc.ReleaseGAPoolParams{Quantity: item.Quantity, UpdatedAt: record.Now.UTC(), OrganizationID: hold.OrganizationID, SessionID: item.SessionID, PoolID: poolID})
				if err != nil {
					return fmt.Errorf("release expired general admission inventory: %w", err)
				}
				if rows != 1 {
					return inventory.ErrHoldConflict
				}
				quantity += item.Quantity
			}

			expiredHold, err := queries.ExpireHold(ctx, sqlc.ExpireHoldParams{UpdatedAt: record.Now.UTC(), OrganizationID: hold.OrganizationID, ID: hold.ID})
			if errors.Is(err, pgx.ErrNoRows) {
				return inventory.ErrHoldConflict
			}
			if err != nil {
				return fmt.Errorf("expire hold: %w", err)
			}
			revision, err := queries.IncrementSessionInventoryRevision(ctx, sqlc.IncrementSessionInventoryRevisionParams{UpdatedAt: expiredHold.UpdatedAt, OrganizationID: expiredHold.OrganizationID, SessionID: expiredHold.SessionID})
			if err != nil {
				return fmt.Errorf("increment inventory revision after expiration: %w", err)
			}
			if err := repository.recordExpirationMutation(ctx, tx, record, expiredHold, quantity); err != nil {
				return err
			}
			if err := repository.recordAvailabilityEvent(ctx, tx, mustIdentifier(expiredHold.OrganizationID), mustIdentifier(expiredHold.SessionID), revision, inventoryKind, expiredHold.UpdatedAt); err != nil {
				return err
			}
			expired++
		}
		return nil
	})
	return expired, err
}

func (repository *Repository) GetAvailabilitySnapshot(ctx context.Context, organizationID, sessionID identifier.ID) (inventory.AvailabilitySnapshot, error) {
	var result inventory.AvailabilitySnapshot
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		sessionRow, err := queries.GetAvailabilitySession(ctx, sqlc.GetAvailabilitySessionParams{OrganizationID: organizationID.UUID(), ID: sessionID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return inventory.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get availability session: %w", err)
		}
		seatRows, err := queries.ListSessionSeatAvailability(ctx, sqlc.ListSessionSeatAvailabilityParams{OrganizationID: organizationID.UUID(), SessionID: sessionID.UUID()})
		if err != nil {
			return fmt.Errorf("list session seat availability: %w", err)
		}
		poolRows, err := queries.ListSessionGAPoolAvailability(ctx, sqlc.ListSessionGAPoolAvailabilityParams{OrganizationID: organizationID.UUID(), SessionID: sessionID.UUID()})
		if err != nil {
			return fmt.Errorf("list session general-admission availability: %w", err)
		}
		result = inventory.AvailabilitySnapshot{OrganizationID: organizationID, SessionID: sessionID, InventoryMode: sessionRow.InventoryMode, SessionStatus: sessionRow.Status, Revision: sessionRow.InventoryRevision, ServerTime: sessionRow.ServerTime, Seats: make([]inventory.SeatAvailability, 0, len(seatRows)), GAPools: make([]inventory.GAPoolAvailability, 0, len(poolRows))}
		for _, row := range seatRows {
			result.Seats = append(result.Seats, inventory.SeatAvailability{ID: mustIdentifier(row.ID), SeatKey: row.SeatKey, Label: row.Label, Category: row.Category, Sellable: row.Sellable, Wheelchair: row.Wheelchair, State: row.State, Version: row.Version})
		}
		for _, row := range poolRows {
			available := row.SellableCapacity - row.SoldQty - row.HeldQty - row.KilledQty - row.CompedQty
			if available < 0 {
				return fmt.Errorf("inventory invariant violated for session pool %s", row.ID)
			}
			result.GAPools = append(result.GAPools, inventory.GAPoolAvailability{ID: mustIdentifier(row.ID), Slug: row.Slug, SellableCapacity: row.SellableCapacity, SoldQuantity: row.SoldQty, HeldQuantity: row.HeldQty, KilledQuantity: row.KilledQty, CompedQuantity: row.CompedQty, Available: available, Version: row.Version})
		}
		return nil
	})
	return result, err
}

func (repository *Repository) ListAvailabilityEvents(ctx context.Context, organizationID, sessionID identifier.ID, after int64, limit int32) (inventory.AvailabilityReplayPage, error) {
	if organizationID.IsZero() || sessionID.IsZero() {
		return inventory.AvailabilityReplayPage{}, inventory.ErrNotFound
	}
	if after < 0 {
		return inventory.AvailabilityReplayPage{}, fmt.Errorf("availability replay sequence cannot be negative")
	}
	if limit < 1 || limit > 100 {
		return inventory.AvailabilityReplayPage{}, fmt.Errorf("availability replay limit must be between 1 and 100")
	}

	var result inventory.AvailabilityReplayPage
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		sessionRow, err := queries.GetAvailabilitySession(ctx, sqlc.GetAvailabilitySessionParams{OrganizationID: organizationID.UUID(), ID: sessionID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return inventory.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("get availability session for replay: %w", err)
		}
		bounds, err := queries.GetAvailabilityEventBounds(ctx, sqlc.GetAvailabilityEventBoundsParams{OrganizationID: organizationID.UUID(), SessionID: sessionID.UUID()})
		if err != nil {
			return fmt.Errorf("get availability event bounds: %w", err)
		}
		if bounds.FirstSequence > 0 && after < bounds.FirstSequence-1 {
			return inventory.ErrReplayUnavailable
		}
		rows, err := queries.ListAvailabilityEventsAfter(ctx, sqlc.ListAvailabilityEventsAfterParams{
			OrganizationID: organizationID.UUID(),
			SessionID:      sessionID.UUID(),
			AfterSequence:  after,
			PageLimit:      limit + 1,
		})
		if err != nil {
			return fmt.Errorf("list availability events: %w", err)
		}
		result = inventory.AvailabilityReplayPage{Events: make([]inventory.AvailabilityEvent, 0, len(rows)), CurrentRevision: sessionRow.InventoryRevision}
		if len(rows) > int(limit) {
			result.HasMore = true
			rows = rows[:limit]
		}
		for _, row := range rows {
			result.Events = append(result.Events, inventory.AvailabilityEvent{
				ID:            mustIdentifier(row.ID),
				SessionID:     mustIdentifier(row.SessionID),
				Sequence:      row.Sequence,
				EventType:     row.EventType,
				SchemaVersion: row.SchemaVersion,
				Payload:       append([]byte(nil), row.Payload...),
				OccurredAt:    row.OccurredAt.UTC(),
			})
		}
		return nil
	})
	return result, err
}

func (repository *Repository) recordMutation(ctx context.Context, tx pgx.Tx, record inventory.CreateGAHoldRecord, result inventory.Hold, action string, quantity int64, poolID identifier.ID) error {
	platform := repository.platform.WithTx(tx)
	data, err := json.Marshal(map[string]any{"hold_id": result.ID.String(), "session_id": result.SessionID.String(), "quantity": quantity, "pool_id": poolID.String(), "currency": result.Currency, "state": string(result.State)})
	if err != nil {
		return fmt.Errorf("marshal hold audit data: %w", err)
	}
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: record.AuditID, OrganizationID: result.OrganizationID, ActorType: record.ActorType, ActorID: record.ActorID, Action: action, SubjectType: "hold", SubjectID: result.ID.String(), Result: "success", AfterData: data, OccurredAt: result.CreatedAt}); err != nil {
		return err
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: record.OutboxID, OrganizationID: result.OrganizationID, AggregateType: "hold", AggregateID: result.ID, AggregateVersion: result.Version, EventType: action, SchemaVersion: 1, Payload: data, AvailableAt: result.CreatedAt, OccurredAt: result.CreatedAt})
}

func (repository *Repository) recordReservedMutation(ctx context.Context, tx pgx.Tx, record inventory.CreateReservedSeatHoldRecord, result inventory.Hold, seatCount int) error {
	platform := repository.platform.WithTx(tx)
	data, err := json.Marshal(map[string]any{"hold_id": result.ID.String(), "session_id": result.SessionID.String(), "seat_count": seatCount, "state": string(result.State)})
	if err != nil {
		return fmt.Errorf("marshal reserved hold audit data: %w", err)
	}
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: record.AuditID, OrganizationID: result.OrganizationID, ActorType: record.ActorType, ActorID: record.ActorID, Action: "hold.created", SubjectType: "hold", SubjectID: result.ID.String(), Result: "success", AfterData: data, OccurredAt: result.CreatedAt}); err != nil {
		return err
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: record.OutboxID, OrganizationID: result.OrganizationID, AggregateType: "hold", AggregateID: result.ID, AggregateVersion: result.Version, EventType: "hold.created", SchemaVersion: 1, Payload: data, AvailableAt: result.CreatedAt, OccurredAt: result.CreatedAt})
}

func (repository *Repository) recordReleaseMutation(ctx context.Context, tx pgx.Tx, record inventory.ReleaseHoldRecord, result inventory.Hold) error {
	platform := repository.platform.WithTx(tx)
	data, err := json.Marshal(map[string]any{"hold_id": result.ID.String(), "session_id": result.SessionID.String(), "state": string(result.State)})
	if err != nil {
		return fmt.Errorf("marshal hold release audit data: %w", err)
	}
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: record.AuditID, OrganizationID: result.OrganizationID, ActorType: record.ActorType, ActorID: record.ActorID, Action: "hold.released", SubjectType: "hold", SubjectID: result.ID.String(), Result: "success", AfterData: data, OccurredAt: result.UpdatedAt}); err != nil {
		return err
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: record.OutboxID, OrganizationID: result.OrganizationID, AggregateType: "hold", AggregateID: result.ID, AggregateVersion: result.Version, EventType: "hold.released", SchemaVersion: 1, Payload: data, AvailableAt: result.UpdatedAt, OccurredAt: result.UpdatedAt})
}

func (repository *Repository) recordRenewalMutation(ctx context.Context, tx pgx.Tx, record inventory.RenewHoldRecord, result inventory.Hold) error {
	platform := repository.platform.WithTx(tx)
	data, err := json.Marshal(map[string]any{"hold_id": result.ID.String(), "session_id": result.SessionID.String(), "state": string(result.State), "renewal_count": result.RenewalCount, "expires_at": result.ExpiresAt.UTC()})
	if err != nil {
		return fmt.Errorf("marshal hold renewal audit data: %w", err)
	}
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: record.AuditID, OrganizationID: result.OrganizationID, ActorType: record.ActorType, ActorID: record.ActorID, Action: "hold.renewed", SubjectType: "hold", SubjectID: result.ID.String(), Result: "success", AfterData: data, OccurredAt: result.UpdatedAt}); err != nil {
		return err
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: record.OutboxID, OrganizationID: result.OrganizationID, AggregateType: "hold", AggregateID: result.ID, AggregateVersion: result.Version, EventType: "hold.renewed", SchemaVersion: 1, Payload: data, AvailableAt: result.UpdatedAt, OccurredAt: result.UpdatedAt})
}

func (repository *Repository) recordModificationMutation(ctx context.Context, tx pgx.Tx, record inventory.ModifyHoldRecord, result inventory.Hold, inventoryKind string, quantity int64) error {
	platform := repository.platform.WithTx(tx)
	data, err := json.Marshal(map[string]any{"hold_id": result.ID.String(), "session_id": result.SessionID.String(), "inventory_kind": inventoryKind, "quantity": quantity, "state": string(result.State)})
	if err != nil {
		return fmt.Errorf("marshal hold modification audit data: %w", err)
	}
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: record.AuditID, OrganizationID: result.OrganizationID, ActorType: record.ActorType, ActorID: record.ActorID, Action: "hold.modified", SubjectType: "hold", SubjectID: result.ID.String(), Result: "success", AfterData: data, OccurredAt: result.UpdatedAt}); err != nil {
		return err
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: record.OutboxID, OrganizationID: result.OrganizationID, AggregateType: "hold", AggregateID: result.ID, AggregateVersion: result.Version, EventType: "hold.modified", SchemaVersion: 1, Payload: data, AvailableAt: result.UpdatedAt, OccurredAt: result.UpdatedAt})
}

func (repository *Repository) recordExpirationMutation(ctx context.Context, tx pgx.Tx, record inventory.ExpireDueRecord, hold sqlc.ExpireHoldRow, quantity int64) error {
	auditID, err := identifier.New()
	if err != nil {
		return fmt.Errorf("create hold expiration audit ID: %w", err)
	}
	outboxID, err := identifier.New()
	if err != nil {
		return fmt.Errorf("create hold expiration outbox ID: %w", err)
	}
	organizationID := mustIdentifier(hold.OrganizationID)
	holdID := mustIdentifier(hold.ID)
	sessionID := mustIdentifier(hold.SessionID)
	platform := repository.platform.WithTx(tx)
	data, err := json.Marshal(map[string]any{"hold_id": holdID.String(), "session_id": sessionID.String(), "quantity": quantity, "state": string(inventory.HoldExpired)})
	if err != nil {
		return fmt.Errorf("marshal hold expiration audit data: %w", err)
	}
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: auditID, OrganizationID: organizationID, ActorType: record.ActorType, ActorID: record.ActorID, Action: "hold.expired", SubjectType: "hold", SubjectID: holdID.String(), Result: "success", AfterData: data, OccurredAt: hold.UpdatedAt}); err != nil {
		return err
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: outboxID, OrganizationID: organizationID, AggregateType: "hold", AggregateID: holdID, AggregateVersion: hold.Version, EventType: "hold.expired", SchemaVersion: 1, Payload: data, AvailableAt: hold.UpdatedAt, OccurredAt: hold.UpdatedAt})
}

func (repository *Repository) recordAvailabilityEvent(ctx context.Context, tx pgx.Tx, organizationID, sessionID identifier.ID, revision int64, inventoryKind string, occurredAt time.Time) error {
	eventID, err := identifier.New()
	if err != nil {
		return fmt.Errorf("create availability event ID: %w", err)
	}
	payload, err := json.Marshal(map[string]any{"session_id": sessionID.String(), "revision": revision, "inventory_kind": inventoryKind})
	if err != nil {
		return fmt.Errorf("marshal availability event: %w", err)
	}
	return repository.platform.WithTx(tx).InsertRealtimeEvent(ctx, platformpostgres.RealtimeEvent{
		ID: eventID, OrganizationID: organizationID, SessionID: sessionID, Topic: "availability", Sequence: revision,
		EventType: "availability.changed", SchemaVersion: 1, Payload: payload, OccurredAt: occurredAt.UTC(), RetentionExpiresAt: occurredAt.UTC().Add(24 * time.Hour),
	})
}

func holdItemsKind(items []sqlc.ListHoldItemsRow) string {
	for _, item := range items {
		if item.SeatID.Valid {
			return "assigned_seating"
		}
	}
	return "general_admission"
}

func findSeatItem(items []inventory.HoldItem, seatID uuid.UUID) (inventory.HoldItem, bool) {
	for _, item := range items {
		if item.SeatID != nil && item.SeatID.UUID() == seatID {
			return item, true
		}
	}
	return inventory.HoldItem{}, false
}

func mapHold(row sqlc.Hold) inventory.Hold {
	var hash [32]byte
	copy(hash[:], row.OwnerTokenHash)
	organizationID, _ := identifier.FromUUID(row.OrganizationID)
	sessionID, _ := identifier.FromUUID(row.SessionID)
	value := inventory.Hold{ID: mustIdentifier(row.ID), OrganizationID: organizationID, SessionID: sessionID, OwnerTokenHash: hash, Currency: row.Currency, State: inventory.HoldStatus(row.State), ExpiresAt: row.ExpiresAt, RenewalCount: row.RenewalCount, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if row.OwnerUserID.Valid {
		id, _ := identifier.FromUUID(uuid.UUID(row.OwnerUserID.Bytes))
		value.OwnerUserID = &id
	}
	if row.ChannelID.Valid {
		id, _ := identifier.FromUUID(uuid.UUID(row.ChannelID.Bytes))
		value.ChannelID = &id
	}
	return value
}

func mustIdentifier(value uuid.UUID) identifier.ID {
	id, _ := identifier.FromUUID(value)
	return id
}

func nullableUUID(value *identifier.ID) pgtype.UUID {
	if value == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: value.UUID(), Valid: true}
}

func requiredPoolUUID(value uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(value), Valid: true}
}

func requiredSeatUUID(value uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: [16]byte(value), Valid: true}
}

func poolUUID(value pgtype.UUID) (uuid.UUID, error) {
	if !value.Valid {
		return uuid.Nil, fmt.Errorf("hold item is missing a general-admission pool reference")
	}
	return uuid.UUID(value.Bytes), nil
}
