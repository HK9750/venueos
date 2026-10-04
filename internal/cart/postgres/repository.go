// Package postgres persists tenant-scoped carts and their commercial snapshots.
package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/HK9750/venueos/internal/cart"
	"github.com/HK9750/venueos/internal/cart/postgres/sqlc"
	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/money"
	platformpostgres "github.com/HK9750/venueos/internal/platform/postgres"
	"github.com/google/uuid"
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

func (repository *Repository) Create(ctx context.Context, record cart.CreateRecord) (cart.Cart, error) {
	if err := validateRecord(record.Cart); err != nil {
		return cart.Cart{}, err
	}
	var result cart.Cart
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		databaseNow, err := queries.GetDatabaseTime(ctx)
		if err != nil {
			return fmt.Errorf("get database time for cart: %w", err)
		}
		hold, err := queries.LockHoldForCart(ctx, sqlc.LockHoldForCartParams{OrganizationID: record.Cart.OrganizationID.UUID(), ID: record.Cart.HoldID.UUID(), OwnerTokenHash: record.Cart.OwnerTokenHash[:]})
		if errors.Is(err, pgx.ErrNoRows) {
			return cart.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock cart hold: %w", err)
		}
		if hold.SessionID != record.Cart.SessionID.UUID() || hold.Currency != record.Cart.Currency {
			return cart.ErrNotFound
		}
		if hold.State != string(cart.StatusActive) || !hold.ExpiresAt.After(databaseNow) {
			return cart.ErrHoldNotActive
		}
		row, err := queries.InsertCart(ctx, sqlc.InsertCartParams{
			ID: record.Cart.ID.UUID(), OrganizationID: record.Cart.OrganizationID.UUID(), SessionID: record.Cart.SessionID.UUID(), HoldID: record.Cart.HoldID.UUID(), OwnerTokenHash: record.Cart.OwnerTokenHash[:], OwnerUserID: nullableUUID(record.Cart.OwnerUserID), Currency: record.Cart.Currency, State: string(record.Cart.Status), QuoteSnapshot: record.Cart.QuoteSnapshot, QuoteSnapshotRaw: record.Cart.QuoteSnapshot, QuoteSha256: record.Cart.QuoteSHA256[:], Version: record.Cart.Version, CreatedAt: record.Cart.CreatedAt.UTC(),
		})
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "uq_carts_active_hold" {
				return cart.ErrActiveCartExists
			}
			return fmt.Errorf("insert cart: %w", err)
		}
		result, err = mapInsertCart(row)
		if err != nil {
			return err
		}
		return repository.recordMutation(ctx, tx, record.Cart.OrganizationID, result.ID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, "cart.created", result.Version, result, result.CreatedAt)
	})
	return result, err
}

func (repository *Repository) Get(ctx context.Context, organizationID, cartID identifier.ID, ownerTokenHash [32]byte) (cart.Cart, error) {
	if organizationID.IsZero() || cartID.IsZero() || ownerTokenHash == [32]byte{} {
		return cart.Cart{}, cart.ErrNotFound
	}
	row, err := repository.queries.GetCartForOwner(ctx, sqlc.GetCartForOwnerParams{OrganizationID: organizationID.UUID(), ID: cartID.UUID(), OwnerTokenHash: ownerTokenHash[:]})
	if errors.Is(err, pgx.ErrNoRows) {
		return cart.Cart{}, cart.ErrNotFound
	}
	if err != nil {
		return cart.Cart{}, fmt.Errorf("get cart: %w", err)
	}
	return mapGetCart(row)
}

