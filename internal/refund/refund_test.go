package refund

import (
	"errors"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/money"
)

func TestValidateAmountEnforcesCapturedCeilingAndCurrency(t *testing.T) {
	usd, _ := money.NewCurrency("USD")
	eur, _ := money.NewCurrency("EUR")
	captured, _ := money.New(1000, usd)
	allocated, _ := money.New(700, usd)
	requested, _ := money.New(300, usd)
	if err := ValidateAmount(captured, allocated, requested); err != nil {
		t.Fatalf("ValidateAmount() error = %v", err)
	}
	tooMuch, _ := money.New(301, usd)
	if err := ValidateAmount(captured, allocated, tooMuch); !errors.Is(err, ErrExceedsCaptured) {
		t.Fatalf("too much error = %v, want ErrExceedsCaptured", err)
	}
	zero, _ := money.New(0, usd)
	if err := ValidateAmount(captured, allocated, zero); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("zero error = %v, want ErrInvalidAmount", err)
	}
	otherCurrency, _ := money.New(1, eur)
	if err := ValidateAmount(captured, allocated, otherCurrency); !errors.Is(err, money.ErrCurrencyMismatch) {
		t.Fatalf("currency error = %v, want ErrCurrencyMismatch", err)
	}
	alreadyOver, _ := money.New(1001, usd)
	if err := ValidateAmount(captured, alreadyOver, requested); !errors.Is(err, ErrExceedsCaptured) {
		t.Fatalf("invalid existing ledger error = %v, want ErrExceedsCaptured", err)
	}
}

func TestAdvanceRefundIsExplicitAndIdempotent(t *testing.T) {
	for _, test := range [][2]Status{{StatusRequested, StatusProcessing}, {StatusProcessing, StatusSucceeded}, {StatusProcessing, StatusFailed}, {StatusProcessing, StatusReconciliationRequired}} {
		got, changed, err := Advance(test[0], test[1])
		if err != nil || got != test[1] || !changed {
			t.Errorf("Advance(%q, %q) = %q/%v/%v", test[0], test[1], got, changed, err)
		}
	}
	got, changed, err := Advance(StatusSucceeded, StatusSucceeded)
	if err != nil || got != StatusSucceeded || changed {
		t.Fatalf("same-state refund replay = %q/%v/%v", got, changed, err)
	}
	if _, _, err := Advance(StatusSucceeded, StatusProcessing); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("terminal rollback error = %v, want ErrInvalidTransition", err)
	}
}

func TestBuildRefundNormalizesReasonAndStartsRequested(t *testing.T) {
	value, err := Build(CreateInput{ID: mustRefundID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5"), OrganizationID: mustRefundID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6"), OrderID: mustRefundID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7"), PaymentAttemptID: mustRefundID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef8"), AmountMinor: 500, Currency: " usd ", IdempotencyKey: "refund-request-0001", Reason: "  customer   request  ", ActorType: "user", ActorID: "user-1", Now: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if value.Status != StatusRequested || value.Currency != "USD" || value.Reason != "customer request" || value.Version != 1 {
		t.Fatalf("refund = %#v", value)
	}
}

func mustRefundID(t *testing.T, value string) identifier.ID {
	t.Helper()
	id, err := identifier.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", value, err)
	}
	return id
}
