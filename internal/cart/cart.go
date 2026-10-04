// Package cart owns the durable one-session cart boundary. Customer identity,
// tax/fee policy, and payment-provider state are deliberately outside this
// slice until their documented decisions are made.
package cart

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/money"
	"github.com/HK9750/venueos/internal/pricing"
)

type Status string

const (
	StatusActive     Status = "active"
	StatusCheckedOut Status = "checked_out"
	StatusExpired    Status = "expired"
	StatusCancelled  Status = "cancelled"
)

var (
	ErrInvalidCart      = errors.New("invalid cart")
	ErrNotFound         = errors.New("cart not found")
	ErrActiveCartExists = errors.New("an active cart already exists for the hold")
	ErrHoldNotActive    = errors.New("cart hold is not active")
	ErrVersionConflict  = errors.New("cart version conflict")
	ErrOwnerMismatch    = errors.New("cart owner does not match")
)

// Cart stores a PII-free commercial quote snapshot and the owner hash used by
// the hold. The durable database row remains the authority; this value is a
// repository/application transfer object.
type Cart struct {
	ID             identifier.ID
	OrganizationID identifier.ID
	SessionID      identifier.ID
	HoldID         identifier.ID
	OwnerTokenHash [32]byte
	OwnerUserID    *identifier.ID
	Currency       string
	Status         Status
	QuoteSnapshot  []byte
	QuoteSHA256    [32]byte
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type CreateInput struct {
	ID             identifier.ID
	OrganizationID identifier.ID
	SessionID      identifier.ID
	HoldID         identifier.ID
	OwnerTokenHash [32]byte
	OwnerUserID    *identifier.ID
	Currency       string
	Quote          pricing.Quote
	Now            time.Time
}

type UpdateQuoteInput struct {
	ExpectedVersion int64
	OwnerTokenHash  [32]byte
	Quote           pricing.Quote
	Now             time.Time
}

type CreateRecord struct {
	Cart      Cart
	AuditID   identifier.ID
	OutboxID  identifier.ID
	ActorType string
	ActorID   string
}

type UpdateQuoteRecord struct {
	OrganizationID  identifier.ID
	CartID          identifier.ID
	OwnerTokenHash  [32]byte
	ExpectedVersion int64
	Quote           pricing.Quote
	AuditID         identifier.ID
	OutboxID        identifier.ID
	ActorType       string
	ActorID         string
	OccurredAt      time.Time
}

type Repository interface {
	Create(context.Context, CreateRecord) (Cart, error)
	Get(context.Context, identifier.ID, identifier.ID, [32]byte) (Cart, error)
	UpdateQuote(context.Context, UpdateQuoteRecord) (Cart, error)
}

// Build validates a new active cart and freezes its quote representation.
func Build(input CreateInput) (Cart, error) {
	if input.ID.IsZero() || input.OrganizationID.IsZero() || input.SessionID.IsZero() || input.HoldID.IsZero() {
		return Cart{}, fmt.Errorf("%w: identifiers are required", ErrInvalidCart)
	}
	if input.OwnerTokenHash == [32]byte{} {
		return Cart{}, fmt.Errorf("%w: owner token hash is required", ErrInvalidCart)
	}
	if input.OwnerUserID != nil && input.OwnerUserID.IsZero() {
		return Cart{}, fmt.Errorf("%w: owner user ID is invalid", ErrInvalidCart)
	}
	currency := strings.ToUpper(strings.TrimSpace(input.Currency))
	if _, err := money.NewCurrency(currency); err != nil {
		return Cart{}, fmt.Errorf("%w: currency: %w", ErrInvalidCart, err)
	}
	if input.Now.IsZero() {
		return Cart{}, fmt.Errorf("%w: current time is required", ErrInvalidCart)
	}
	snapshot, err := snapshotForCurrency(input.Quote, currency)
	if err != nil {
		return Cart{}, err
	}
	now := input.Now.UTC()
	return Cart{ID: input.ID, OrganizationID: input.OrganizationID, SessionID: input.SessionID, HoldID: input.HoldID, OwnerTokenHash: input.OwnerTokenHash, OwnerUserID: cloneID(input.OwnerUserID), Currency: currency, Status: StatusActive, QuoteSnapshot: snapshot, QuoteSHA256: input.Quote.SnapshotSHA256, Version: 1, CreatedAt: now, UpdatedAt: now}, nil
}

// UpdateQuote applies an optimistic, owner-checked commercial snapshot change.
// It does not extend the hold; renewal remains a separate inventory command.
func (cart Cart) UpdateQuote(input UpdateQuoteInput) (Cart, error) {
	if cart.ID.IsZero() || cart.OrganizationID.IsZero() || cart.SessionID.IsZero() || cart.HoldID.IsZero() || cart.Version < 1 {
		return Cart{}, fmt.Errorf("%w: current cart is invalid", ErrInvalidCart)
	}
	if input.ExpectedVersion != cart.Version {
		return Cart{}, ErrVersionConflict
	}
	if subtle.ConstantTimeCompare(cart.OwnerTokenHash[:], input.OwnerTokenHash[:]) != 1 {
		return Cart{}, ErrOwnerMismatch
	}
	if cart.Status != StatusActive {
		return Cart{}, ErrInvalidCart
	}
	if input.Now.IsZero() || input.Now.Before(cart.UpdatedAt) {
		return Cart{}, fmt.Errorf("%w: update time is invalid", ErrInvalidCart)
	}
	snapshot, err := snapshotForCurrency(input.Quote, cart.Currency)
	if err != nil {
		return Cart{}, err
	}
	next := cart
	next.QuoteSnapshot = snapshot
	next.QuoteSHA256 = input.Quote.SnapshotSHA256
	next.Version++
	next.UpdatedAt = input.Now.UTC()
	return next, nil
}

func snapshotForCurrency(quote pricing.Quote, currency string) ([]byte, error) {
	if quote.Currency.String() != currency {
		return nil, money.ErrCurrencyMismatch
	}
	snapshot, err := quote.SnapshotJSON()
	if errors.Is(err, pricing.ErrQuoteMutated) {
		return nil, fmt.Errorf("%w: quote snapshot is not stable", ErrInvalidCart)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: quote snapshot: %w", ErrInvalidCart, err)
	}
	if len(snapshot) < 2 || len(snapshot) > 262144 {
		return nil, fmt.Errorf("%w: quote snapshot size is invalid", ErrInvalidCart)
	}
	return append([]byte(nil), snapshot...), nil
}

func cloneID(value *identifier.ID) *identifier.ID {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}
