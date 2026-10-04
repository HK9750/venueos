// Package payment defines the provider-neutral payment port. It carries
// durable business references and sanitized provider facts, never raw payment
// instruments or provider SDK types.
package payment

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HK9750/venueos/internal/order"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/money"
)

type Status = order.PaymentStatus

const (
	StatusCreated        = order.PaymentCreated
	StatusRequiresAction = order.PaymentRequiresAction
	StatusProcessing     = order.PaymentProcessing
	StatusAuthorized     = order.PaymentAuthorized
	StatusCaptured       = order.PaymentCaptured
	StatusFailed         = order.PaymentFailed
	StatusCancelled      = order.PaymentCancelled
	StatusUnknown        = order.PaymentUnknown
)

type FailureClass string

const (
	FailurePermanent FailureClass = "permanent"
	FailureTransient FailureClass = "transient"
	FailureUnknown   FailureClass = "unknown"
)

var (
	ErrInvalidRequest   = errors.New("invalid payment request")
	ErrInvalidEvent     = errors.New("invalid payment webhook event")
	ErrProviderFailure  = errors.New("payment provider failure")
	ErrInvalidAttempt   = errors.New("invalid payment attempt")
	ErrNotFound         = errors.New("payment attempt not found")
	ErrAttemptExists    = errors.New("payment attempt already exists")
	ErrVersionConflict  = errors.New("payment attempt version conflict")
	ErrProviderMismatch = errors.New("payment provider does not match attempt")
	ErrAmountMismatch   = errors.New("payment amount or currency does not match attempt")
)

// CreateIntentRequest contains only the minimum provider-neutral intent facts.
// Provider account selection and capture mode are adapter/application policy.
type CreateIntentRequest struct {
	OrganizationID identifier.ID
	OrderID        identifier.ID
	AmountMinor    int64
	Currency       string
	IdempotencyKey string
}

func (request CreateIntentRequest) Normalize() (CreateIntentRequest, error) {
	request.Currency = normalizeCurrency(request.Currency)
	if request.OrganizationID.IsZero() || request.OrderID.IsZero() || request.AmountMinor < 1 {
		return CreateIntentRequest{}, fmt.Errorf("%w: organization, order, and positive amount are required", ErrInvalidRequest)
	}
	if _, err := money.NewCurrency(request.Currency); err != nil {
		return CreateIntentRequest{}, fmt.Errorf("%w: currency: %w", ErrInvalidRequest, err)
	}
	if !validReference(request.IdempotencyKey, 16, 128) {
		return CreateIntentRequest{}, fmt.Errorf("%w: idempotency key is invalid", ErrInvalidRequest)
	}
	return request, nil
}

type RefundRequest struct {
	OrganizationID   identifier.ID
	OrderID          identifier.ID
	PaymentReference string
	AmountMinor      int64
	Currency         string
	IdempotencyKey   string
}

func (request RefundRequest) Normalize() (RefundRequest, error) {
	request.Currency = normalizeCurrency(request.Currency)
	if request.OrganizationID.IsZero() || request.OrderID.IsZero() || request.AmountMinor < 1 {
		return RefundRequest{}, fmt.Errorf("%w: organization, order, and positive amount are required", ErrInvalidRequest)
	}
	if _, err := money.NewCurrency(request.Currency); err != nil {
		return RefundRequest{}, fmt.Errorf("%w: currency: %w", ErrInvalidRequest, err)
	}
	if !validReference(request.PaymentReference, 1, 255) || !validReference(request.IdempotencyKey, 16, 128) {
		return RefundRequest{}, fmt.Errorf("%w: payment and idempotency references are invalid", ErrInvalidRequest)
	}
	return request, nil
}

