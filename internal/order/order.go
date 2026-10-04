package order

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/money"
	"github.com/HK9750/venueos/internal/pricing"
)

var (
	ErrInvalidOrder    = errors.New("invalid order")
	ErrNotFound        = errors.New("order not found")
	ErrOrderExists     = errors.New("an order already exists for the cart or hold")
	ErrCartNotActive   = errors.New("cart is not active")
	ErrHoldNotActive   = errors.New("order hold is not active")
	ErrOwnerMismatch   = errors.New("order owner does not match")
	ErrVersionConflict = errors.New("order version conflict")
)

// Order is the durable commercial aggregate created from one immutable cart
// snapshot. Customer contact and provider-specific fields are intentionally
// absent until the documented identity/provider decisions are resolved.
type Order struct {
	ID             identifier.ID
	OrganizationID identifier.ID
	CartID         identifier.ID
	SessionID      identifier.ID
	HoldID         identifier.ID
	OrderNumber    string
	OwnerTokenHash [32]byte
	OwnerUserID    *identifier.ID
	Currency       string
	Status         Status
	SubtotalMinor  int64
	DiscountMinor  int64
	FeesMinor      int64
	TaxesMinor     int64
	TotalMinor     int64
	QuoteSnapshot  []byte
	QuoteSHA256    [32]byte
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ConfirmedAt    *time.Time
	Lines          []Line
}

type Line struct {
	LineNumber    int32
	PriceTierID   identifier.ID
	Quantity      int64
	UnitMinor     int64
	SubtotalMinor int64
	Snapshot      []byte
}

type CreateFromCartInput struct {
	ID             identifier.ID
	OrganizationID identifier.ID
	CartID         identifier.ID
	SessionID      identifier.ID
	HoldID         identifier.ID
	OrderNumber    string
	OwnerTokenHash [32]byte
	OwnerUserID    *identifier.ID
	Currency       string
	QuoteSnapshot  []byte
	QuoteSHA256    [32]byte
	Now            time.Time
}

type CreateFromCartRecord struct {
	Order        Order
	AuditID      identifier.ID
	OutboxID     identifier.ID
	CartAuditID  identifier.ID
	CartOutboxID identifier.ID
	ActorType    string
	ActorID      string
}

type Repository interface {
	CreateFromCart(context.Context, CreateFromCartRecord) (Order, error)
	Get(context.Context, identifier.ID, identifier.ID, [32]byte) (Order, error)
}

