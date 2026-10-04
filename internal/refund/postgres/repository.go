// Package postgres persists refund requests and enforces their financial
// ceiling while the order and payment attempt are locked.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/money"
	platformpostgres "github.com/HK9750/venueos/internal/platform/postgres"
	"github.com/HK9750/venueos/internal/refund"
	"github.com/HK9750/venueos/internal/refund/postgres/sqlc"
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

func (repository *Repository) Create(ctx context.Context, record refund.CreateRecord) (refund.Refund, error) {
	if err := record.Refund.Validate(); err != nil {
		return refund.Refund{}, err
	}
	if record.AuditID.IsZero() || record.OutboxID.IsZero() {
		return refund.Refund{}, fmt.Errorf("%w: audit and outbox IDs are required", refund.ErrInvalidRefund)
	}
	var result refund.Refund
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		orderRow, err := queries.LockOrderForRefund(ctx, sqlc.LockOrderForRefundParams{OrganizationID: record.Refund.OrganizationID.UUID(), ID: record.Refund.OrderID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return refund.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock order for refund: %w", err)
		}
		if orderRow.State != "confirmed" && orderRow.State != "partially_refunded" {
			return refund.ErrInvalidTransition
		}
		paymentRow, err := queries.LockPaymentForRefund(ctx, sqlc.LockPaymentForRefundParams{OrganizationID: record.Refund.OrganizationID.UUID(), ID: record.Refund.PaymentAttemptID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return refund.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock payment for refund: %w", err)
		}
		if paymentRow.OrderID != record.Refund.OrderID.UUID() || paymentRow.State != "captured" || paymentRow.Currency != record.Refund.Currency || orderRow.Currency != record.Refund.Currency {
			return refund.ErrInvalidAmount
		}
		existing, lookupErr := queries.GetRefundByIdempotency(ctx, sqlc.GetRefundByIdempotencyParams{OrganizationID: record.Refund.OrganizationID.UUID(), OrderID: record.Refund.OrderID.UUID(), IdempotencyKey: record.Refund.IdempotencyKey})
		if lookupErr == nil {
			existingRefund, mapErr := mapRefund(existing)
			if mapErr != nil {
				return mapErr
			}
			if !sameRequest(existingRefund, record.Refund) {
				return refund.ErrExists
			}
			result = existingRefund
			return nil
		}
		if !errors.Is(lookupErr, pgx.ErrNoRows) {
			return fmt.Errorf("get refund idempotency key: %w", lookupErr)
		}
		allocatedMinor, err := queries.SumAllocatedRefunds(ctx, sqlc.SumAllocatedRefundsParams{OrganizationID: record.Refund.OrganizationID.UUID(), OrderID: record.Refund.OrderID.UUID(), PaymentAttemptID: record.Refund.PaymentAttemptID.UUID()})
		if err != nil {
			return fmt.Errorf("sum allocated refunds: %w", err)
		}
		currency, err := money.NewCurrency(record.Refund.Currency)
		if err != nil {
			return fmt.Errorf("%w: refund currency: %w", refund.ErrInvalidAmount, err)
		}
		captured, err := money.New(paymentRow.AmountMinor, currency)
		if err != nil {
			return fmt.Errorf("%w: captured amount: %w", refund.ErrInvalidAmount, err)
		}
		allocated, err := money.New(allocatedMinor, currency)
		if err != nil {
			return fmt.Errorf("%w: allocated amount: %w", refund.ErrInvalidAmount, err)
		}
		requested, err := money.New(record.Refund.AmountMinor, currency)
		if err != nil {
			return fmt.Errorf("%w: requested amount: %w", refund.ErrInvalidAmount, err)
		}
		if err := refund.ValidateAmount(captured, allocated, requested); err != nil {
			return err
		}
		databaseNow, err := queries.GetDatabaseTime(ctx)
		if err != nil {
			return fmt.Errorf("get database time for refund: %w", err)
		}
		row, err := queries.InsertRefund(ctx, sqlc.InsertRefundParams{ID: record.Refund.ID.UUID(), OrganizationID: record.Refund.OrganizationID.UUID(), OrderID: record.Refund.OrderID.UUID(), PaymentAttemptID: record.Refund.PaymentAttemptID.UUID(), AmountMinor: record.Refund.AmountMinor, Currency: record.Refund.Currency, State: string(refund.StatusRequested), IdempotencyKey: record.Refund.IdempotencyKey, Reason: record.Refund.Reason, ActorType: record.Refund.ActorType, ActorID: nullableText(record.Refund.ActorID), Version: record.Refund.Version, CreatedAt: databaseNow.UTC()})
		if errors.Is(err, pgx.ErrNoRows) {
			existing, lookupErr := queries.GetRefundByIdempotency(ctx, sqlc.GetRefundByIdempotencyParams{OrganizationID: record.Refund.OrganizationID.UUID(), OrderID: record.Refund.OrderID.UUID(), IdempotencyKey: record.Refund.IdempotencyKey})
			if errors.Is(lookupErr, pgx.ErrNoRows) {
				return refund.ErrExists
			}
			if lookupErr != nil {
				return fmt.Errorf("get refund idempotency replay: %w", lookupErr)
			}
			existingRefund, mapErr := mapRefund(existing)
			if mapErr != nil {
				return mapErr
			}
			if !sameRequest(existingRefund, record.Refund) {
				return refund.ErrExists
			}
			result = existingRefund
			return nil
		}
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && postgresError.ConstraintName == "uq_refunds_order_idempotency" {
				return refund.ErrExists
			}
			return fmt.Errorf("insert refund: %w", err)
		}
		result, err = mapRefund(row)
		if err != nil {
			return err
		}
		return recordMutation(ctx, repository.platform.WithTx(tx), record.AuditID, record.OutboxID, result)
	})
	return result, err
}

