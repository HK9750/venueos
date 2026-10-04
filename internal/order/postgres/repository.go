// Package postgres persists the durable order boundary and its immutable lines.
package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/order"
	"github.com/HK9750/venueos/internal/order/postgres/sqlc"
	"github.com/HK9750/venueos/internal/platform/identifier"
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

func (repository *Repository) CreateFromCart(ctx context.Context, record order.CreateFromCartRecord) (order.Order, error) {
	if err := validateRecord(record); err != nil {
		return order.Order{}, err
	}
	var result order.Order
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		cartRow, err := queries.LockCartForCheckout(ctx, sqlc.LockCartForCheckoutParams{OrganizationID: record.Order.OrganizationID.UUID(), ID: record.Order.CartID.UUID(), OwnerTokenHash: record.Order.OwnerTokenHash[:]})
		if errors.Is(err, pgx.ErrNoRows) {
			return order.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock cart for checkout: %w", err)
		}
		if cartRow.State != "active" {
			return order.ErrCartNotActive
		}
		if cartRow.OrganizationID != record.Order.OrganizationID.UUID() || cartRow.OwnerTokenHash == nil || len(cartRow.OwnerTokenHash) != 32 {
			return order.ErrNotFound
		}
		holdRow, err := queries.LockHoldForCheckout(ctx, sqlc.LockHoldForCheckoutParams{OrganizationID: record.Order.OrganizationID.UUID(), ID: cartRow.HoldID, OwnerTokenHash: record.Order.OwnerTokenHash[:]})
		if errors.Is(err, pgx.ErrNoRows) {
			return order.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock hold for checkout: %w", err)
		}
		databaseNow, err := queries.GetDatabaseTime(ctx)
		if err != nil {
			return fmt.Errorf("get database time for checkout: %w", err)
		}
		if cartRow.SessionID != holdRow.SessionID || cartRow.Currency != holdRow.Currency || holdRow.State != "active" || !holdRow.ExpiresAt.After(databaseNow) {
			return order.ErrHoldNotActive
		}
		cartID, err := identifier.FromUUID(cartRow.ID)
		if err != nil {
			return fmt.Errorf("map checkout cart ID: %w", err)
		}
		sessionID, err := identifier.FromUUID(cartRow.SessionID)
		if err != nil {
			return fmt.Errorf("map checkout session ID: %w", err)
		}
		holdID, err := identifier.FromUUID(cartRow.HoldID)
		if err != nil {
			return fmt.Errorf("map checkout hold ID: %w", err)
		}
		ownerUserID, err := mapOptionalID(cartRow.OwnerUserID)
		if err != nil {
			return err
		}
		var quoteHash [32]byte
		if len(cartRow.QuoteSha256) != len(quoteHash) {
			return fmt.Errorf("%w: cart quote digest is invalid", order.ErrInvalidOrder)
		}
		copy(quoteHash[:], cartRow.QuoteSha256)
		value, err := order.BuildFromCart(order.CreateFromCartInput{
			ID: record.Order.ID, OrganizationID: record.Order.OrganizationID, CartID: cartID, SessionID: sessionID, HoldID: holdID,
			OrderNumber: record.Order.OrderNumber, OwnerTokenHash: record.Order.OwnerTokenHash, OwnerUserID: ownerUserID,
			Currency: cartRow.Currency, QuoteSnapshot: snapshotBytes(cartRow.QuoteSnapshotRaw, cartRow.QuoteSnapshot), QuoteSHA256: quoteHash, Now: databaseNow,
		})
		if err != nil {
			return err
		}
		row, err := queries.InsertOrder(ctx, sqlc.InsertOrderParams{
			ID: value.ID.UUID(), OrganizationID: value.OrganizationID.UUID(), CartID: value.CartID.UUID(), SessionID: value.SessionID.UUID(), HoldID: value.HoldID.UUID(), OrderNumber: value.OrderNumber,
			OwnerTokenHash: value.OwnerTokenHash[:], OwnerUserID: nullableUUID(value.OwnerUserID), Currency: value.Currency, State: string(value.Status), SubtotalMinor: value.SubtotalMinor,
			DiscountMinor: value.DiscountMinor, FeesMinor: value.FeesMinor, TaxesMinor: value.TaxesMinor, TotalMinor: value.TotalMinor, QuoteSnapshot: value.QuoteSnapshot,
			QuoteSnapshotRaw: value.QuoteSnapshot, QuoteSha256: value.QuoteSHA256[:], Version: value.Version, CreatedAt: value.CreatedAt,
		})
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && (postgresError.ConstraintName == "uq_orders_organization_cart" || postgresError.ConstraintName == "uq_orders_organization_hold" || postgresError.ConstraintName == "uq_orders_organization_number") {
				return order.ErrOrderExists
			}
			return fmt.Errorf("insert order: %w", err)
		}
		for _, line := range value.Lines {
			lineID, newErr := identifier.New()
			if newErr != nil {
				return fmt.Errorf("generate order line ID: %w", newErr)
			}
			if err := queries.InsertOrderLine(ctx, sqlc.InsertOrderLineParams{ID: lineID.UUID(), OrganizationID: value.OrganizationID.UUID(), OrderID: value.ID.UUID(), LineNumber: line.LineNumber, PriceTierID: line.PriceTierID.UUID(), Quantity: line.Quantity, UnitMinor: line.UnitMinor, SubtotalMinor: line.SubtotalMinor, Snapshot: line.Snapshot, CreatedAt: value.CreatedAt}); err != nil {
				return fmt.Errorf("insert order line: %w", err)
			}
		}
		checkedOutCart, err := queries.MarkCartCheckedOut(ctx, sqlc.MarkCartCheckedOutParams{OrganizationID: value.OrganizationID.UUID(), ID: cartRow.ID, UpdatedAt: databaseNow.UTC(), OwnerTokenHash: value.OwnerTokenHash[:], Version: cartRow.Version})
		if errors.Is(err, pgx.ErrNoRows) {
			return order.ErrVersionConflict
		}
		if err != nil {
			return fmt.Errorf("mark cart checked out: %w", err)
		}
		result, err = mapInsertOrder(row, value.Lines)
		if err != nil {
			return err
		}
		platform := repository.platform.WithTx(tx)
		if err := recordOrderMutation(ctx, platform, record, result); err != nil {
			return err
		}
		if err := recordCartMutation(ctx, platform, record, value.OrganizationID, cartID, checkedOutCart.Version, databaseNow); err != nil {
			return err
		}
		return nil
	})
	return result, err
}

