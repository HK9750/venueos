package pricing

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/money"
)

const MaxQuoteLines = 100

var (
	ErrInvalidQuote         = errors.New("invalid quote")
	ErrEmptyQuote           = errors.New("quote must contain at least one line")
	ErrDiscountExceedsTotal = errors.New("discount exceeds subtotal")
	ErrDuplicateQuoteLine   = errors.New("quote contains duplicate price tier")
	ErrDuplicateAdjustment  = errors.New("quote contains duplicate adjustment code")
	ErrQuoteMutated         = errors.New("quote snapshot no longer matches its digest")
	ErrInvalidSnapshot      = errors.New("invalid quote snapshot")
)

// LineInput is an immutable price snapshot supplied by the cart/order layer.
// The calculator does not look up or trust mutable catalog rows.
type LineInput struct {
	PriceTierID identifier.ID
	Quantity    int64
	UnitPrice   money.Money
}

// Adjustment is an explicit policy result. Tax, fee, and discount policies are
// intentionally supplied by the caller until launch jurisdiction decisions are
// made; this package never invents rates or legal semantics.
type Adjustment struct {
	Code   string
	Amount money.Money
}

type QuoteInput struct {
	Lines         []LineInput
	DiscountMinor int64
	Fees          []Adjustment
	Taxes         []Adjustment
}

type QuoteLine struct {
	PriceTierID identifier.ID
	Quantity    int64
	UnitPrice   money.Money
	Subtotal    money.Money
}

// Quote is a deterministic commercial snapshot. SnapshotSHA256 covers every
// field that affects the total and can be stored with an order for replay and
// reconciliation.
type Quote struct {
	Currency       money.Currency
	Lines          []QuoteLine
	Subtotal       money.Money
	Discount       money.Money
	Fees           []Adjustment
	Taxes          []Adjustment
	Total          money.Money
	SnapshotSHA256 [32]byte
}

// SnapshotJSON returns the canonical, PII-free commercial snapshot that may be
// stored with a cart/order. It refuses a Quote whose exported components were
// mutated after calculation, so the stored digest remains meaningful.
func (quote Quote) SnapshotJSON() ([]byte, error) {
	if quote.Currency == "" || len(quote.Lines) == 0 || quote.SnapshotSHA256 != quoteDigest(quote) {
		return nil, ErrQuoteMutated
	}
	return quoteSnapshotBytes(quote), nil
}