func (repository *Repository) UpdateQuote(ctx context.Context, record cart.UpdateQuoteRecord) (cart.Cart, error) {
	if record.OrganizationID.IsZero() || record.CartID.IsZero() || record.OwnerTokenHash == [32]byte{} || record.ExpectedVersion < 1 {
		return cart.Cart{}, cart.ErrNotFound
	}
	var result cart.Cart
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		row, err := queries.LockCartForOwner(ctx, sqlc.LockCartForOwnerParams{OrganizationID: record.OrganizationID.UUID(), ID: record.CartID.UUID(), OwnerTokenHash: record.OwnerTokenHash[:]})
		if errors.Is(err, pgx.ErrNoRows) {
			return cart.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock cart: %w", err)
		}
		current, err := mapLockCart(row)
		if err != nil {
			return err
		}
		databaseNow, err := queries.GetDatabaseTime(ctx)
		if err != nil {
			return fmt.Errorf("get database time for cart update: %w", err)
		}
		hold, err := queries.LockHoldForCart(ctx, sqlc.LockHoldForCartParams{OrganizationID: record.OrganizationID.UUID(), ID: current.HoldID.UUID(), OwnerTokenHash: record.OwnerTokenHash[:]})
		if errors.Is(err, pgx.ErrNoRows) {
			return cart.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock hold for cart update: %w", err)
		}
		if hold.SessionID != current.SessionID.UUID() || hold.Currency != current.Currency {
			return cart.ErrNotFound
		}
		if hold.State != "active" || !hold.ExpiresAt.After(databaseNow) {
			return cart.ErrHoldNotActive
		}
		next, err := current.UpdateQuote(cart.UpdateQuoteInput{ExpectedVersion: record.ExpectedVersion, OwnerTokenHash: record.OwnerTokenHash, Quote: record.Quote, Now: databaseNow.UTC()})
		if err != nil {
			return err
		}
		updated, err := queries.UpdateCartQuote(ctx, sqlc.UpdateCartQuoteParams{QuoteSnapshot: next.QuoteSnapshot, QuoteSnapshotRaw: next.QuoteSnapshot, QuoteSha256: next.QuoteSHA256[:], UpdatedAt: next.UpdatedAt.UTC(), OrganizationID: record.OrganizationID.UUID(), ID: record.CartID.UUID(), OwnerTokenHash: record.OwnerTokenHash[:], Version: record.ExpectedVersion})
		if errors.Is(err, pgx.ErrNoRows) {
			return cart.ErrVersionConflict
		}
		if err != nil {
			return fmt.Errorf("update cart quote: %w", err)
		}
		result, err = mapUpdatedCart(updated)
		if err != nil {
			return err
		}
		return repository.recordMutation(ctx, tx, record.OrganizationID, result.ID, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, "cart.updated", result.Version, result, result.UpdatedAt)
	})
	return result, err
}

func (repository *Repository) recordMutation(ctx context.Context, tx pgx.Tx, organizationID, aggregateID, auditID, outboxID identifier.ID, actorType, actorID, action string, version int64, value cart.Cart, occurredAt time.Time) error {
	after := map[string]any{"cart_id": value.ID.String(), "session_id": value.SessionID.String(), "hold_id": value.HoldID.String(), "currency": value.Currency, "state": string(value.Status), "version": value.Version, "quote_sha256": hex.EncodeToString(value.QuoteSHA256[:])}
	afterData, err := json.Marshal(after)
	if err != nil {
		return fmt.Errorf("marshal cart audit data: %w", err)
	}
	platform := repository.platform.WithTx(tx)
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: auditID, OrganizationID: organizationID, ActorType: actorType, ActorID: actorID, Action: action, SubjectType: "cart", SubjectID: aggregateID.String(), Result: "success", AfterData: afterData, OccurredAt: occurredAt}); err != nil {
		return err
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: outboxID, OrganizationID: organizationID, AggregateType: "cart", AggregateID: aggregateID, AggregateVersion: version, EventType: action, SchemaVersion: 1, Payload: afterData, AvailableAt: occurredAt, OccurredAt: occurredAt})
}

func validateRecord(value cart.Cart) error {
	if value.ID.IsZero() || value.OrganizationID.IsZero() || value.SessionID.IsZero() || value.HoldID.IsZero() || value.OwnerTokenHash == [32]byte{} || value.Version < 1 || value.Status != cart.StatusActive || value.CreatedAt.IsZero() || value.UpdatedAt.IsZero() || value.UpdatedAt.Before(value.CreatedAt) {
		return fmt.Errorf("%w: cart record is incomplete", cart.ErrInvalidCart)
	}
	if _, err := money.NewCurrency(value.Currency); err != nil {
		return fmt.Errorf("%w: cart currency: %w", cart.ErrInvalidCart, err)
	}
	if len(value.QuoteSnapshot) < 2 || len(value.QuoteSnapshot) > 262144 || value.QuoteSHA256 == [32]byte{} {
		return fmt.Errorf("%w: quote snapshot is invalid", cart.ErrInvalidCart)
	}
	return nil
}

