package pricing

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/money"
)

func TestCalculateQuoteProducesCheckedCommercialSnapshot(t *testing.T) {
	usd, err := money.NewCurrency("USD")
	if err != nil {
		t.Fatalf("NewCurrency() error = %v", err)
	}
	first := mustQuoteID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	second := mustQuoteID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	firstPrice, _ := money.New(1000, usd)
	secondPrice, _ := money.New(500, usd)
	fee, _ := money.New(100, usd)
	tax, _ := money.New(240, usd)
	quote, err := CalculateQuote(QuoteInput{
		Lines: []LineInput{
			{PriceTierID: second, Quantity: 1, UnitPrice: secondPrice},
			{PriceTierID: first, Quantity: 2, UnitPrice: firstPrice},
		},
		DiscountMinor: 200,
		Fees:          []Adjustment{{Code: "service_fee", Amount: fee}},
		Taxes:         []Adjustment{{Code: "sales_tax", Amount: tax}},
	})
	if err != nil {
		t.Fatalf("CalculateQuote() error = %v", err)
	}
	if quote.Currency != usd || quote.Subtotal.MinorUnits() != 2500 || quote.Discount.MinorUnits() != 200 || quote.Total.MinorUnits() != 2640 {
		t.Fatalf("quote totals = %#v", quote)
	}
	if len(quote.Lines) != 2 || quote.Lines[0].PriceTierID != first || quote.Lines[0].Subtotal.MinorUnits() != 2000 {
		t.Fatalf("canonical lines = %#v", quote.Lines)
	}
	if quote.Fees[0].Code != "service_fee" || quote.Taxes[0].Code != "sales_tax" || quote.SnapshotSHA256 == [32]byte{} {
		t.Fatalf("quote components/digest = %#v/%x", quote, quote.SnapshotSHA256)
	}
}

func TestCalculateQuoteDigestIsOrderIndependent(t *testing.T) {
	usd, _ := money.NewCurrency("USD")
	first := mustQuoteID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	second := mustQuoteID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	firstPrice, _ := money.New(1000, usd)
	secondPrice, _ := money.New(500, usd)
	serviceFee, _ := money.New(100, usd)
	input := QuoteInput{Lines: []LineInput{{PriceTierID: first, Quantity: 1, UnitPrice: firstPrice}, {PriceTierID: second, Quantity: 2, UnitPrice: secondPrice}}, Fees: []Adjustment{{Code: "service_fee", Amount: serviceFee}}}
	left, err := CalculateQuote(input)
	if err != nil {
		t.Fatalf("left quote error = %v", err)
	}
	input.Lines[0], input.Lines[1] = input.Lines[1], input.Lines[0]
	input.Fees[0].Code = " SERVICE_FEE "
	right, err := CalculateQuote(input)
	if err != nil {
		t.Fatalf("right quote error = %v", err)
	}
	if left.SnapshotSHA256 != right.SnapshotSHA256 {
		t.Fatalf("digest changed for equivalent input: %x != %x", left.SnapshotSHA256, right.SnapshotSHA256)
	}
}