func (repository *Repository) Get(ctx context.Context, organizationID, orderID identifier.ID, ownerTokenHash [32]byte) (order.Order, error) {
	if organizationID.IsZero() || orderID.IsZero() || ownerTokenHash == [32]byte{} {
		return order.Order{}, order.ErrNotFound
	}
	row, err := repository.queries.GetOrderForOwner(ctx, sqlc.GetOrderForOwnerParams{OrganizationID: organizationID.UUID(), ID: orderID.UUID(), OwnerTokenHash: ownerTokenHash[:]})
	if errors.Is(err, pgx.ErrNoRows) {
		return order.Order{}, order.ErrNotFound
	}
	if err != nil {
		return order.Order{}, fmt.Errorf("get order: %w", err)
	}
	lines, err := repository.queries.ListOrderLines(ctx, sqlc.ListOrderLinesParams{OrganizationID: organizationID.UUID(), OrderID: orderID.UUID()})
	if err != nil {
		return order.Order{}, fmt.Errorf("get order lines: %w", err)
	}
	return mapStoredOrder(row, lines)
}

func recordOrderMutation(ctx context.Context, platform *platformpostgres.Store, record order.CreateFromCartRecord, value order.Order) error {
	after, err := json.Marshal(map[string]any{"order_id": value.ID.String(), "order_number": value.OrderNumber, "cart_id": value.CartID.String(), "hold_id": value.HoldID.String(), "session_id": value.SessionID.String(), "currency": value.Currency, "state": string(value.Status), "subtotal_minor": value.SubtotalMinor, "discount_minor": value.DiscountMinor, "fees_minor": value.FeesMinor, "taxes_minor": value.TaxesMinor, "total_minor": value.TotalMinor, "version": value.Version, "quote_sha256": hex.EncodeToString(value.QuoteSHA256[:])})
	if err != nil {
		return fmt.Errorf("marshal order audit data: %w", err)
	}
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: record.AuditID, OrganizationID: value.OrganizationID, ActorType: record.ActorType, ActorID: record.ActorID, Action: "order.created", SubjectType: "order", SubjectID: value.ID.String(), Result: "success", AfterData: after, OccurredAt: value.CreatedAt}); err != nil {
		return err
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: record.OutboxID, OrganizationID: value.OrganizationID, AggregateType: "order", AggregateID: value.ID, AggregateVersion: value.Version, EventType: "order.created", SchemaVersion: 1, Payload: after, AvailableAt: value.CreatedAt, OccurredAt: value.CreatedAt})
}

