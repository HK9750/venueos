package money

import (
	"errors"
	"math"
	"testing"
)

func TestNewCurrency(t *testing.T) {
	for _, code := range []string{"", "US", "usd", "U5D", "USDD"} {
		if _, err := NewCurrency(code); !errors.Is(err, ErrInvalidCurrency) {
			t.Errorf("NewCurrency(%q) error = %v, want ErrInvalidCurrency", code, err)
		}
	}
	currency, err := NewCurrency("USD")
	if err != nil {
		t.Fatalf("NewCurrency() error = %v", err)
	}
	if currency.String() != "USD" {
		t.Fatalf("String() = %q, want USD", currency.String())
	}
}

func TestMoneyCheckedArithmetic(t *testing.T) {
	usd, err := NewCurrency("USD")
	if err != nil {
		t.Fatalf("NewCurrency() error = %v", err)
	}
	gbp, err := NewCurrency("GBP")
	if err != nil {
		t.Fatalf("NewCurrency() error = %v", err)
	}

	left, _ := New(700, usd)
	right, _ := New(300, usd)
	total, err := left.Add(right)
	if err != nil || total.MinorUnits() != 1000 || total.Currency() != usd {
		t.Fatalf("Add() = %+v, %v", total, err)
	}
	remainder, err := total.Subtract(right)
	if err != nil || remainder != left {
		t.Fatalf("Subtract() = %+v, %v; want %+v", remainder, err, left)
	}

	other, _ := New(1, gbp)
	if _, err := left.Add(other); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("currency mismatch error = %v", err)
	}
	if _, err := right.Subtract(left); !errors.Is(err, ErrNegativeAmount) {
		t.Fatalf("negative result error = %v", err)
	}
	maximum, _ := New(math.MaxInt64, usd)
	if _, err := maximum.Add(otherCurrency(t, 1, usd)); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflow error = %v", err)
	}
}

func TestMoneyRejectsNegativeAmount(t *testing.T) {
	usd, _ := NewCurrency("USD")
	if _, err := New(-1, usd); !errors.Is(err, ErrNegativeAmount) {
		t.Fatalf("New() error = %v, want ErrNegativeAmount", err)
	}
}

func otherCurrency(t *testing.T, amount int64, currency Currency) Money {
	t.Helper()
	value, err := New(amount, currency)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return value
}
