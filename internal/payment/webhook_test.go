package payment

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/inbox"
)

type webhookAdapterFake struct {
	event WebhookEvent
	err   error
	seen  []byte
}

func (fake *webhookAdapterFake) Name() string { return "stripe" }
func (*webhookAdapterFake) CreateIntent(context.Context, CreateIntentRequest) (Intent, error) {
	return Intent{}, errors.New("not used")
}
func (*webhookAdapterFake) RetrieveIntent(context.Context, string) (Intent, error) {
	return Intent{}, errors.New("not used")
}
func (*webhookAdapterFake) CancelIntent(context.Context, string) (Intent, error) {
	return Intent{}, errors.New("not used")
}
func (*webhookAdapterFake) CreateRefund(context.Context, RefundRequest) (RefundResult, error) {
	return RefundResult{}, errors.New("not used")
}
func (fake *webhookAdapterFake) VerifyWebhook(_ context.Context, payload []byte, _ map[string]string) (WebhookEvent, error) {
	fake.seen = append([]byte(nil), payload...)
	if fake.err != nil {
		return WebhookEvent{}, fake.err
	}
	return fake.event, nil
}

type webhookPayloadStoreFake struct {
	input PayloadPutInput
	puts  int
}

func (fake *webhookPayloadStoreFake) Put(_ context.Context, input PayloadPutInput) (string, error) {
	fake.input = input
	fake.puts++
	return "payments/stripe/evt_123", nil
}

type webhookInboxStoreFake struct {
	message  inbox.Message
	inserted bool
	called   int
	err      error
}

func (fake *webhookInboxStoreFake) InsertInboxMessage(_ context.Context, message inbox.Message) (bool, error) {
	fake.message = message
	fake.called++
	if fake.err != nil {
		return false, fake.err
	}
	return fake.inserted, nil
}

func TestWebhookReceiverVerifiesStoresAndDeduplicates(t *testing.T) {
	body := []byte(`{"id":"evt_123"}`)
	hash := PayloadHash(body)
	status := StatusCaptured
	amount := int64(2500)
	adapter := &webhookAdapterFake{event: WebhookEvent{Provider: "stripe", EventID: "evt_123", EventType: "payment_intent.succeeded", ProviderObjectID: "pi_123", PayloadSHA256: hash, OccurredAt: time.Date(2026, 10, 3, 11, 59, 0, 0, time.UTC), Status: &status, AmountMinor: &amount, Currency: "USD"}}
	payloads := &webhookPayloadStoreFake{}
	inboxStore := &webhookInboxStoreFake{inserted: true}
	receiver, err := NewWebhookReceiver(adapter, inboxStore, payloads, clock.NewFixed(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)), 1024)
	if err != nil {
		t.Fatalf("NewWebhookReceiver() error = %v", err)
	}
	result, err := receiver.Receive(context.Background(), body, map[string]string{"Stripe-Signature": "opaque"})
	if err != nil {
		t.Fatalf("Receive() error = %v", err)
	}
	if !result.Inserted || result.Duplicate || result.Event.PayloadReference != "payments/stripe/evt_123" || inboxStore.message.MessageID != "evt_123" || payloads.puts != 1 || string(payloads.input.Payload) != string(body) {
		t.Fatalf("receive result/stores = %#v/%#v/%#v", result, inboxStore.message, payloads)
	}
	inboxStore.inserted = false
	result, err = receiver.Receive(context.Background(), body, nil)
	if err != nil || result.Inserted || !result.Duplicate {
		t.Fatalf("duplicate Receive() = %#v, %v", result, err)
	}
}

func TestWebhookReceiverRejectsTamperedAndOversizedBodiesBeforePersistence(t *testing.T) {
	body := []byte(`{"id":"evt_123"}`)
	status := StatusCaptured
	adapter := &webhookAdapterFake{event: WebhookEvent{Provider: "stripe", EventID: "evt_123", EventType: "payment_intent.succeeded", ProviderObjectID: "pi_123", PayloadSHA256: PayloadHash([]byte("different")), OccurredAt: time.Now().UTC(), Status: &status}}
	payloads := &webhookPayloadStoreFake{}
	inboxStore := &webhookInboxStoreFake{inserted: true}
	receiver, err := NewWebhookReceiver(adapter, inboxStore, payloads, clock.System{}, len(body))
	if err != nil {
		t.Fatalf("NewWebhookReceiver() error = %v", err)
	}
	if _, err := receiver.Receive(context.Background(), body, nil); !errors.Is(err, ErrWebhookPayloadHash) {
		t.Fatalf("tampered hash error = %v", err)
	}
	if payloads.puts != 0 || inboxStore.called != 0 {
		t.Fatalf("persistence happened before hash verification: payloads=%d inbox=%d", payloads.puts, inboxStore.called)
	}
	if _, err := receiver.Receive(context.Background(), append(body, 'x'), nil); !errors.Is(err, ErrWebhookBodyTooLarge) {
		t.Fatalf("oversized error = %v", err)
	}
}

func TestWebhookReceiverDoesNotExposeAdapterFailure(t *testing.T) {
	adapter := &webhookAdapterFake{err: errors.New("provider secret=do-not-log")}
	receiver, err := NewWebhookReceiver(adapter, &webhookInboxStoreFake{}, &webhookPayloadStoreFake{}, clock.System{}, 1024)
	if err != nil {
		t.Fatalf("NewWebhookReceiver() error = %v", err)
	}
	_, err = receiver.Receive(context.Background(), []byte(`{}`), nil)
	if err == nil || err.Error() == "provider secret=do-not-log" || strings.Contains(err.Error(), "secret=") {
		t.Fatalf("adapter failure leaked: %v", err)
	}
}