func recordCartMutation(ctx context.Context, platform *platformpostgres.Store, record order.CreateFromCartRecord, organizationID, cartID identifier.ID, version int64, occurredAt time.Time) error {
	after, err := json.Marshal(map[string]any{"cart_id": cartID.String(), "state": "checked_out", "version": version})
	if err != nil {
		return fmt.Errorf("marshal cart checkout audit data: %w", err)
	}
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: record.CartAuditID, OrganizationID: organizationID, ActorType: record.ActorType, ActorID: record.ActorID, Action: "cart.checked_out", SubjectType: "cart", SubjectID: cartID.String(), Result: "success", AfterData: after, OccurredAt: occurredAt}); err != nil {
		return err
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: record.CartOutboxID, OrganizationID: organizationID, AggregateType: "cart", AggregateID: cartID, AggregateVersion: version, EventType: "cart.checked_out", SchemaVersion: 1, Payload: after, AvailableAt: occurredAt, OccurredAt: occurredAt})
}

type orderRow struct {
	ID               uuid.UUID
	OrganizationID   uuid.UUID
	CartID           uuid.UUID
	SessionID        uuid.UUID
	HoldID           uuid.UUID
	OrderNumber      string
	OwnerTokenHash   []byte
	OwnerUserID      pgtype.UUID
	Currency         string
	State            string
	SubtotalMinor    int64
	DiscountMinor    int64
	FeesMinor        int64
	TaxesMinor       int64
	TotalMinor       int64
	QuoteSnapshot    []byte
	QuoteSnapshotRaw []byte
	QuoteSha256      []byte
	Version          int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
	ConfirmedAt      pgtype.Timestamptz
}

func mapInsertOrder(row sqlc.InsertOrderRow, lines []order.Line) (order.Order, error) {
	return mapOrderWithLines(orderRow{ID: row.ID, OrganizationID: row.OrganizationID, CartID: row.CartID, SessionID: row.SessionID, HoldID: row.HoldID, OrderNumber: row.OrderNumber, OwnerTokenHash: row.OwnerTokenHash, OwnerUserID: row.OwnerUserID, Currency: row.Currency, State: row.State, SubtotalMinor: row.SubtotalMinor, DiscountMinor: row.DiscountMinor, FeesMinor: row.FeesMinor, TaxesMinor: row.TaxesMinor, TotalMinor: row.TotalMinor, QuoteSnapshot: row.QuoteSnapshot, QuoteSnapshotRaw: row.QuoteSnapshotRaw, QuoteSha256: row.QuoteSha256, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ConfirmedAt: row.ConfirmedAt}, lines)
}

func mapStoredOrder(row sqlc.GetOrderForOwnerRow, rows []sqlc.OrderLine) (order.Order, error) {
	lineValues := make([]order.Line, 0, len(rows))
	for _, value := range rows {
		lineID, lineErr := identifier.FromUUID(value.PriceTierID)
		if lineErr != nil {
			return order.Order{}, fmt.Errorf("map order line price tier: %w", lineErr)
		}
		lineValues = append(lineValues, order.Line{LineNumber: value.LineNumber, PriceTierID: lineID, Quantity: value.Quantity, UnitMinor: value.UnitMinor, SubtotalMinor: value.SubtotalMinor, Snapshot: append([]byte(nil), value.Snapshot...)})
	}
	return mapOrderWithLines(orderRow{ID: row.ID, OrganizationID: row.OrganizationID, CartID: row.CartID, SessionID: row.SessionID, HoldID: row.HoldID, OrderNumber: row.OrderNumber, OwnerTokenHash: row.OwnerTokenHash, OwnerUserID: row.OwnerUserID, Currency: row.Currency, State: row.State, SubtotalMinor: row.SubtotalMinor, DiscountMinor: row.DiscountMinor, FeesMinor: row.FeesMinor, TaxesMinor: row.TaxesMinor, TotalMinor: row.TotalMinor, QuoteSnapshot: row.QuoteSnapshot, QuoteSnapshotRaw: row.QuoteSnapshotRaw, QuoteSha256: row.QuoteSha256, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ConfirmedAt: row.ConfirmedAt}, lineValues)
}