// CalculateQuote computes subtotal, explicit discount, fees, taxes, and total
// using checked minor-unit arithmetic. Inputs are copied and canonically sorted
// so equivalent carts produce the same snapshot digest.
func CalculateQuote(input QuoteInput) (Quote, error) {
	if len(input.Lines) == 0 {
		return Quote{}, ErrEmptyQuote
	}
	if len(input.Lines) > MaxQuoteLines {
		return Quote{}, fmt.Errorf("%w: at most %d lines", ErrInvalidQuote, MaxQuoteLines)
	}
	if input.DiscountMinor < 0 {
		return Quote{}, fmt.Errorf("%w: discount must not be negative", ErrInvalidQuote)
	}
	lines := append([]LineInput(nil), input.Lines...)
	sort.Slice(lines, func(left, right int) bool {
		return lines[left].PriceTierID.String() < lines[right].PriceTierID.String()
	})
	quoteLines := make([]QuoteLine, 0, len(lines))
	var currency money.Currency
	var subtotal money.Money
	seenLines := make(map[identifier.ID]struct{}, len(lines))
	for _, line := range lines {
		if line.PriceTierID.IsZero() || line.Quantity < 1 || line.Quantity > 1000 {
			return Quote{}, fmt.Errorf("%w: line identifier or quantity is invalid", ErrInvalidQuote)
		}
		if _, exists := seenLines[line.PriceTierID]; exists {
			return Quote{}, ErrDuplicateQuoteLine
		}
		seenLines[line.PriceTierID] = struct{}{}
		if line.UnitPrice.Currency() == "" {
			return Quote{}, fmt.Errorf("%w: line currency is required", ErrInvalidQuote)
		}
		if len(quoteLines) == 0 {
			currency = line.UnitPrice.Currency()
			if _, err := money.NewCurrency(currency.String()); err != nil {
				return Quote{}, fmt.Errorf("%w: invalid currency", ErrInvalidQuote)
			}
		} else if line.UnitPrice.Currency() != currency {
			return Quote{}, money.ErrCurrencyMismatch
		}
		lineSubtotal, err := line.UnitPrice.Multiply(line.Quantity)
		if err != nil {
			return Quote{}, fmt.Errorf("%w: line total: %w", ErrInvalidQuote, err)
		}
		subtotal, err = addMoney(subtotal, lineSubtotal, len(quoteLines) == 0)
		if err != nil {
			return Quote{}, err
		}
		quoteLines = append(quoteLines, QuoteLine{PriceTierID: line.PriceTierID, Quantity: line.Quantity, UnitPrice: line.UnitPrice, Subtotal: lineSubtotal})
	}
	discount, err := money.New(input.DiscountMinor, currency)
	if err != nil {
		return Quote{}, fmt.Errorf("%w: discount: %w", ErrInvalidQuote, err)
	}
	if discount.MinorUnits() > subtotal.MinorUnits() {
		return Quote{}, ErrDiscountExceedsTotal
	}
	base, err := subtotal.Subtract(discount)
	if err != nil {
		return Quote{}, fmt.Errorf("%w: apply discount: %w", ErrInvalidQuote, err)
	}
	fees, err := normalizeAdjustments(input.Fees, currency)
	if err != nil {
		return Quote{}, err
	}
	taxes, err := normalizeAdjustments(input.Taxes, currency)
	if err != nil {
		return Quote{}, err
	}
	total, err := addAdjustments(base, fees)
	if err != nil {
		return Quote{}, err
	}
	total, err = addAdjustments(total, taxes)
	if err != nil {
		return Quote{}, err
	}
	quote := Quote{Currency: currency, Lines: quoteLines, Subtotal: subtotal, Discount: discount, Fees: fees, Taxes: taxes, Total: total}
	quote.SnapshotSHA256 = quoteDigest(quote)
	return quote, nil
}

func addMoney(current, next money.Money, first bool) (money.Money, error) {
	if first {
		return next, nil
	}
	result, err := current.Add(next)
	if err != nil {
		return money.Money{}, err
	}
	return result, nil
}

func normalizeAdjustments(adjustments []Adjustment, currency money.Currency) ([]Adjustment, error) {
	result := append([]Adjustment(nil), adjustments...)
	sort.Slice(result, func(left, right int) bool { return result[left].Code < result[right].Code })
	seen := make(map[string]struct{}, len(result))
	for index := range result {
		result[index].Code = strings.ToLower(strings.TrimSpace(result[index].Code))
		if !validAdjustmentCode(result[index].Code) {
			return nil, fmt.Errorf("%w: adjustment code is invalid", ErrInvalidQuote)
		}
		if _, exists := seen[result[index].Code]; exists {
			return nil, ErrDuplicateAdjustment
		}
		seen[result[index].Code] = struct{}{}
		amount := result[index].Amount
		if amount.Currency() != currency {
			return nil, money.ErrCurrencyMismatch
		}
		if _, err := money.New(amount.MinorUnits(), amount.Currency()); err != nil {
			return nil, fmt.Errorf("%w: adjustment amount: %w", ErrInvalidQuote, err)
		}
	}
	return result, nil
}

func addAdjustments(base money.Money, adjustments []Adjustment) (money.Money, error) {
	result := base
	for _, adjustment := range adjustments {
		var err error
		result, err = result.Add(adjustment.Amount)
		if err != nil {
			return money.Money{}, fmt.Errorf("%w: adjustment total: %w", ErrInvalidQuote, err)
		}
	}
	return result, nil
}

