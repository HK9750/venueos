package order

import (
	"errors"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/money"
	"github.com/HK9750/venueos/internal/pricing"
)

func TestBuildFromCartFreezesValidatedQuoteLines(t *testing.T) {
	usd, _ := money.NewCurrency("USD")
	price, _ := money.New(1250, usd)
	priceTierID := mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	quote, err := pricing.CalculateQuote(pricing.QuoteInput{Lines: []pricing.LineInput{{PriceTierID: priceTierID, Quantity: 2, UnitPrice: price}}})
	if err != nil {
		t.Fatalf("CalculateQuote() error = %v", err)
	}
	organizationID := mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	orderValue, err := BuildFromCart(CreateFromCartInput{
		ID: mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7"), OrganizationID: organizationID,
		CartID: mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef8"), SessionID: mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef9"), HoldID: mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394efa"),
		OrderNumber: "VO-01ABCDEF", OwnerTokenHash: [32]byte{1}, Currency: "USD", QuoteSnapshot: mustOrderSnapshot(t, quote), QuoteSHA256: quote.SnapshotSHA256, Now: time.Date(2026, 10, 3, 12, 0, 0, 0, time.FixedZone("PKT", 5*60*60)),
	})
	if err != nil {
		t.Fatalf("BuildFromCart() error = %v", err)
	}
	if orderValue.Status != StatusPaymentPending || orderValue.Currency != "USD" || orderValue.SubtotalMinor != 2500 || orderValue.TotalMinor != 2500 || len(orderValue.Lines) != 1 || orderValue.Lines[0].Quantity != 2 {
		t.Fatalf("order = %#v", orderValue)
	}
	if orderValue.CreatedAt.Location() != time.UTC || orderValue.UpdatedAt.Location() != time.UTC {
		t.Fatalf("timestamps were not normalized: %#v", orderValue)
	}
}

func TestBuildFromCartRejectsDigestAndSnapshotTampering(t *testing.T) {
	usd, _ := money.NewCurrency("USD")
	price, _ := money.New(100, usd)
	quote, err := pricing.CalculateQuote(pricing.QuoteInput{Lines: []pricing.LineInput{{PriceTierID: mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5"), Quantity: 1, UnitPrice: price}}})
	if err != nil {
		t.Fatalf("CalculateQuote() error = %v", err)
	}
	snapshot := mustOrderSnapshot(t, quote)
	snapshot[len(snapshot)-2] = '9'
	_, err = BuildFromCart(CreateFromCartInput{ID: mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6"), OrganizationID: mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7"), CartID: mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef8"), SessionID: mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef9"), HoldID: mustOrderID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394efa"), OrderNumber: "VO-01ABCDEF", OwnerTokenHash: [32]byte{1}, Currency: "USD", QuoteSnapshot: snapshot, QuoteSHA256: quote.SnapshotSHA256, Now: time.Now()})
	if !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("tampered snapshot error = %v, want ErrInvalidOrder", err)
	}
}

func mustOrderID(t *testing.T, value string) identifier.ID {
	t.Helper()
	id, err := identifier.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", value, err)
	}
	return id
}

func mustOrderSnapshot(t *testing.T, quote pricing.Quote) []byte {
	t.Helper()
	snapshot, err := quote.SnapshotJSON()
	if err != nil {
		t.Fatalf("SnapshotJSON() error = %v", err)
	}
	return snapshot
}
