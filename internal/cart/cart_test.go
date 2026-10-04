package cart

import (
	"errors"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/money"
	"github.com/HK9750/venueos/internal/pricing"
)

func TestBuildAndUpdateCartFreezesOwnerScopedQuote(t *testing.T) {
	usd, _ := money.NewCurrency("USD")
	price, _ := money.New(1250, usd)
	tierID := mustCartID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	quote, err := pricing.CalculateQuote(pricing.QuoteInput{Lines: []pricing.LineInput{{PriceTierID: tierID, Quantity: 1, UnitPrice: price}}})
	if err != nil {
		t.Fatalf("CalculateQuote() error = %v", err)
	}
	owner := [32]byte{1}
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	cart, err := Build(CreateInput{ID: mustCartID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6"), OrganizationID: mustCartID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7"), SessionID: mustCartID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef8"), HoldID: mustCartID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef9"), OwnerTokenHash: owner, Currency: " usd ", Quote: quote, Now: now})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if cart.Status != StatusActive || cart.Version != 1 || cart.Currency != "USD" || len(cart.QuoteSnapshot) == 0 || cart.QuoteSHA256 != quote.SnapshotSHA256 {
		t.Fatalf("cart = %#v", cart)
	}
	updated, err := cart.UpdateQuote(UpdateQuoteInput{ExpectedVersion: 1, OwnerTokenHash: owner, Quote: quote, Now: now.Add(time.Second)})
	if err != nil {
		t.Fatalf("UpdateQuote() error = %v", err)
	}
	if updated.Version != 2 || !updated.UpdatedAt.Equal(now.Add(time.Second)) || updated.Status != StatusActive {
		t.Fatalf("updated cart = %#v", updated)
	}
	if _, err := cart.UpdateQuote(UpdateQuoteInput{ExpectedVersion: 2, OwnerTokenHash: owner, Quote: quote, Now: now.Add(2 * time.Second)}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale UpdateQuote() error = %v, want ErrVersionConflict", err)
	}
	if _, err := cart.UpdateQuote(UpdateQuoteInput{ExpectedVersion: 1, OwnerTokenHash: [32]byte{2}, Quote: quote, Now: now.Add(time.Second)}); !errors.Is(err, ErrOwnerMismatch) {
		t.Fatalf("wrong owner UpdateQuote() error = %v, want ErrOwnerMismatch", err)
	}
}

func TestBuildCartRejectsUnstableOrMismatchedQuote(t *testing.T) {
	usd, _ := money.NewCurrency("USD")
	eur, _ := money.NewCurrency("EUR")
	price, _ := money.New(1000, usd)
	otherPrice, _ := money.New(1000, eur)
	tierID := mustCartID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	quote, err := pricing.CalculateQuote(pricing.QuoteInput{Lines: []pricing.LineInput{{PriceTierID: tierID, Quantity: 1, UnitPrice: price}}})
	if err != nil {
		t.Fatalf("CalculateQuote() error = %v", err)
	}
	base := CreateInput{ID: mustCartID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6"), OrganizationID: mustCartID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7"), SessionID: mustCartID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef8"), HoldID: mustCartID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef9"), OwnerTokenHash: [32]byte{1}, Currency: "USD", Quote: quote, Now: time.Now().UTC()}
	quote.Lines[0].Quantity = 2
	if _, err := Build(base); !errors.Is(err, ErrInvalidCart) {
		t.Fatalf("mutated quote Build() error = %v, want ErrInvalidCart", err)
	}
	base.Quote, err = pricing.CalculateQuote(pricing.QuoteInput{Lines: []pricing.LineInput{{PriceTierID: tierID, Quantity: 1, UnitPrice: otherPrice}}})
	if err != nil {
		t.Fatalf("EUR CalculateQuote() error = %v", err)
	}
	if _, err := Build(base); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("currency mismatch Build() error = %v, want ErrCurrencyMismatch", err)
	}
}

func mustCartID(t *testing.T, value string) identifier.ID {
	t.Helper()
	id, err := identifier.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", value, err)
	}
	return id
}