func sameRequest(existing, requested refund.Refund) bool {
	return existing.OrganizationID == requested.OrganizationID &&
		existing.OrderID == requested.OrderID &&
		existing.PaymentAttemptID == requested.PaymentAttemptID &&
		existing.AmountMinor == requested.AmountMinor &&
		existing.Currency == requested.Currency &&
		existing.IdempotencyKey == requested.IdempotencyKey &&
		existing.Reason == requested.Reason
}

func (repository *Repository) Get(ctx context.Context, organizationID, refundID identifier.ID) (refund.Refund, error) {
	if organizationID.IsZero() || refundID.IsZero() {
		return refund.Refund{}, refund.ErrNotFound
	}
	row, err := repository.queries.GetRefund(ctx, sqlc.GetRefundParams{OrganizationID: organizationID.UUID(), ID: refundID.UUID()})
	if errors.Is(err, pgx.ErrNoRows) {
		return refund.Refund{}, refund.ErrNotFound
	}
	if err != nil {
		return refund.Refund{}, fmt.Errorf("get refund: %w", err)
	}
	return mapRefund(row)
}

func recordMutation(ctx context.Context, platform *platformpostgres.Store, auditID, outboxID identifier.ID, value refund.Refund) error {
	return recordStateMutation(ctx, platform, auditID, outboxID, "refund.requested", value)
}

func recordStateMutation(ctx context.Context, platform *platformpostgres.Store, auditID, outboxID identifier.ID, action string, value refund.Refund) error {
	payload, err := json.Marshal(map[string]any{"refund_id": value.ID.String(), "order_id": value.OrderID.String(), "payment_attempt_id": value.PaymentAttemptID.String(), "amount_minor": value.AmountMinor, "currency": value.Currency, "state": string(value.Status), "version": value.Version})
	if err != nil {
		return fmt.Errorf("marshal refund mutation: %w", err)
	}
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: auditID, OrganizationID: value.OrganizationID, ActorType: value.ActorType, ActorID: value.ActorID, Action: action, SubjectType: "refund", SubjectID: value.ID.String(), Result: "success", AfterData: payload, OccurredAt: value.UpdatedAt}); err != nil {
		return err
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: outboxID, OrganizationID: value.OrganizationID, AggregateType: "refund", AggregateID: value.ID, AggregateVersion: value.Version, EventType: action, SchemaVersion: 1, Payload: payload, AvailableAt: value.UpdatedAt, OccurredAt: value.UpdatedAt})
}

func mapRefund(row sqlc.Refund) (refund.Refund, error) {
	id, err := identifier.FromUUID(row.ID)
	if err != nil {
		return refund.Refund{}, err
	}
	organizationID, err := identifier.FromUUID(row.OrganizationID)
	if err != nil {
		return refund.Refund{}, err
	}
	orderID, err := identifier.FromUUID(row.OrderID)
	if err != nil {
		return refund.Refund{}, err
	}
	paymentID, err := identifier.FromUUID(row.PaymentAttemptID)
	if err != nil {
		return refund.Refund{}, err
	}
	value := refund.Refund{ID: id, OrganizationID: organizationID, OrderID: orderID, PaymentAttemptID: paymentID, AmountMinor: row.AmountMinor, Currency: row.Currency, Status: refund.Status(row.State), IdempotencyKey: row.IdempotencyKey, Reason: row.Reason, ActorType: row.ActorType, Version: row.Version, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC()}
	if row.ActorID.Valid {
		value.ActorID = row.ActorID.String
	}
	if row.ProviderRefundID.Valid {
		value.ProviderRefundID = row.ProviderRefundID.String
	}
	return value, nil
}

func nullableText(value string) pgtype.Text {
	if value == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: value, Valid: true}
}

var _ refund.Repository = (*Repository)(nil)
var _ refund.ExecutionRepository = (*Repository)(nil)
