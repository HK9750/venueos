// Package order contains provider-neutral commerce state machines.
// Persistence, authorization, idempotency records, and provider calls belong
// to the application/infrastructure layers around these pure transitions.
package order

import (
	"errors"
	"fmt"
)

type Status string

const (
	StatusDraft                  Status = "draft"
	StatusPaymentPending         Status = "payment_pending"
	StatusConfirmed              Status = "confirmed"
	StatusPaymentFailed          Status = "payment_failed"
	StatusCancelled              Status = "cancelled"
	StatusReconciliationRequired Status = "reconciliation_required"
	StatusPartiallyRefunded      Status = "partially_refunded"
	StatusRefunded               Status = "refunded"
	StatusFulfilmentIssue        Status = "fulfilment_issue"
)

type PaymentStatus string

const (
	PaymentCreated        PaymentStatus = "created"
	PaymentRequiresAction PaymentStatus = "requires_action"
	PaymentProcessing     PaymentStatus = "processing"
	PaymentAuthorized     PaymentStatus = "authorized"
	PaymentCaptured       PaymentStatus = "captured"
	PaymentFailed         PaymentStatus = "failed"
	PaymentCancelled      PaymentStatus = "cancelled"
	PaymentUnknown        PaymentStatus = "unknown"
)

var (
	ErrInvalidStatus     = errors.New("invalid order status")
	ErrInvalidPayment    = errors.New("invalid payment status")
	ErrInvalidTransition = errors.New("invalid state transition")
)

// TransitionResult makes duplicate webhook/worker delivery explicit: a
// same-state transition is accepted as an idempotent replay with Changed=false.
type TransitionResult[T ~string] struct {
	Status  T
	Changed bool
}

// Advance validates an order transition. It has no side effects and must be
// called while the durable order row is locked by the application transaction.
func Advance(current, target Status) (TransitionResult[Status], error) {
	if !validStatus(current) || !validStatus(target) {
		return TransitionResult[Status]{}, ErrInvalidStatus
	}
	if current == target {
		return TransitionResult[Status]{Status: current}, nil
	}
	if !allowedOrderTransition(current, target) {
		return TransitionResult[Status]{}, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current, target)
	}
	return TransitionResult[Status]{Status: target, Changed: true}, nil
}

// AdvancePayment validates an observed provider payment status. Providers may
// deliver events out of order; callers should reconcile ambiguous/unknown facts
// before asking to move a durable payment row.
func AdvancePayment(current, target PaymentStatus) (TransitionResult[PaymentStatus], error) {
	if !validPaymentStatus(current) || !validPaymentStatus(target) {
		return TransitionResult[PaymentStatus]{}, ErrInvalidPayment
	}
	if current == target {
		return TransitionResult[PaymentStatus]{Status: current}, nil
	}
	if !allowedPaymentTransition(current, target) {
		return TransitionResult[PaymentStatus]{}, fmt.Errorf("%w: payment %s -> %s", ErrInvalidTransition, current, target)
	}
	return TransitionResult[PaymentStatus]{Status: target, Changed: true}, nil
}

func validStatus(value Status) bool {
	switch value {
	case StatusDraft, StatusPaymentPending, StatusConfirmed, StatusPaymentFailed, StatusCancelled,
		StatusReconciliationRequired, StatusPartiallyRefunded, StatusRefunded, StatusFulfilmentIssue:
		return true
	default:
		return false
	}
}

func allowedOrderTransition(current, target Status) bool {
	switch current {
	case StatusDraft:
		return target == StatusPaymentPending || target == StatusCancelled
	case StatusPaymentPending:
		return target == StatusConfirmed || target == StatusPaymentFailed || target == StatusCancelled || target == StatusReconciliationRequired
	case StatusConfirmed:
		return target == StatusPartiallyRefunded || target == StatusFulfilmentIssue
	case StatusPartiallyRefunded:
		return target == StatusRefunded
	case StatusPaymentFailed, StatusCancelled, StatusReconciliationRequired, StatusRefunded, StatusFulfilmentIssue:
		return false
	default:
		return false
	}
}

func validPaymentStatus(value PaymentStatus) bool {
	switch value {
	case PaymentCreated, PaymentRequiresAction, PaymentProcessing, PaymentAuthorized, PaymentCaptured, PaymentFailed, PaymentCancelled, PaymentUnknown:
		return true
	default:
		return false
	}
}

func allowedPaymentTransition(current, target PaymentStatus) bool {
	switch current {
	case PaymentCreated:
		return target == PaymentRequiresAction || target == PaymentProcessing || target == PaymentAuthorized || target == PaymentCaptured || target == PaymentFailed || target == PaymentCancelled || target == PaymentUnknown
	case PaymentRequiresAction:
		return target == PaymentProcessing || target == PaymentAuthorized || target == PaymentCaptured || target == PaymentFailed || target == PaymentCancelled || target == PaymentUnknown
	case PaymentProcessing:
		return target == PaymentAuthorized || target == PaymentCaptured || target == PaymentFailed || target == PaymentCancelled || target == PaymentUnknown
	case PaymentAuthorized:
		return target == PaymentCaptured || target == PaymentFailed || target == PaymentCancelled || target == PaymentUnknown
	case PaymentUnknown:
		return target == PaymentProcessing || target == PaymentAuthorized || target == PaymentCaptured || target == PaymentFailed || target == PaymentCancelled
	case PaymentCaptured, PaymentFailed, PaymentCancelled:
		return false
	default:
		return false
	}
}
