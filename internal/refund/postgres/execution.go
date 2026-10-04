package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/refund"
	"github.com/HK9750/venueos/internal/refund/postgres/sqlc"
	"github.com/jackc/pgx/v5"
)

func (repository *Repository) PrepareExecution(ctx context.Context, record refund.PrepareExecutionRecord) (refund.PrepareExecutionResult, error) {
	if record.OrganizationID.IsZero() || record.RefundID.IsZero() || record.AuditID.IsZero() || record.OutboxID.IsZero() || record.OccurredAt.IsZero() {
		return refund.PrepareExecutionResult{}, fmt.Errorf("%w: refund execution identifiers and time are required", refund.ErrInvalidRefund)
	}
	var result refund.PrepareExecutionResult
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		row, err := queries.LockRefundExecution(ctx, sqlc.LockRefundExecutionParams{OrganizationID: record.OrganizationID.UUID(), ID: record.RefundID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return refund.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock refund for execution: %w", err)
		}
		value, err := mapRefundExecution(row)
		if err != nil {
			return err
		}
		execution := refund.Execution{Refund: value, Provider: row.Provider, PaymentReference: row.ProviderObjectID.String}
		switch value.Status {
		case refund.StatusSucceeded, refund.StatusFailed, refund.StatusCancelled, refund.StatusReconciliationRequired:
			result = refund.PrepareExecutionResult{Execution: execution, Call: false}
			return nil
		case refund.StatusRequested, refund.StatusProcessing:
		default:
			return refund.ErrInvalidStatus
		}
		if strings.TrimSpace(row.Provider) == "" || !row.ProviderObjectID.Valid || strings.TrimSpace(row.ProviderObjectID.String) == "" {
			return fmt.Errorf("%w: captured payment provider reference is missing", refund.ErrInvalidRefund)
		}
		if value.Status == refund.StatusRequested {
			databaseNow, err := queries.GetDatabaseTime(ctx)
			if err != nil {
				return fmt.Errorf("get database time for refund execution: %w", err)
			}
			updated, err := queries.MarkRefundProcessing(ctx, sqlc.MarkRefundProcessingParams{OrganizationID: record.OrganizationID.UUID(), ID: record.RefundID.UUID(), UpdatedAt: databaseNow.UTC(), Version: value.Version})
			if errors.Is(err, pgx.ErrNoRows) {
				return refund.ErrVersionConflict
			}
			if err != nil {
				return fmt.Errorf("mark refund processing: %w", err)
			}
			value, err = mapRefund(updated)
			if err != nil {
				return err
			}
			if err := recordStateMutation(ctx, repository.platform.WithTx(tx), record.AuditID, record.OutboxID, "refund.processing", value); err != nil {
				return err
			}
			execution.Refund = value
		}
		result = refund.PrepareExecutionResult{Execution: execution, Call: true}
		return nil
	})
	return result, err
}

func (repository *Repository) FinalizeExecution(ctx context.Context, record refund.FinalizeExecutionRecord) (refund.Refund, error) {
	if record.OrganizationID.IsZero() || record.RefundID.IsZero() || record.ExpectedVersion < 1 || record.AuditID.IsZero() || record.OutboxID.IsZero() || record.OccurredAt.IsZero() {
		return refund.Refund{}, fmt.Errorf("%w: refund finalization identifiers, version, and time are required", refund.ErrInvalidRefund)
	}
	if record.Status != refund.StatusSucceeded && record.Status != refund.StatusFailed && record.Status != refund.StatusReconciliationRequired {
		return refund.Refund{}, refund.ErrInvalidTransition
	}
	var result refund.Refund
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		currentRow, err := queries.LockRefundExecution(ctx, sqlc.LockRefundExecutionParams{OrganizationID: record.OrganizationID.UUID(), ID: record.RefundID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return refund.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock refund for finalization: %w", err)
		}
		current, err := mapRefundExecution(currentRow)
		if err != nil {
			return err
		}
		if current.Status == record.Status && current.ProviderRefundID == record.ProviderRefundID {
			result = current
			return nil
		}
		if current.Status != refund.StatusProcessing || current.Version != record.ExpectedVersion {
			return refund.ErrVersionConflict
		}
		updated, err := queries.FinalizeRefund(ctx, sqlc.FinalizeRefundParams{OrganizationID: record.OrganizationID.UUID(), ID: record.RefundID.UUID(), State: string(record.Status), ProviderRefundID: nullableText(record.ProviderRefundID), UpdatedAt: record.OccurredAt.UTC(), Version: record.ExpectedVersion})
		if errors.Is(err, pgx.ErrNoRows) {
			return refund.ErrVersionConflict
		}
		if err != nil {
			return fmt.Errorf("finalize refund: %w", err)
		}
		result, err = mapRefund(updated)
		if err != nil {
			return err
		}
		return recordStateMutation(ctx, repository.platform.WithTx(tx), record.AuditID, record.OutboxID, "refund."+string(record.Status), result)
	})
	return result, err
}

func mapRefundExecution(row sqlc.LockRefundExecutionRow) (refund.Refund, error) {
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