// BuildFromCart validates and freezes a cart's quote without contacting a
// provider. The repository must still lock the cart and hold before calling
// this constructor, and must use database time for the resulting timestamps.
func BuildFromCart(input CreateFromCartInput) (Order, error) {
	if input.ID.IsZero() || input.OrganizationID.IsZero() || input.CartID.IsZero() || input.SessionID.IsZero() || input.HoldID.IsZero() {
		return Order{}, fmt.Errorf("%w: identifiers are required", ErrInvalidOrder)
	}
	if input.OwnerTokenHash == [32]byte{} {
		return Order{}, fmt.Errorf("%w: owner token hash is required", ErrInvalidOrder)
	}
	if input.OwnerUserID != nil && input.OwnerUserID.IsZero() {
		return Order{}, fmt.Errorf("%w: owner user ID is invalid", ErrInvalidOrder)
	}
	orderNumber := strings.TrimSpace(input.OrderNumber)
	if !validOrderNumber(orderNumber) {
		return Order{}, fmt.Errorf("%w: order number is invalid", ErrInvalidOrder)
	}
	currency := strings.ToUpper(strings.TrimSpace(input.Currency))
	if _, err := money.NewCurrency(currency); err != nil {
		return Order{}, fmt.Errorf("%w: currency is invalid", ErrInvalidOrder)
	}
	if input.Now.IsZero() {
		return Order{}, fmt.Errorf("%w: current time is required", ErrInvalidOrder)
	}
	if len(input.QuoteSnapshot) < 2 || len(input.QuoteSnapshot) > 262144 || input.QuoteSHA256 == [32]byte{} {
		return Order{}, fmt.Errorf("%w: quote snapshot is invalid", ErrInvalidOrder)
	}
	digest := sha256.Sum256(input.QuoteSnapshot)
	if subtle.ConstantTimeCompare(input.QuoteSHA256[:], digest[:]) != 1 {
		return Order{}, fmt.Errorf("%w: quote snapshot digest does not match", ErrInvalidOrder)
	}
	snapshot, err := pricing.ParseSnapshot(input.QuoteSnapshot)
	if err != nil {
		return Order{}, fmt.Errorf("%w: %w", ErrInvalidOrder, err)
	}
	if snapshot.Currency.String() != currency {
		return Order{}, fmt.Errorf("%w: quote currency does not match order", ErrInvalidOrder)
	}
	lines := make([]Line, 0, len(snapshot.Lines))
	for index, source := range snapshot.Lines {
		lineNumber := int32(index + 1)
		lineSnapshot, marshalErr := json.Marshal(struct {
			Version       int    `json:"version"`
			PriceTierID   string `json:"price_tier_id"`
			Quantity      int64  `json:"quantity"`
			UnitMinor     int64  `json:"unit_minor"`
			SubtotalMinor int64  `json:"subtotal_minor"`
		}{Version: 1, PriceTierID: source.PriceTierID.String(), Quantity: source.Quantity, UnitMinor: source.UnitMinor, SubtotalMinor: source.SubtotalMinor})
		if marshalErr != nil {
			return Order{}, fmt.Errorf("%w: line snapshot: %w", ErrInvalidOrder, marshalErr)
		}
		lines = append(lines, Line{LineNumber: lineNumber, PriceTierID: source.PriceTierID, Quantity: source.Quantity, UnitMinor: source.UnitMinor, SubtotalMinor: source.SubtotalMinor, Snapshot: lineSnapshot})
	}
	feesMinor, err := adjustmentTotal(snapshot.Fees, snapshot.Currency)
	if err != nil {
		return Order{}, fmt.Errorf("%w: fees: %w", ErrInvalidOrder, err)
	}
	taxesMinor, err := adjustmentTotal(snapshot.Taxes, snapshot.Currency)
	if err != nil {
		return Order{}, fmt.Errorf("%w: taxes: %w", ErrInvalidOrder, err)
	}
	now := input.Now.UTC()
	return Order{
		ID: input.ID, OrganizationID: input.OrganizationID, CartID: input.CartID, SessionID: input.SessionID, HoldID: input.HoldID,
		OrderNumber: orderNumber, OwnerTokenHash: input.OwnerTokenHash, OwnerUserID: cloneID(input.OwnerUserID), Currency: currency,
		Status: StatusPaymentPending, SubtotalMinor: lineTotal(lines, snapshot.Currency), DiscountMinor: snapshot.DiscountMinor, FeesMinor: feesMinor,
		TaxesMinor: taxesMinor, TotalMinor: snapshot.TotalMinor, QuoteSnapshot: append([]byte(nil), input.QuoteSnapshot...), QuoteSHA256: input.QuoteSHA256,
		Version: 1, CreatedAt: now, UpdatedAt: now, Lines: lines,
	}, nil
}

func adjustmentTotal(values []pricing.SnapshotAdjustment, currency money.Currency) (int64, error) {
	var total money.Money
	for index, value := range values {
		amount, err := money.New(value.AmountMinor, currency)
		if err != nil {
			return 0, fmt.Errorf("adjustment %d: %w", index, err)
		}
		if index == 0 {
			total = amount
			continue
		}
		total, err = total.Add(amount)
		if err != nil {
			return 0, err
		}
	}
	return total.MinorUnits(), nil
}

func lineTotal(lines []Line, currency money.Currency) int64 {
	if len(lines) == 0 {
		return 0
	}
	var total money.Money
	for index, line := range lines {
		value, _ := money.New(line.SubtotalMinor, currency)
		if index == 0 {
			total = value
		} else {
			total, _ = total.Add(value)
		}
	}
	return total.MinorUnits()
}

func validOrderNumber(value string) bool {
	if len(value) < 8 || len(value) > 64 {
		return false
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' {
			continue
		}
		return false
	}
	return true
}

func cloneID(value *identifier.ID) *identifier.ID {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}
