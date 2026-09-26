package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
	platformpostgres "github.com/HK9750/venueos/internal/platform/postgres"
)

type relayStoreFake struct {
	events        []platformpostgres.OutboxEvent
	published     []identifier.ID
	retries       []retryCall
	deadLetters   []failureCall
	sweeps        int
	claimOwner    string
	claimBatch    int32
	claimAttempts int32
	claimLease    time.Duration
	claimNow      time.Time
}

type retryCall struct {
	id          identifier.ID
	availableAt time.Time
	code        string
	message     string
}

type failureCall struct {
	id             identifier.ID
	deadLetteredAt time.Time
	code           string
	message        string
}

func (fake *relayStoreFake) ClaimOutboxEvents(_ context.Context, owner string, batchSize, maxAttempts int32, lease time.Duration, now time.Time) ([]platformpostgres.OutboxEvent, error) {
	fake.claimOwner = owner
	fake.claimBatch = batchSize
	fake.claimAttempts = maxAttempts
	fake.claimLease = lease
	fake.claimNow = now
	events := append([]platformpostgres.OutboxEvent(nil), fake.events...)
	fake.events = nil
	return events, nil
}

func (fake *relayStoreFake) MarkOutboxPublished(_ context.Context, id identifier.ID, _ string, _ time.Time) error {
	fake.published = append(fake.published, id)
	return nil
}

func (fake *relayStoreFake) RetryOutboxEvent(_ context.Context, id identifier.ID, _ string, availableAt time.Time, code, message string) error {
	fake.retries = append(fake.retries, retryCall{id: id, availableAt: availableAt, code: code, message: message})
	return nil
}

func (fake *relayStoreFake) DeadLetterOutboxEvent(_ context.Context, id identifier.ID, _ string, deadLetteredAt time.Time, code, message string) error {
	fake.deadLetters = append(fake.deadLetters, failureCall{id: id, deadLetteredAt: deadLetteredAt, code: code, message: message})
	return nil
}

func (fake *relayStoreFake) DeadLetterExhaustedOutboxLeases(_ context.Context, _ int32, _ time.Time) (int64, error) {
	fake.sweeps++
	return 0, nil
}

type relayPublisherFake struct {
	err  error
	seen []identifier.ID
}

func (fake *relayPublisherFake) Publish(_ context.Context, event platformpostgres.OutboxEvent) error {
	fake.seen = append(fake.seen, event.ID)
	return fake.err
}

func newRelayEvent(t *testing.T, attempts int32) platformpostgres.OutboxEvent {
	t.Helper()
	id, err := identifier.New()
	if err != nil {
		t.Fatalf("new event ID: %v", err)
	}
	organizationID, err := identifier.New()
	if err != nil {
		t.Fatalf("new organization ID: %v", err)
	}
	aggregateID, err := identifier.New()
	if err != nil {
		t.Fatalf("new aggregate ID: %v", err)
	}
	return platformpostgres.OutboxEvent{
		ID:             id,
		OrganizationID: organizationID,
		AggregateID:    aggregateID,
		EventType:      "venue.updated",
		Payload:        []byte(`{"version":1}`),
		AttemptCount:   attempts,
	}
}

func testRelayConfig() Config {
	return Config{
		Owner:          "test-relay",
		PollInterval:   time.Second,
		BatchSize:      10,
		MaxAttempts:    3,
		LeaseDuration:  10 * time.Second,
		PublishTimeout: time.Second,
		RetryBase:      2 * time.Second,
		RetryMax:       10 * time.Second,
		Jitter:         func(maximum time.Duration) time.Duration { return maximum },
	}
}

func TestRelayPublishesAndAcknowledges(t *testing.T) {
	event := newRelayEvent(t, 1)
	store := &relayStoreFake{events: []platformpostgres.OutboxEvent{event}}
	publisher := &relayPublisherFake{}
	relay, err := New(store, publisher, testRelayConfig())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	if err := relay.RunOnce(context.Background(), now); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(publisher.seen) != 1 || publisher.seen[0] != event.ID {
		t.Fatalf("published IDs = %v, want %v", publisher.seen, event.ID)
	}
	if len(store.published) != 1 || store.published[0] != event.ID {
		t.Fatalf("acknowledged IDs = %v, want %v", store.published, event.ID)
	}
	if len(store.retries) != 0 || len(store.deadLetters) != 0 || store.sweeps != 1 {
		t.Fatalf("relay transitions = retries %d dead letters %d sweeps %d", len(store.retries), len(store.deadLetters), store.sweeps)
	}
	if !store.claimNow.Equal(now) || store.claimOwner != "test-relay" || store.claimBatch != 10 || store.claimAttempts != 3 || store.claimLease != 10*time.Second {
		t.Fatalf("claim parameters were not forwarded: %+v", store)
	}
}

func TestRelayRetriesTransientFailure(t *testing.T) {
	event := newRelayEvent(t, 1)
	store := &relayStoreFake{events: []platformpostgres.OutboxEvent{event}}
	publisher := &relayPublisherFake{err: &Failure{Class: FailureTransient, SafeMessage: "provider unavailable"}}
	relay, err := New(store, publisher, testRelayConfig())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	if err := relay.RunOnce(context.Background(), now); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(store.retries) != 1 || !store.retries[0].availableAt.Equal(now.Add(2*time.Second)) {
		t.Fatalf("retry calls = %+v", store.retries)
	}
	if store.retries[0].code != FailureTransient || store.retries[0].message != "provider unavailable" {
		t.Fatalf("retry classification = %+v", store.retries[0])
	}
	if len(store.published) != 0 || len(store.deadLetters) != 0 {
		t.Fatalf("unexpected terminal transitions: published %d dead letters %d", len(store.published), len(store.deadLetters))
	}
}

func TestRelayDeadLettersPermanentAndExhaustedFailures(t *testing.T) {
	tests := []struct {
		name     string
		attempts int32
		err      error
	}{
		{name: "permanent", attempts: 1, err: &Failure{Class: FailurePermanent, SafeMessage: "unsupported event"}},
		{name: "exhausted", attempts: 3, err: errors.New("unknown publication failure")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := newRelayEvent(t, test.attempts)
			store := &relayStoreFake{events: []platformpostgres.OutboxEvent{event}}
			publisher := &relayPublisherFake{err: test.err}
			relay, err := New(store, publisher, testRelayConfig())
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
			if err := relay.RunOnce(context.Background(), now); err != nil {
				t.Fatalf("RunOnce() error = %v", err)
			}
			if len(store.deadLetters) != 1 || store.deadLetters[0].id != event.ID {
				t.Fatalf("dead-letter calls = %+v", store.deadLetters)
			}
			if len(store.retries) != 0 || len(store.published) != 0 {
				t.Fatalf("unexpected transitions: retries %d published %d", len(store.retries), len(store.published))
			}
		})
	}
}

func TestNewRejectsUnsafeRelayConfiguration(t *testing.T) {
	store := &relayStoreFake{}
	publisher := &relayPublisherFake{}
	config := testRelayConfig()
	config.LeaseDuration = config.PublishTimeout
	if _, err := New(store, publisher, config); err == nil {
		t.Fatal("New() accepted a lease that does not exceed the publish timeout")
	}
}