func TestQuoteSnapshotRejectsMutationAndContainsOnlyCommercialFacts(t *testing.T) {
	usd, _ := money.NewCurrency("USD")
	price, _ := money.New(1000, usd)
	quote, err := CalculateQuote(QuoteInput{Lines: []LineInput{{PriceTierID: mustQuoteID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5"), Quantity: 1, UnitPrice: price}}})
	if err != nil {
		t.Fatalf("CalculateQuote() error = %v", err)
	}
	snapshot, err := quote.SnapshotJSON()
	if err != nil {
		t.Fatalf("SnapshotJSON() error = %v", err)
	}
	if len(snapshot) == 0 || string(snapshot) == "null" {
		t.Fatalf("snapshot = %s", snapshot)
	}
	if string(snapshot) == "" || containsAny(string(snapshot), "email", "customer", "payment_method") {
		t.Fatalf("snapshot contains sensitive fields: %s", snapshot)
	}
	quote.Lines[0].Quantity = 2
	if _, err := quote.SnapshotJSON(); !errors.Is(err, ErrQuoteMutated) {
		t.Fatalf("mutated SnapshotJSON() error = %v, want ErrQuoteMutated", err)
	}
}

func TestParseSnapshotRejectsTrailingJSON(t *testing.T) {
	usd, _ := money.NewCurrency("USD")
	price, _ := money.New(1000, usd)
	quote, err := CalculateQuote(QuoteInput{Lines: []LineInput{{PriceTierID: mustQuoteID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5"), Quantity: 1, UnitPrice: price}}})
	if err != nil {
		t.Fatalf("CalculateQuote() error = %v", err)
	}
	snapshot, err := quote.SnapshotJSON()
	if err != nil {
		t.Fatalf("SnapshotJSON() error = %v", err)
	}
	if _, err := ParseSnapshot(append(snapshot, []byte(` {}`)...)); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("ParseSnapshot() error = %v, want ErrInvalidSnapshot", err)
	}
}

func TestCalculateQuoteRejectsUnsafeOrInconsistentInput(t *testing.T) {
	usd, _ := money.NewCurrency("USD")
	eur, _ := money.NewCurrency("EUR")
	first := mustQuoteID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	second := mustQuoteID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	usdPrice, _ := money.New(100, usd)
	eurPrice, _ := money.New(100, eur)
	tests := []struct {
		name string
		in   QuoteInput
		want error
	}{
		{name: "empty", in: QuoteInput{}, want: ErrEmptyQuote},
		{name: "duplicate line", in: QuoteInput{Lines: []LineInput{{PriceTierID: first, Quantity: 1, UnitPrice: usdPrice}, {PriceTierID: first, Quantity: 1, UnitPrice: usdPrice}}}, want: ErrDuplicateQuoteLine},
		{name: "currency mismatch", in: QuoteInput{Lines: []LineInput{{PriceTierID: first, Quantity: 1, UnitPrice: usdPrice}, {PriceTierID: second, Quantity: 1, UnitPrice: eurPrice}}}, want: money.ErrCurrencyMismatch},
		{name: "discount exceeds", in: QuoteInput{Lines: []LineInput{{PriceTierID: first, Quantity: 1, UnitPrice: usdPrice}}, DiscountMinor: 101}, want: ErrDiscountExceedsTotal},
		{name: "duplicate adjustment", in: QuoteInput{Lines: []LineInput{{PriceTierID: first, Quantity: 1, UnitPrice: usdPrice}}, Fees: []Adjustment{{Code: "service", Amount: usdPrice}, {Code: " SERVICE ", Amount: usdPrice}}}, want: ErrDuplicateAdjustment},
		{name: "invalid adjustment code", in: QuoteInput{Lines: []LineInput{{PriceTierID: first, Quantity: 1, UnitPrice: usdPrice}}, Fees: []Adjustment{{Code: "service fee", Amount: usdPrice}}}, want: ErrInvalidQuote},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := CalculateQuote(test.in); !errors.Is(err, test.want) {
				t.Fatalf("CalculateQuote() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestCalculateQuoteRejectsOverflow(t *testing.T) {
	usd, _ := money.NewCurrency("USD")
	first := mustQuoteID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	price, _ := money.New(math.MaxInt64, usd)
	if _, err := CalculateQuote(QuoteInput{Lines: []LineInput{{PriceTierID: first, Quantity: 2, UnitPrice: price}}}); !errors.Is(err, ErrInvalidQuote) {
		t.Fatalf("CalculateQuote() error = %v, want ErrInvalidQuote", err)
	}
}

func mustQuoteID(t *testing.T, value string) identifier.ID {
	t.Helper()
	id, err := identifier.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", value, err)
	}
	return id
}

func containsAny(value string, forbidden ...string) bool {
	for _, candidate := range forbidden {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}
