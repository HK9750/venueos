// Package money provides checked integer minor-unit monetary values.
package money

import (
	"errors"
	"fmt"
	"math"
)

var (
	ErrInvalidCurrency  = errors.New("invalid currency")
	ErrNegativeAmount   = errors.New("money amount must not be negative")
	ErrCurrencyMismatch = errors.New("money currencies do not match")
	ErrOverflow         = errors.New("money amount overflow")
)

// Currency is a canonical three-letter ISO-4217-style currency code.
type Currency string

// NewCurrency validates the storage and API representation of a currency code.
func NewCurrency(code string) (Currency, error) {
	if len(code) != 3 {
		return "", fmt.Errorf("%w: code must contain three uppercase ASCII letters", ErrInvalidCurrency)
	}
	for index := range code {
		if code[index] < 'A' || code[index] > 'Z' {
			return "", fmt.Errorf("%w: code must contain three uppercase ASCII letters", ErrInvalidCurrency)
		}
	}
	return Currency(code), nil
}

func (currency Currency) String() string { return string(currency) }

func (currency Currency) valid() bool {
	validated, err := NewCurrency(string(currency))
	return err == nil && validated == currency
}

// Money is a non-negative amount in integer minor units and one currency.
type Money struct {
	minorUnits int64
	currency   Currency
}

// New constructs a non-negative monetary value.
func New(minorUnits int64, currency Currency) (Money, error) {
	if !currency.valid() {
		return Money{}, ErrInvalidCurrency
	}
	if minorUnits < 0 {
		return Money{}, ErrNegativeAmount
	}
	return Money{minorUnits: minorUnits, currency: currency}, nil
}

func (money Money) MinorUnits() int64 { return money.minorUnits }

func (money Money) Currency() Currency { return money.currency }

// Add returns the checked sum of two values in the same currency.
func (money Money) Add(other Money) (Money, error) {
	if money.currency != other.currency || !money.currency.valid() {
		return Money{}, ErrCurrencyMismatch
	}
	if other.minorUnits > math.MaxInt64-money.minorUnits {
		return Money{}, ErrOverflow
	}
	return New(money.minorUnits+other.minorUnits, money.currency)
}

// Subtract returns the non-negative difference of two values in the same currency.
func (money Money) Subtract(other Money) (Money, error) {
	if money.currency != other.currency || !money.currency.valid() {
		return Money{}, ErrCurrencyMismatch
	}
	if other.minorUnits > money.minorUnits {
		return Money{}, ErrNegativeAmount
	}
	return New(money.minorUnits-other.minorUnits, money.currency)
}

// Multiply returns money multiplied by a non-negative quantity with checked
// integer arithmetic. It is used for ticket/cart line extensions; fractional
// currency and floating-point rounding never enter the calculation.
func (money Money) Multiply(quantity int64) (Money, error) {
	if !money.currency.valid() {
		return Money{}, ErrCurrencyMismatch
	}
	if quantity < 0 {
		return Money{}, ErrNegativeAmount
	}
	if quantity != 0 && money.minorUnits > math.MaxInt64/quantity {
		return Money{}, ErrOverflow
	}
	return New(money.minorUnits*quantity, money.currency)
}
