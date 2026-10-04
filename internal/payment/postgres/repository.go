// Package postgres persists sanitized payment-attempt facts. Provider calls
// are deliberately absent; adapters run outside these database transactions.
package postgres

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/payment"
	"github.com/HK9750/venueos/internal/payment/postgres/sqlc"
	"github.com/HK9750/venueos/internal/platform/identifier"
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

func (repository *Repository) Create(ctx context.Context, record payment.CreateAttemptRecord) (payment.Attempt, error) {
	if err := validateMutationIDs(record.Attempt.ID, record.Attempt.OrganizationID, record.AuditID, record.OutboxID); err != nil {
		return payment.Attempt{}, err
	}
	if err := record.Attempt.Validate(); err != nil {
		return payment.Attempt{}, err
	}
	var result payment.Attempt
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		orderRow, err := queries.LockOrderForPayment(ctx, sqlc.LockOrderForPaymentParams{OrganizationID: record.Attempt.OrganizationID.UUID(), ID: record.Attempt.OrderID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return payment.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock order for payment attempt: %w", err)
		}
		if orderRow.State != "payment_pending" || orderRow.Currency != record.Attempt.Currency || orderRow.TotalMinor != record.Attempt.AmountMinor {
			return payment.ErrAmountMismatch
		}
		databaseNow, err := queries.GetDatabaseTime(ctx)
		if err != nil {
			return fmt.Errorf("get database time for payment attempt: %w", err)
		}
		row, err := queries.InsertPaymentAttempt(ctx, sqlc.InsertPaymentAttemptParams{
			ID: record.Attempt.ID.UUID(), OrganizationID: record.Attempt.OrganizationID.UUID(), OrderID: record.Attempt.OrderID.UUID(), Provider: record.Attempt.Provider,
			ProviderObjectID: nullableText(record.Attempt.ProviderObjectID), AmountMinor: record.Attempt.AmountMinor, Currency: record.Attempt.Currency, State: string(record.Attempt.Status),
			IdempotencyKey: record.Attempt.IdempotencyKey, FailureClass: nullableText(string(record.Attempt.FailureClass)), FailureCode: nullableText(record.Attempt.FailureCode), FailureMessage: nullableText(record.Attempt.FailureMessage),
			ClientActionReference: nullableText(record.Attempt.ClientActionReference), Metadata: record.Attempt.Metadata, Version: record.Attempt.Version, CreatedAt: databaseNow.UTC(),
		})
		if err != nil {
			var postgresError *pgconn.PgError
			if errors.As(err, &postgresError) && postgresError.Code == "23505" && (postgresError.ConstraintName == "uq_payment_attempts_order_idempotency" || postgresError.ConstraintName == "uq_payment_attempts_provider_object") {
				return payment.ErrAttemptExists
			}
			return fmt.Errorf("insert payment attempt: %w", err)
		}
		result, err = mapAttempt(row)
		if err != nil {
			return err
		}
		return recordMutation(ctx, repository.platform.WithTx(tx), record.AuditID, record.OutboxID, record.ActorType, record.ActorID, result, "payment_attempt.created")
	})
	return result, err
}

func (repository *Repository) ApplyIntent(ctx context.Context, record payment.ApplyIntentRecord) (payment.Attempt, error) {
	return repository.apply(ctx, record.OrganizationID, record.AttemptID, record.ExpectedVersion, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, func(current payment.Attempt, now time.Time) (payment.Attempt, error) {
		return current.ApplyIntent(record.Intent, record.ExpectedVersion, now)
	}, false)
}

func (repository *Repository) ApplyWebhook(ctx context.Context, record payment.ApplyWebhookRecord) (payment.Attempt, error) {
	return repository.apply(ctx, record.OrganizationID, record.AttemptID, record.ExpectedVersion, record.AuditID, record.OutboxID, record.ActorType, record.ActorID, func(current payment.Attempt, now time.Time) (payment.Attempt, error) {
		return current.ApplyWebhook(record.Event, record.ExpectedVersion, now)
	}, true)
}

func (repository *Repository) apply(ctx context.Context, organizationID, attemptID identifier.ID, expectedVersion int64, auditID, outboxID identifier.ID, actorType, actorID string, transition func(payment.Attempt, time.Time) (payment.Attempt, error), webhook bool) (payment.Attempt, error) {
	if err := validateMutationIDs(attemptID, organizationID, auditID, outboxID); err != nil || expectedVersion < 1 {
		if err != nil {
			return payment.Attempt{}, err
		}
		return payment.Attempt{}, payment.ErrVersionConflict
	}
	var result payment.Attempt
	err := repository.transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable}, func(ctx context.Context, tx pgx.Tx) error {
		queries := repository.queries.WithTx(tx)
		row, err := queries.LockPaymentAttempt(ctx, sqlc.LockPaymentAttemptParams{OrganizationID: organizationID.UUID(), ID: attemptID.UUID()})
		if errors.Is(err, pgx.ErrNoRows) {
			return payment.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("lock payment attempt: %w", err)
		}
		current, err := mapAttempt(row)
		if err != nil {
			return err
		}
		if current.Version != expectedVersion {
			return payment.ErrVersionConflict
		}
		databaseNow, err := queries.GetDatabaseTime(ctx)
		if err != nil {
			return fmt.Errorf("get database time for payment transition: %w", err)
		}
		next, err := transition(current, databaseNow.UTC())
		if err != nil {
			return err
		}
		if next.Version == current.Version {
			result = current
			return nil
		}
		updated, err := queries.UpdatePaymentAttempt(ctx, sqlc.UpdatePaymentAttemptParams{OrganizationID: organizationID.UUID(), ID: attemptID.UUID(), ProviderObjectID: nullableText(next.ProviderObjectID), State: string(next.Status), FailureClass: nullableText(string(next.FailureClass)), FailureCode: nullableText(next.FailureCode), FailureMessage: nullableText(next.FailureMessage), ClientActionReference: nullableText(next.ClientActionReference), Metadata: next.Metadata, UpdatedAt: next.UpdatedAt.UTC(), Version: expectedVersion})
		if errors.Is(err, pgx.ErrNoRows) {
			return payment.ErrVersionConflict
		}
		if err != nil {
			return fmt.Errorf("update payment attempt: %w", err)
		}
		result, err = mapAttempt(updated)
		if err != nil {
			return err
		}
		action := "payment_attempt.updated"
		if webhook {
			action = "payment_attempt.webhook_applied"
		}
		return recordMutation(ctx, repository.platform.WithTx(tx), auditID, outboxID, actorType, actorID, result, action)
	})
	return result, err
}