// Intent is the sanitized result stored by the application. ClientAction is an
// opaque provider response reference and must never be logged or put in audit
// metadata; raw provider secrets do not belong in this type.
type Intent struct {
	Provider              string
	ProviderIntentID      string
	Status                Status
	AmountMinor           int64
	Currency              string
	ClientActionReference string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// RefundResult is the sanitized provider response for a refund command. It is
// separate from Intent because refund state and provider object identity have a
// different lifecycle from payment authorization/capture.
type RefundResult struct {
	Provider         string
	ProviderRefundID string
	State            string
	AmountMinor      int64
	Currency         string
	FailureClass     FailureClass
	FailureCode      string
	FailureMessage   string
}

const (
	RefundStateProcessing = "processing"
	RefundStateSucceeded  = "succeeded"
	RefundStateFailed     = "failed"
)

// Attempt is the sanitized durable payment-attempt record. It never contains
// card data or an unbounded provider response body.
type Attempt struct {
	ID                    identifier.ID
	OrganizationID        identifier.ID
	OrderID               identifier.ID
	Provider              string
	ProviderObjectID      string
	AmountMinor           int64
	Currency              string
	Status                Status
	IdempotencyKey        string
	FailureClass          FailureClass
	FailureCode           string
	FailureMessage        string
	ClientActionReference string
	Metadata              []byte
	Version               int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// MetadataHash is safe to place in audit/outbox payloads when an operator
// needs correlation without copying provider metadata into durable logs.
func (attempt Attempt) MetadataHash() [32]byte { return sha256.Sum256(attempt.Metadata) }

func (attempt Attempt) Validate() error { return validateAttempt(attempt) }

type CreateAttemptInput struct {
	ID             identifier.ID
	OrganizationID identifier.ID
	OrderID        identifier.ID
	Provider       string
	AmountMinor    int64
	Currency       string
	IdempotencyKey string
	Now            time.Time
}

type CreateAttemptRecord struct {
	Attempt   Attempt
	AuditID   identifier.ID
	OutboxID  identifier.ID
	ActorType string
	ActorID   string
}

type ApplyIntentRecord struct {
	OrganizationID  identifier.ID
	AttemptID       identifier.ID
	ExpectedVersion int64
	Intent          Intent
	AuditID         identifier.ID
	OutboxID        identifier.ID
	ActorType       string
	ActorID         string
}

type ApplyWebhookRecord struct {
	OrganizationID  identifier.ID
	AttemptID       identifier.ID
	ExpectedVersion int64
	Event           WebhookEvent
	AuditID         identifier.ID
	OutboxID        identifier.ID
	ActorType       string
	ActorID         string
}

type Repository interface {
	Create(context.Context, CreateAttemptRecord) (Attempt, error)
	ApplyIntent(context.Context, ApplyIntentRecord) (Attempt, error)
	ApplyWebhook(context.Context, ApplyWebhookRecord) (Attempt, error)
	Get(context.Context, identifier.ID, identifier.ID) (Attempt, error)
}

// BuildAttempt creates the local durable record before or alongside provider
// intent creation. The remote call itself belongs outside the database tx.
func BuildAttempt(input CreateAttemptInput) (Attempt, error) {
	request, err := (CreateIntentRequest{OrganizationID: input.OrganizationID, OrderID: input.OrderID, AmountMinor: input.AmountMinor, Currency: input.Currency, IdempotencyKey: input.IdempotencyKey}).Normalize()
	if err != nil {
		return Attempt{}, err
	}
	provider := strings.ToLower(strings.TrimSpace(input.Provider))
	if !validReference(provider, 2, 64) || input.ID.IsZero() || input.Now.IsZero() {
		return Attempt{}, fmt.Errorf("%w: ID, provider, and current time are required", ErrInvalidAttempt)
	}
	now := input.Now.UTC()
	return Attempt{ID: input.ID, OrganizationID: request.OrganizationID, OrderID: request.OrderID, Provider: provider, AmountMinor: request.AmountMinor, Currency: request.Currency, Status: StatusCreated, IdempotencyKey: request.IdempotencyKey, Version: 1, CreatedAt: now, UpdatedAt: now, Metadata: []byte(`{}`)}, nil
}

func (attempt Attempt) ApplyIntent(intent Intent, expectedVersion int64, now time.Time) (Attempt, error) {
	if err := validateAttempt(attempt); err != nil {
		return Attempt{}, err
	}
	if expectedVersion != attempt.Version {
		return Attempt{}, ErrVersionConflict
	}
	if strings.ToLower(strings.TrimSpace(intent.Provider)) != attempt.Provider {
		return Attempt{}, ErrProviderMismatch
	}
	if !validReference(intent.ProviderIntentID, 1, 255) || intent.AmountMinor != attempt.AmountMinor || normalizeCurrency(intent.Currency) != attempt.Currency {
		return Attempt{}, ErrAmountMismatch
	}
	if intent.Status == "" {
		return Attempt{}, fmt.Errorf("%w: intent status is required", ErrInvalidAttempt)
	}
	if intent.ClientActionReference != "" && !validReference(intent.ClientActionReference, 1, 1000) {
		return Attempt{}, fmt.Errorf("%w: client action reference is invalid", ErrInvalidAttempt)
	}
	transition, err := order.AdvancePayment(attempt.Status, intent.Status)
	if err != nil {
		return Attempt{}, err
	}
	if !intent.CreatedAt.IsZero() && intent.CreatedAt.Before(attempt.CreatedAt) {
		return Attempt{}, fmt.Errorf("%w: intent time precedes attempt", ErrInvalidAttempt)
	}
	if now.IsZero() || now.Before(attempt.UpdatedAt) {
		return Attempt{}, fmt.Errorf("%w: update time is invalid", ErrInvalidAttempt)
	}
	if !transition.Changed && attempt.ProviderObjectID == intent.ProviderIntentID && attempt.ClientActionReference == intent.ClientActionReference {
		return attempt, nil
	}
	next := attempt
	next.ProviderObjectID = intent.ProviderIntentID
	next.Status = transition.Status
	next.ClientActionReference = intent.ClientActionReference
	next.Version++
	next.UpdatedAt = now.UTC()
	return next, nil
}

func (attempt Attempt) ApplyWebhook(event WebhookEvent, expectedVersion int64, now time.Time) (Attempt, error) {
	if err := validateAttempt(attempt); err != nil {
		return Attempt{}, err
	}
	if expectedVersion != attempt.Version {
		return Attempt{}, ErrVersionConflict
	}
	if err := event.Validate(); err != nil {
		return Attempt{}, err
	}
	if strings.ToLower(strings.TrimSpace(event.Provider)) != attempt.Provider || (attempt.ProviderObjectID != "" && event.ProviderObjectID != attempt.ProviderObjectID) {
		return Attempt{}, ErrProviderMismatch
	}
	if event.Status == nil {
		return Attempt{}, fmt.Errorf("%w: webhook status is required", ErrInvalidEvent)
	}
	if event.AmountMinor != nil && *event.AmountMinor != attempt.AmountMinor {
		return Attempt{}, ErrAmountMismatch
	}
	if event.Currency != "" && normalizeCurrency(event.Currency) != attempt.Currency {
		return Attempt{}, ErrAmountMismatch
	}
	transition, err := order.AdvancePayment(attempt.Status, *event.Status)
	if err != nil {
		return Attempt{}, err
	}
	if now.IsZero() || now.Before(attempt.UpdatedAt) {
		return Attempt{}, fmt.Errorf("%w: update time is invalid", ErrInvalidAttempt)
	}
	if !transition.Changed && attempt.ProviderObjectID == event.ProviderObjectID {
		return attempt, nil
	}
	next := attempt
	next.ProviderObjectID = event.ProviderObjectID
	next.Status = transition.Status
	next.Version++
	next.UpdatedAt = now.UTC()
	return next, nil
}

func validateAttempt(attempt Attempt) error {
	if attempt.ID.IsZero() || attempt.OrganizationID.IsZero() || attempt.OrderID.IsZero() || !validReference(attempt.Provider, 2, 64) || attempt.AmountMinor < 1 || attempt.Version < 1 || attempt.CreatedAt.IsZero() || attempt.UpdatedAt.IsZero() || attempt.UpdatedAt.Before(attempt.CreatedAt) {
		return fmt.Errorf("%w: attempt fields are invalid", ErrInvalidAttempt)
	}
	if _, err := money.NewCurrency(attempt.Currency); err != nil || !validPaymentStatus(attempt.Status) || !validReference(attempt.IdempotencyKey, 16, 128) {
		return fmt.Errorf("%w: attempt currency, status, or idempotency is invalid", ErrInvalidAttempt)
	}
	return nil
}

// WebhookEvent is the adapter's verified, deduplicated-ready result. Raw body
// bytes remain in an encrypted/reference-backed inbox payload, not this event.
type WebhookEvent struct {
	Provider         string
	EventID          string
	EventType        string
	ProviderObjectID string
	PayloadReference string
	PayloadSHA256    [32]byte
	OccurredAt       time.Time
	Status           *Status
	AmountMinor      *int64
	Currency         string
}

func (event WebhookEvent) Validate() error {
	if !validReference(event.Provider, 1, 64) || !validReference(event.EventID, 1, 255) || !validEventType(event.EventType) || !validReference(event.ProviderObjectID, 1, 255) {
		return fmt.Errorf("%w: provider/event references are invalid", ErrInvalidEvent)
	}
	if event.PayloadSHA256 == [32]byte{} || event.OccurredAt.IsZero() {
		return fmt.Errorf("%w: payload hash and occurrence time are required", ErrInvalidEvent)
	}
	if event.PayloadReference != "" && !validPayloadReference(event.PayloadReference) {
		return fmt.Errorf("%w: payload reference is invalid", ErrInvalidEvent)
	}
	if event.Status != nil && !validPaymentStatus(*event.Status) {
		return fmt.Errorf("%w: payment status is invalid", ErrInvalidEvent)
	}
	if event.AmountMinor != nil && *event.AmountMinor < 0 {
		return fmt.Errorf("%w: amount cannot be negative", ErrInvalidEvent)
	}
	if event.Currency != "" {
		if _, err := money.NewCurrency(normalizeCurrency(event.Currency)); err != nil {
			return fmt.Errorf("%w: currency: %w", ErrInvalidEvent, err)
		}
	}
	return nil
}

// Adapter is implemented by Stripe or another provider adapter. Implementations
// must set explicit network deadlines, map provider errors to Failure, and make
// every mutation idempotent with the supplied key.
type Adapter interface {
	Name() string
	CreateIntent(context.Context, CreateIntentRequest) (Intent, error)
	RetrieveIntent(context.Context, string) (Intent, error)
	CancelIntent(context.Context, string) (Intent, error)
	CreateRefund(context.Context, RefundRequest) (RefundResult, error)
	VerifyWebhook(context.Context, []byte, map[string]string) (WebhookEvent, error)
}

type Failure struct {
	Class       FailureClass
	Code        string
	SafeMessage string
	RetryAfter  time.Duration
	Cause       error
}

func (failure Failure) Error() string {
	if failure.SafeMessage == "" {
		return string(failure.Class)
	}
	return failure.SafeMessage
}

func (failure Failure) Unwrap() error { return failure.Cause }

func (failure Failure) Valid() bool {
	return (failure.Class == FailurePermanent || failure.Class == FailureTransient || failure.Class == FailureUnknown) && validReference(failure.Code, 1, 64) && len(failure.SafeMessage) >= 1 && len(failure.SafeMessage) <= 1000 && failure.RetryAfter >= 0
}

// Classify converts untrusted adapter errors into a bounded, operator-safe
// failure. Provider response bodies and secrets are never copied into the
// returned message.
func Classify(err error) Failure {
	if err == nil {
		return Failure{Class: FailureUnknown, Code: "none", SafeMessage: "no failure"}
	}
	var failure Failure
	if errors.As(err, &failure) && failure.Valid() {
		failure.SafeMessage = truncate(failure.SafeMessage, 1000)
		return failure
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return Failure{Class: FailureTransient, Code: "timeout", SafeMessage: "The payment provider did not respond in time.", Cause: err}
	}
	return Failure{Class: FailureUnknown, Code: "provider_error", SafeMessage: "The payment provider returned an unclassified error.", Cause: err}
}

func PayloadHash(payload []byte) [32]byte { return sha256.Sum256(payload) }

func normalizeCurrency(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func validPaymentStatus(value Status) bool {
	switch value {
	case StatusCreated, StatusRequiresAction, StatusProcessing, StatusAuthorized, StatusCaptured, StatusFailed, StatusCancelled, StatusUnknown:
		return true
	default:
		return false
	}
}

func validEventType(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func validReference(value string, minimum, maximum int) bool {
	if len(value) < minimum || len(value) > maximum {
		return false
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == ':' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func truncate(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}