func mapOrderWithLines(row orderRow, lineValues []order.Line) (order.Order, error) {
	orderID, err := identifier.FromUUID(row.ID)
	if err != nil {
		return order.Order{}, fmt.Errorf("map order ID: %w", err)
	}
	organizationID, err := identifier.FromUUID(row.OrganizationID)
	if err != nil {
		return order.Order{}, fmt.Errorf("map order organization: %w", err)
	}
	cartID, err := identifier.FromUUID(row.CartID)
	if err != nil {
		return order.Order{}, fmt.Errorf("map order cart: %w", err)
	}
	sessionID, err := identifier.FromUUID(row.SessionID)
	if err != nil {
		return order.Order{}, fmt.Errorf("map order session: %w", err)
	}
	holdID, err := identifier.FromUUID(row.HoldID)
	if err != nil {
		return order.Order{}, fmt.Errorf("map order hold: %w", err)
	}
	if len(row.OwnerTokenHash) != 32 || len(row.QuoteSha256) != 32 || row.Version < 1 {
		return order.Order{}, fmt.Errorf("%w: persisted order hashes/version are invalid", order.ErrInvalidOrder)
	}
	ownerUserID, err := mapOptionalID(row.OwnerUserID)
	if err != nil {
		return order.Order{}, err
	}
	var ownerHash, quoteHash [32]byte
	copy(ownerHash[:], row.OwnerTokenHash)
	copy(quoteHash[:], row.QuoteSha256)
	var confirmedAt *time.Time
	if row.ConfirmedAt.Valid {
		value := row.ConfirmedAt.Time.UTC()
		confirmedAt = &value
	}
	snapshot := snapshotBytes(row.QuoteSnapshotRaw, row.QuoteSnapshot)
	return order.Order{ID: orderID, OrganizationID: organizationID, CartID: cartID, SessionID: sessionID, HoldID: holdID, OrderNumber: row.OrderNumber, OwnerTokenHash: ownerHash, OwnerUserID: ownerUserID, Currency: row.Currency, Status: order.Status(row.State), SubtotalMinor: row.SubtotalMinor, DiscountMinor: row.DiscountMinor, FeesMinor: row.FeesMinor, TaxesMinor: row.TaxesMinor, TotalMinor: row.TotalMinor, QuoteSnapshot: append([]byte(nil), snapshot...), QuoteSHA256: quoteHash, Version: row.Version, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(), ConfirmedAt: confirmedAt, Lines: lineValues}, nil
}

func snapshotBytes(raw, normalized []byte) []byte {
	if len(raw) > 0 {
		return raw
	}
	return normalized
}

func validateRecord(record order.CreateFromCartRecord) error {
	if record.Order.ID.IsZero() || record.Order.OrganizationID.IsZero() || record.Order.CartID.IsZero() || record.Order.OwnerTokenHash == [32]byte{} || record.AuditID.IsZero() || record.OutboxID.IsZero() || record.CartAuditID.IsZero() || record.CartOutboxID.IsZero() {
		return fmt.Errorf("%w: order and mutation identifiers are required", order.ErrInvalidOrder)
	}
	return nil
}

func mapOptionalID(value pgtype.UUID) (*identifier.ID, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := identifier.FromUUID(value.Bytes)
	if err != nil {
		return nil, fmt.Errorf("map optional user ID: %w", err)
	}
	return &parsed, nil
}

func nullableUUID(value *identifier.ID) pgtype.UUID {
	if value == nil {
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: value.UUID(), Valid: true}
}

var _ order.Repository = (*Repository)(nil)