type cartRow struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	SessionID        uuid.UUID
	HoldID           uuid.UUID
	OwnerTokenHash   []byte
	OwnerUserID      pgtype.UUID
	Currency         string
	State            string
	QuoteSnapshot    []byte
	QuoteSnapshotRaw []byte
	QuoteSha256      []byte
	Version          int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func mapInsertCart(row sqlc.InsertCartRow) (cart.Cart, error) {
	return mapCartValues(cartRow{ID: row.ID, OrganizationID: row.OrganizationID, SessionID: row.SessionID, HoldID: row.HoldID, OwnerTokenHash: row.OwnerTokenHash, OwnerUserID: row.OwnerUserID, Currency: row.Currency, State: row.State, QuoteSnapshot: row.QuoteSnapshot, QuoteSnapshotRaw: row.QuoteSnapshotRaw, QuoteSha256: row.QuoteSha256, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
}

func mapGetCart(row sqlc.GetCartForOwnerRow) (cart.Cart, error) {
	return mapCartValues(cartRow{ID: row.ID, OrganizationID: row.OrganizationID, SessionID: row.SessionID, HoldID: row.HoldID, OwnerTokenHash: row.OwnerTokenHash, OwnerUserID: row.OwnerUserID, Currency: row.Currency, State: row.State, QuoteSnapshot: row.QuoteSnapshot, QuoteSnapshotRaw: row.QuoteSnapshotRaw, QuoteSha256: row.QuoteSha256, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
}

func mapLockCart(row sqlc.LockCartForOwnerRow) (cart.Cart, error) {
	return mapCartValues(cartRow{ID: row.ID, OrganizationID: row.OrganizationID, SessionID: row.SessionID, HoldID: row.HoldID, OwnerTokenHash: row.OwnerTokenHash, OwnerUserID: row.OwnerUserID, Currency: row.Currency, State: row.State, QuoteSnapshot: row.QuoteSnapshot, QuoteSnapshotRaw: row.QuoteSnapshotRaw, QuoteSha256: row.QuoteSha256, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
}

func mapUpdatedCart(row sqlc.UpdateCartQuoteRow) (cart.Cart, error) {
	return mapCartValues(cartRow{ID: row.ID, OrganizationID: row.OrganizationID, SessionID: row.SessionID, HoldID: row.HoldID, OwnerTokenHash: row.OwnerTokenHash, OwnerUserID: row.OwnerUserID, Currency: row.Currency, State: row.State, QuoteSnapshot: row.QuoteSnapshot, QuoteSnapshotRaw: row.QuoteSnapshotRaw, QuoteSha256: row.QuoteSha256, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
}

func mapCartValues(row cartRow) (cart.Cart, error) {
	organizationID, err := identifier.FromUUID(row.OrganizationID)
	if err != nil {
		return cart.Cart{}, fmt.Errorf("map cart organization: %w", err)
	}
	sessionID, err := identifier.FromUUID(row.SessionID)
	if err != nil {
		return cart.Cart{}, fmt.Errorf("map cart session: %w", err)
	}
	holdID, err := identifier.FromUUID(row.HoldID)
	if err != nil {
		return cart.Cart{}, fmt.Errorf("map cart hold: %w", err)
	}
	cartID, err := identifier.FromUUID(row.ID)
	if err != nil {
		return cart.Cart{}, fmt.Errorf("map cart ID: %w", err)
	}
	if len(row.OwnerTokenHash) != 32 || len(row.QuoteSha256) != 32 {
		return cart.Cart{}, fmt.Errorf("%w: persisted hashes are invalid", cart.ErrInvalidCart)
	}
	snapshot := row.QuoteSnapshotRaw
	if len(snapshot) == 0 {
		snapshot = row.QuoteSnapshot
	}
	digest := sha256.Sum256(snapshot)
	if len(snapshot) < 2 || !bytes.Equal(digest[:], row.QuoteSha256) {
		return cart.Cart{}, fmt.Errorf("%w: quote snapshot bytes cannot be verified", cart.ErrInvalidCart)
	}
	var ownerUserID *identifier.ID
	if row.OwnerUserID.Valid {
		parsed, parseErr := identifier.FromUUID(row.OwnerUserID.Bytes)
		if parseErr != nil {
			return cart.Cart{}, fmt.Errorf("map cart owner: %w", parseErr)
		}
		ownerUserID = &parsed
	}
	var tokenHash, quoteHash [32]byte
	copy(tokenHash[:], row.OwnerTokenHash)
	copy(quoteHash[:], row.QuoteSha256)
	return cart.Cart{ID: cartID, OrganizationID: organizationID, SessionID: sessionID, HoldID: holdID, OwnerTokenHash: tokenHash, OwnerUserID: ownerUserID, Currency: row.Currency, Status: cart.Status(row.State), QuoteSnapshot: append([]byte(nil), snapshot...), QuoteSHA256: quoteHash, Version: row.Version, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC()}, nil
}

func nullableUUID(value *identifier.ID) pgtype.UUID {
	if value == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: value.UUID(), Valid: true}
}

var _ cart.Repository = (*Repository)(nil)
