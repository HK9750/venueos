package payment

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/order"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

func TestPaymentRequestsNormalizeWithoutProviderPolicy(t *testing.T) {
	organizationID := mustPaymentID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	orderID := mustPaymentID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	request, err := (CreateIntentRequest{OrganizationID: organizationID, OrderID: orderID, AmountMinor: 2500, Currency: " usd ", IdempotencyKey: "checkout-2026-0001"}).Normalize()
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if request.Currency != "USD" {
		t.Fatalf("currency normalization = %q", request.Currency)
	}
	for _, invalid := range []CreateIntentRequest{{OrganizationID: organizationID, OrderID: orderID, AmountMinor: 0, Currency: "USD", IdempotencyKey: "checkout-2026-0001"}, {OrganizationID: organizationID, OrderID: orderID, AmountMinor: 1, Currency: "USD", IdempotencyKey: "short"}} {
		if _, err := invalid.Normalize(); !errors.Is(err, ErrInvalidRequest) {
			t.Errorf("Normalize(%#v) error = %v, want ErrInvalidRequest", invalid, err)
		}
	}
}

func TestWebhookEventValidationAndPayloadHash(t *testing.T) {
	status := order.PaymentCaptured
	hash := PayloadHash([]byte(`{"provider":"test","event":"captured"}`))
	amount := int64(2500)
	event := WebhookEvent{Provider: "stripe", EventID: "evt_123", EventType: "payment_intent.succeeded", ProviderObjectID: "pi_123", PayloadReference: "blob.payments.evt_123", PayloadSHA256: hash, OccurredAt: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC), Status: &status, AmountMinor: &amount, Currency: "USD"}
	if err := event.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	event.PayloadSHA256 = [32]byte{}
	if !errors.Is(event.Validate(), ErrInvalidEvent) {
		t.Fatalf("missing hash error = %v, want ErrInvalidEvent", event.Validate())
	}
}

func TestClassifyNeverReturnsRawProviderError(t *testing.T) {
	raw := errors.New("provider secret=do-not-log card=4111111111111111")
	failure := Classify(raw)
	if failure.Class != FailureUnknown || failure.Code != "provider_error" || failure.SafeMessage == "" {
		t.Fatalf("failure = %#v", failure)
	}
	if failure.SafeMessage == raw.Error() || contains(failure.SafeMessage, "secret", "411111") {
		t.Fatalf("raw provider data leaked: %#v", failure)
	}
	timeout := Classify(context.DeadlineExceeded)
	if timeout.Class != FailureTransient || timeout.Code != "timeout" {
		t.Fatalf("timeout failure = %#v", timeout)
	}
	valid := Failure{Class: FailurePermanent, Code: "declined", SafeMessage: "The payment was declined."}
	if got := Classify(valid); got.Class != FailurePermanent || got.Code != "declined" {
		t.Fatalf("valid failure = %#v", got)
	}
}

func TestAttemptAppliesOnlyMatchingMonotonicProviderFacts(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	attempt, err := BuildAttempt(CreateAttemptInput{ID: mustPaymentID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5"), OrganizationID: mustPaymentID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6"), OrderID: mustPaymentID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7"), Provider: "stripe", AmountMinor: 2500, Currency: "USD", IdempotencyKey: "checkout-2026-0001", Now: now})
	if err != nil {
		t.Fatalf("BuildAttempt() error = %v", err)
	}
	intent := Intent{Provider: "stripe", ProviderIntentID: "pi_123", Status: StatusRequiresAction, AmountMinor: 2500, Currency: "USD", CreatedAt: now}
	updated, err := attempt.ApplyIntent(intent, 1, now.Add(time.Second))
	if err != nil {
		t.Fatalf("ApplyIntent() error = %v", err)
	}
	if updated.Status != StatusRequiresAction || updated.Version != 2 || updated.ProviderObjectID != "pi_123" {
		t.Fatalf("updated attempt = %#v", updated)
	}
	status := StatusCaptured
	amount := int64(2500)
	event := WebhookEvent{Provider: "stripe", EventID: "evt_123", EventType: "payment_intent.succeeded", ProviderObjectID: "pi_123", PayloadSHA256: PayloadHash([]byte("payload")), OccurredAt: now.Add(2 * time.Second), Status: &status, AmountMinor: &amount, Currency: "USD"}
	confirmed, err := updated.ApplyWebhook(event, 2, now.Add(2*time.Second))
	if err != nil {
		t.Fatalf("ApplyWebhook() error = %v", err)
	}
	if confirmed.Status != StatusCaptured || confirmed.Version != 3 {
		t.Fatalf("confirmed attempt = %#v", confirmed)
	}
	replayed, err := confirmed.ApplyWebhook(event, 3, now.Add(3*time.Second))
	if err != nil || replayed.Version != confirmed.Version || replayed.Status != confirmed.Status {
		t.Fatalf("same-state webhook replay = %#v, %v", replayed, err)
	}
	amount = 2501
	event.AmountMinor = &amount
	if _, err := confirmed.ApplyWebhook(event, 3, now.Add(3*time.Second)); !errors.Is(err, ErrAmountMismatch) {
		t.Fatalf("amount mismatch error = %v, want ErrAmountMismatch", err)
	}
	if _, err := confirmed.ApplyIntent(intent, 2, now.Add(3*time.Second)); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("version mismatch error = %v, want ErrVersionConflict", err)
	}
}

func mustPaymentID(t *testing.T, value string) identifier.ID {
	t.Helper()
	id, err := identifier.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", value, err)
	}
	return id
}

func contains(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		for index := 0; index+len(candidate) <= len(value); index++ {
			if value[index:index+len(candidate)] == candidate {
				return true
			}
		}
	}
	return false
}