func (repository *Repository) Get(ctx context.Context, organizationID, attemptID identifier.ID) (payment.Attempt, error) {
	if organizationID.IsZero() || attemptID.IsZero() {
		return payment.Attempt{}, payment.ErrNotFound
	}
	row, err := repository.queries.GetPaymentAttempt(ctx, sqlc.GetPaymentAttemptParams{OrganizationID: organizationID.UUID(), ID: attemptID.UUID()})
	if errors.Is(err, pgx.ErrNoRows) {
		return payment.Attempt{}, payment.ErrNotFound
	}
	if err != nil {
		return payment.Attempt{}, fmt.Errorf("get payment attempt: %w", err)
	}
	return mapAttempt(row)
}

func recordMutation(ctx context.Context, platform *platformpostgres.Store, auditID, outboxID identifier.ID, actorType, actorID string, value payment.Attempt, action string) error {
	metadataHash := value.MetadataHash()
	after, err := json.Marshal(map[string]any{"payment_attempt_id": value.ID.String(), "order_id": value.OrderID.String(), "provider": value.Provider, "provider_object_id": value.ProviderObjectID, "amount_minor": value.AmountMinor, "currency": value.Currency, "status": string(value.Status), "version": value.Version, "failure_class": string(value.FailureClass), "failure_code": value.FailureCode, "payload_hash": hex.EncodeToString(metadataHash[:])})
	if err != nil {
		return fmt.Errorf("marshal payment attempt audit data: %w", err)
	}
	if err := platform.InsertAuditEntry(ctx, platformpostgres.AuditEntry{ID: auditID, OrganizationID: value.OrganizationID, ActorType: actorType, ActorID: actorID, Action: action, SubjectType: "payment_attempt", SubjectID: value.ID.String(), Result: "success", AfterData: after, OccurredAt: value.UpdatedAt}); err != nil {
		return err
	}
	return platform.InsertOutboxEvent(ctx, platformpostgres.OutboxEvent{ID: outboxID, OrganizationID: value.OrganizationID, AggregateType: "payment_attempt", AggregateID: value.ID, AggregateVersion: value.Version, EventType: action, SchemaVersion: 1, Payload: after, AvailableAt: value.UpdatedAt, OccurredAt: value.UpdatedAt})
}

func mapAttempt(row sqlc.PaymentAttempt) (payment.Attempt, error) {
	id, err := identifier.FromUUID(row.ID)
	if err != nil {
		return payment.Attempt{}, fmt.Errorf("map payment attempt ID: %w", err)
	}
	organizationID, err := identifier.FromUUID(row.OrganizationID)
	if err != nil {
		return payment.Attempt{}, fmt.Errorf("map payment organization: %w", err)
	}
	orderID, err := identifier.FromUUID(row.OrderID)
	if err != nil {
		return payment.Attempt{}, fmt.Errorf("map payment order: %w", err)
	}
	if !isJSONObject(row.Metadata) {
		return payment.Attempt{}, fmt.Errorf("%w: payment metadata is invalid", payment.ErrInvalidAttempt)
	}
	attempt := payment.Attempt{ID: id, OrganizationID: organizationID, OrderID: orderID, Provider: row.Provider, AmountMinor: row.AmountMinor, Currency: row.Currency, Status: payment.Status(row.State), IdempotencyKey: row.IdempotencyKey, Metadata: append([]byte(nil), row.Metadata...), Version: row.Version, CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC()}
	if row.ProviderObjectID.Valid {
		attempt.ProviderObjectID = row.ProviderObjectID.String
	}
	if row.FailureClass.Valid {
		attempt.FailureClass = payment.FailureClass(row.FailureClass.String)
	}
	if row.FailureCode.Valid {
		attempt.FailureCode = row.FailureCode.String
	}
	if row.FailureMessage.Valid {
		attempt.FailureMessage = row.FailureMessage.String
	}
	if row.ClientActionReference.Valid {
		attempt.ClientActionReference = row.ClientActionReference.String
	}
	return attempt, nil
}

func isJSONObject(value []byte) bool {
	if len(value) < 2 || len(value) > 65536 || !json.Valid(value) {
		return false
	}
	var object map[string]json.RawMessage
	return json.Unmarshal(value, &object) == nil
}

func validateMutationIDs(attemptID, organizationID, auditID, outboxID identifier.ID) error {
	if attemptID.IsZero() || organizationID.IsZero() || auditID.IsZero() || outboxID.IsZero() {
		return fmt.Errorf("%w: mutation identifiers are required", payment.ErrInvalidAttempt)
	}
	return nil
}

func nullableText(value string) pgtype.Text {
	if value == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: value, Valid: true}
}

var _ payment.Repository = (*Repository)(nil)