func validAdjustmentCode(value string) bool {
	if len(value) < 1 || len(value) > 64 {
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

type digestLine struct {
	ID       string `json:"id"`
	Quantity int64  `json:"quantity"`
	Unit     int64  `json:"unit_minor"`
	Subtotal int64  `json:"subtotal_minor"`
}

type digestAdjustment struct {
	Code   string `json:"code"`
	Amount int64  `json:"amount_minor"`
}

func quoteDigest(quote Quote) [32]byte {
	return sha256.Sum256(quoteSnapshotBytes(quote))
}

func quoteSnapshotBytes(quote Quote) []byte {
	lines := make([]digestLine, len(quote.Lines))
	for index, line := range quote.Lines {
		lines[index] = digestLine{ID: line.PriceTierID.String(), Quantity: line.Quantity, Unit: line.UnitPrice.MinorUnits(), Subtotal: line.Subtotal.MinorUnits()}
	}
	toDigestAdjustments := func(adjustments []Adjustment) []digestAdjustment {
		result := make([]digestAdjustment, len(adjustments))
		for index, adjustment := range adjustments {
			result[index] = digestAdjustment{Code: adjustment.Code, Amount: adjustment.Amount.MinorUnits()}
		}
		return result
	}
	body, _ := json.Marshal(struct {
		Version  int                `json:"version"`
		Currency string             `json:"currency"`
		Lines    []digestLine       `json:"lines"`
		Discount int64              `json:"discount_minor"`
		Fees     []digestAdjustment `json:"fees"`
		Taxes    []digestAdjustment `json:"taxes"`
		Total    int64              `json:"total_minor"`
	}{
		Version:  1,
		Currency: quote.Currency.String(),
		Lines:    lines,
		Discount: quote.Discount.MinorUnits(),
		Fees:     toDigestAdjustments(quote.Fees),
		Taxes:    toDigestAdjustments(quote.Taxes),
		Total:    quote.Total.MinorUnits(),
	})
	return body
}

// Snapshot is the validated, PII-free representation persisted with a cart or
// order. It is intentionally separate from Quote because it contains no
// mutable catalog objects or policy resolvers.
type Snapshot struct {
	Version       int
	Currency      money.Currency
	Lines         []SnapshotLine
	DiscountMinor int64
	Fees          []SnapshotAdjustment
	Taxes         []SnapshotAdjustment
	TotalMinor    int64
}

type SnapshotLine struct {
	PriceTierID   identifier.ID
	Quantity      int64
	UnitMinor     int64
	SubtotalMinor int64
}

type SnapshotAdjustment struct {
	Code        string
	AmountMinor int64
}

// ParseSnapshot validates a stored canonical quote snapshot before it is used
// to create durable commercial records. The caller may compare Digest(body)
// with the separately stored quote hash to detect tampering.
func ParseSnapshot(body []byte) (Snapshot, error) {
	if len(body) < 2 || len(body) > 262144 {
		return Snapshot{}, fmt.Errorf("%w: snapshot size is invalid", ErrInvalidSnapshot)
	}
	var payload struct {
		Version  int                `json:"version"`
		Currency string             `json:"currency"`
		Lines    []digestLine       `json:"lines"`
		Discount int64              `json:"discount_minor"`
		Fees     []digestAdjustment `json:"fees"`
		Taxes    []digestAdjustment `json:"taxes"`
		Total    int64              `json:"total_minor"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return Snapshot{}, fmt.Errorf("%w: JSON is invalid", ErrInvalidSnapshot)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Snapshot{}, fmt.Errorf("%w: trailing JSON is not allowed", ErrInvalidSnapshot)
	}
	if payload.Version != 1 || len(payload.Lines) < 1 || len(payload.Lines) > MaxQuoteLines {
		return Snapshot{}, fmt.Errorf("%w: version or lines are invalid", ErrInvalidSnapshot)
	}
	currency, err := money.NewCurrency(payload.Currency)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: currency is invalid", ErrInvalidSnapshot)
	}
	lines := make([]SnapshotLine, 0, len(payload.Lines))
	var subtotal money.Money
	seenLines := make(map[identifier.ID]struct{}, len(payload.Lines))
	for index, line := range payload.Lines {
		id, parseErr := identifier.Parse(line.ID)
		if parseErr != nil || line.Quantity < 1 || line.Quantity > 1000 || line.Unit < 0 || line.Subtotal < 0 {
			return Snapshot{}, fmt.Errorf("%w: line %d is invalid", ErrInvalidSnapshot, index)
		}
		if index > 0 && payload.Lines[index-1].ID >= line.ID {
			return Snapshot{}, fmt.Errorf("%w: lines are not canonically ordered", ErrInvalidSnapshot)
		}
		if _, exists := seenLines[id]; exists {
			return Snapshot{}, fmt.Errorf("%w: duplicate line", ErrInvalidSnapshot)
		}
		seenLines[id] = struct{}{}
		unit, newErr := money.New(line.Unit, currency)
		if newErr != nil {
			return Snapshot{}, fmt.Errorf("%w: line %d unit is invalid", ErrInvalidSnapshot, index)
		}
		expected, multiplyErr := unit.Multiply(line.Quantity)
		if multiplyErr != nil || expected.MinorUnits() != line.Subtotal {
			return Snapshot{}, fmt.Errorf("%w: line %d subtotal is invalid", ErrInvalidSnapshot, index)
		}
		lineTotal, newErr := money.New(line.Subtotal, currency)
		if newErr != nil {
			return Snapshot{}, fmt.Errorf("%w: line %d subtotal is invalid", ErrInvalidSnapshot, index)
		}
		if len(lines) == 0 {
			subtotal = lineTotal
		} else {
			subtotal, newErr = subtotal.Add(lineTotal)
			if newErr != nil {
				return Snapshot{}, fmt.Errorf("%w: subtotal overflow", ErrInvalidSnapshot)
			}
		}
		lines = append(lines, SnapshotLine{PriceTierID: id, Quantity: line.Quantity, UnitMinor: line.Unit, SubtotalMinor: line.Subtotal})
	}
	if payload.Discount < 0 || payload.Discount > subtotal.MinorUnits() {
		return Snapshot{}, fmt.Errorf("%w: discount is invalid", ErrInvalidSnapshot)
	}
	fees, err := parseSnapshotAdjustments(payload.Fees, currency)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: fees: %w", ErrInvalidSnapshot, err)
	}
	taxes, err := parseSnapshotAdjustments(payload.Taxes, currency)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: taxes: %w", ErrInvalidSnapshot, err)
	}
	total, err := subtotal.Subtract(mustMoney(payload.Discount, currency))
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: discount arithmetic is invalid", ErrInvalidSnapshot)
	}
	for _, adjustment := range append(append([]SnapshotAdjustment{}, fees...), taxes...) {
		total, err = total.Add(mustMoney(adjustment.AmountMinor, currency))
		if err != nil {
			return Snapshot{}, fmt.Errorf("%w: total overflow", ErrInvalidSnapshot)
		}
	}
	if total.MinorUnits() != payload.Total {
		return Snapshot{}, fmt.Errorf("%w: total is invalid", ErrInvalidSnapshot)
	}
	return Snapshot{Version: payload.Version, Currency: currency, Lines: lines, DiscountMinor: payload.Discount, Fees: fees, Taxes: taxes, TotalMinor: payload.Total}, nil
}

func parseSnapshotAdjustments(values []digestAdjustment, currency money.Currency) ([]SnapshotAdjustment, error) {
	result := make([]SnapshotAdjustment, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if !validAdjustmentCode(value.Code) || value.Amount < 0 {
			return nil, fmt.Errorf("adjustment %d is invalid", index)
		}
		if index > 0 && values[index-1].Code >= value.Code {
			return nil, errors.New("adjustments are not canonically ordered")
		}
		if _, exists := seen[value.Code]; exists {
			return nil, ErrDuplicateAdjustment
		}
		seen[value.Code] = struct{}{}
		if _, err := money.New(value.Amount, currency); err != nil {
			return nil, err
		}
		result = append(result, SnapshotAdjustment{Code: value.Code, AmountMinor: value.Amount})
	}
	return result, nil
}

func mustMoney(amount int64, currency money.Currency) money.Money {
	value, _ := money.New(amount, currency)
	return value
}
