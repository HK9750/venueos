package order

import (
	"errors"
	"testing"
)

func TestAdvanceOrderIsExplicitAndIdempotent(t *testing.T) {
	valid := []struct {
		from Status
		to   Status
	}{
		{StatusDraft, StatusPaymentPending},
		{StatusPaymentPending, StatusConfirmed},
		{StatusConfirmed, StatusPartiallyRefunded},
		{StatusPartiallyRefunded, StatusRefunded},
		{StatusPaymentPending, StatusReconciliationRequired},
	}
	for _, test := range valid {
		result, err := Advance(test.from, test.to)
		if err != nil || result.Status != test.to || !result.Changed {
			t.Errorf("Advance(%q, %q) = %#v, %v", test.from, test.to, result, err)
		}
	}
	replay, err := Advance(StatusConfirmed, StatusConfirmed)
	if err != nil || replay.Status != StatusConfirmed || replay.Changed {
		t.Fatalf("same-state replay = %#v, %v", replay, err)
	}
	for _, test := range [][2]Status{{StatusConfirmed, StatusPaymentPending}, {StatusRefunded, StatusConfirmed}, {StatusPaymentFailed, StatusPaymentPending}, {StatusDraft, StatusConfirmed}} {
		if _, err := Advance(test[0], test[1]); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("Advance(%q, %q) error = %v, want ErrInvalidTransition", test[0], test[1], err)
		}
	}
}

func TestAdvancePaymentAllowsMonotonicProviderFactsAndReplay(t *testing.T) {
	valid := []struct {
		from PaymentStatus
		to   PaymentStatus
	}{
		{PaymentCreated, PaymentRequiresAction},
		{PaymentRequiresAction, PaymentProcessing},
		{PaymentProcessing, PaymentAuthorized},
		{PaymentAuthorized, PaymentCaptured},
		{PaymentUnknown, PaymentFailed},
	}
	for _, test := range valid {
		result, err := AdvancePayment(test.from, test.to)
		if err != nil || result.Status != test.to || !result.Changed {
			t.Errorf("AdvancePayment(%q, %q) = %#v, %v", test.from, test.to, result, err)
		}
	}
	replay, err := AdvancePayment(PaymentCaptured, PaymentCaptured)
	if err != nil || replay.Status != PaymentCaptured || replay.Changed {
		t.Fatalf("same payment replay = %#v, %v", replay, err)
	}
	if _, err := AdvancePayment(PaymentCaptured, PaymentFailed); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("captured rollback error = %v, want ErrInvalidTransition", err)
	}
}

func TestAdvanceRejectsUnknownStates(t *testing.T) {
	if _, err := Advance(Status("future"), StatusDraft); !errors.Is(err, ErrInvalidStatus) {
		t.Fatalf("unknown order status error = %v", err)
	}
	if _, err := AdvancePayment(PaymentStatus("future"), PaymentCreated); !errors.Is(err, ErrInvalidPayment) {
		t.Fatalf("unknown payment status error = %v", err)
	}
}
