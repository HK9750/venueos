// Package refund contains provider-neutral refund invariants and lifecycle
// transitions. Financial policy and provider execution belong outside it.
package refund

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/money"
)

type Status string

const (
	StatusRequested              Status = "requested"
	StatusProcessing             Status = "processing"
	StatusSucceeded              Status = "succeeded"
	StatusFailed                 Status = "failed"
	StatusCancelled              Status = "cancelled"
	StatusReconciliationRequired Status = "reconciliation_required"
)

var (
	ErrInvalidAmount     = errors.New("invalid refund amount")
	ErrExceedsCaptured   = errors.New("refunds exceed captured value")
	ErrInvalidStatus     = errors.New("invalid refund status")
	ErrInvalidTransition = errors.New("invalid refund transition")
	ErrInvalidRefund     = errors.New("invalid refund")
	ErrNotFound          = errors.New("refund not found")
	ErrExists            = errors.New("refund request already exists")
	ErrVersionConflict   = errors.New("refund version conflict")
)

type Refund struct {
	ID               identifier.ID
	OrganizationID   identifier.ID
	OrderID          identifier.ID
	PaymentAttemptID identifier.ID
	AmountMinor      int64
	Currency         string
	Status           Status
	IdempotencyKey   string
	Reason           string
	ActorType        string
	ActorID          string
	ProviderRefundID string
	Version          int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type CreateInput struct {
	ID               identifier.ID
	OrganizationID   identifier.ID
	OrderID          identifier.ID
	PaymentAttemptID identifier.ID
	AmountMinor      int64
	Currency         string
	IdempotencyKey   string
	Reason           string
	ActorType        string
	ActorID          string
	Now              time.Time
}

type CreateRecord struct {
	Refund   Refund
	AuditID  identifier.ID
	OutboxID identifier.ID
}

type Repository interface {
	Create(context.Context, CreateRecord) (Refund, error)
	Get(context.Context, identifier.ID, identifier.ID) (Refund, error)
}

func (refund Refund) Validate() error {
	if refund.ID.IsZero() || refund.OrganizationID.IsZero() || refund.OrderID.IsZero() || refund.PaymentAttemptID.IsZero() || refund.AmountMinor < 1 || !validReference(refund.IdempotencyKey, 16, 128) || len(strings.TrimSpace(refund.Reason)) < 1 || len(strings.TrimSpace(refund.Reason)) > 1000 || len(refund.ActorType) < 1 || len(refund.ActorType) > 64 || len(refund.ActorID) > 255 || refund.Version < 1 || refund.CreatedAt.IsZero() || refund.UpdatedAt.IsZero() || refund.UpdatedAt.Before(refund.CreatedAt) {
		return fmt.Errorf("%w: refund fields are invalid", ErrInvalidRefund)
	}
	if _, err := money.NewCurrency(refund.Currency); err != nil {
		return fmt.Errorf("%w: currency is invalid", ErrInvalidRefund)
	}
	if refund.Status != StatusRequested {
		return fmt.Errorf("%w: new refunds must be requested", ErrInvalidRefund)
	}
	return nil
}

func Build(input CreateInput) (Refund, error) {
	if input.ID.IsZero() || input.OrganizationID.IsZero() || input.OrderID.IsZero() || input.PaymentAttemptID.IsZero() || input.AmountMinor < 1 || input.Now.IsZero() {
		return Refund{}, fmt.Errorf("%w: identifiers, positive amount, and time are required", ErrInvalidRefund)
	}
	currency := strings.ToUpper(strings.TrimSpace(input.Currency))
	if _, err := money.NewCurrency(currency); err != nil {
		return Refund{}, fmt.Errorf("%w: currency is invalid", ErrInvalidRefund)
	}
	if !validReference(input.IdempotencyKey, 16, 128) || len(strings.TrimSpace(input.Reason)) < 1 || len(strings.TrimSpace(input.Reason)) > 1000 || len(input.ActorType) < 1 || len(input.ActorType) > 64 || len(input.ActorID) > 255 {
		return Refund{}, fmt.Errorf("%w: idempotency, reason, or actor is invalid", ErrInvalidRefund)
	}
	now := input.Now.UTC()
	return Refund{ID: input.ID, OrganizationID: input.OrganizationID, OrderID: input.OrderID, PaymentAttemptID: input.PaymentAttemptID, AmountMinor: input.AmountMinor, Currency: currency, Status: StatusRequested, IdempotencyKey: input.IdempotencyKey, Reason: strings.Join(strings.Fields(input.Reason), " "), ActorType: input.ActorType, ActorID: input.ActorID, Version: 1, CreatedAt: now, UpdatedAt: now}, nil
}

func validReference(value string, minimum, maximum int) bool {
	if len(value) < minimum || len(value) > maximum {
		return false
	}
	for index := range value {
		char := value[index]
		if (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == ':' || char == '-' {
			continue
		}
		return false
	}
	return true
}

// ValidateAmount enforces the non-negotiable financial ceiling. allocated is
// the sum of pending and successful refunds already recorded for the order;
// callers must execute this check under the order/refund lock or equivalent
// serializable database constraint.
func ValidateAmount(captured, allocated, requested money.Money) error {
	if captured.Currency() == "" || allocated.Currency() != captured.Currency() || requested.Currency() != captured.Currency() {
		return money.ErrCurrencyMismatch
	}
	if requested.MinorUnits() < 1 {
		return ErrInvalidAmount
	}
	if allocated.MinorUnits() > captured.MinorUnits() {
		return ErrExceedsCaptured
	}
	total, err := allocated.Add(requested)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrExceedsCaptured, err)
	}
	if total.MinorUnits() > captured.MinorUnits() {
		return ErrExceedsCaptured
	}
	return nil
}

// Advance validates a refund lifecycle transition. Same-state delivery is an
// idempotent no-op for retried worker/provider notifications.
func Advance(current, target Status) (Status, bool, error) {
	if !validStatus(current) || !validStatus(target) {
		return "", false, ErrInvalidStatus
	}
	if current == target {
		return current, false, nil
	}
	if !allowedTransition(current, target) {
		return "", false, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current, target)
	}
	return target, true, nil
}

func validStatus(value Status) bool {
	switch value {
	case StatusRequested, StatusProcessing, StatusSucceeded, StatusFailed, StatusCancelled, StatusReconciliationRequired:
		return true
	default:
		return false
	}
}

func allowedTransition(current, target Status) bool {
	switch current {
	case StatusRequested:
		return target == StatusProcessing || target == StatusCancelled
	case StatusProcessing:
		return target == StatusSucceeded || target == StatusFailed || target == StatusCancelled || target == StatusReconciliationRequired
	case StatusSucceeded, StatusFailed, StatusCancelled, StatusReconciliationRequired:
		return false
	default:
		return false
	}
}
