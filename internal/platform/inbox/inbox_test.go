package inbox

import (
	"context"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
)

type processorStoreFake struct {
	messages    []Message
	processed   []identifier.ID
	retries     []retryCall
	deadLetters []failureCall
	sweeps      int
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

func (fake *processorStoreFake) Claim(_ context.Context, _ string, _ int32, _ int32, _ time.Duration, _ time.Time) ([]Message, error) {
	messages := append([]Message(nil), fake.messages...)
	fake.messages = nil
	return messages, nil
}

func (fake *processorStoreFake) MarkProcessed(_ context.Context, id identifier.ID, _ string, _ time.Time) error {
	fake.processed = append(fake.processed, id)
	return nil
}

func (fake *processorStoreFake) Retry(_ context.Context, id identifier.ID, _ string, availableAt time.Time, code, message string) error {
	fake.retries = append(fake.retries, retryCall{id: id, availableAt: availableAt, code: code, message: message})
	return nil
}

func (fake *processorStoreFake) DeadLetter(_ context.Context, id identifier.ID, _ string, deadLetteredAt time.Time, code, message string) error {
	fake.deadLetters = append(fake.deadLetters, failureCall{id: id, deadLetteredAt: deadLetteredAt, code: code, message: message})
	return nil
}

func (fake *processorStoreFake) DeadLetterExhaustedLeases(_ context.Context, _ int32, _ time.Time) (int64, error) {
	fake.sweeps++
	return 0, nil
}

func newMessage(t *testing.T, attempts int32) Message {
	t.Helper()
	id, err := identifier.New()
	if err != nil {
		t.Fatalf("new message ID: %v", err)
	}
	now := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	return Message{
		ID: id, Source: "stripe", MessageID: "evt_123", PayloadSHA256: [32]byte{1},
		State: StateReceived, AttemptCount: attempts, AvailableAt: now, ReceivedAt: now, UpdatedAt: now,
	}
}

func processorConfig() Config {
	return Config{
		Owner: "inbox-worker", BatchSize: 10, MaxAttempts: 3, LeaseDuration: 5 * time.Second,
		ProcessTimeout: time.Second, Jitter: func(maximum time.Duration) time.Duration { return maximum },
	}
}

func TestProcessorMarksSuccessfulMessageProcessed(t *testing.T) {
	message := newMessage(t, 1)
	store := &processorStoreFake{messages: []Message{message}}
	processor, err := New(store, func(context.Context, Message) error { return nil }, processorConfig())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	now := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	if err := processor.RunOnce(context.Background(), now); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(store.processed) != 1 || store.processed[0] != message.ID {
		t.Fatalf("processed IDs = %v, want %v", store.processed, message.ID)
	}
	if len(store.retries) != 0 || len(store.deadLetters) != 0 || store.sweeps != 1 {
		t.Fatalf("unexpected transitions: retries %d dead letters %d sweeps %d", len(store.retries), len(store.deadLetters), store.sweeps)
	}
}

func TestProcessorRetriesTransientFailure(t *testing.T) {
	message := newMessage(t, 1)
	store := &processorStoreFake{messages: []Message{message}}
	processor, err := New(store, func(context.Context, Message) error {
		return &Failure{Class: FailureTransient, SafeMessage: "provider unavailable"}
	}, processorConfig())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	now := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	if err := processor.RunOnce(context.Background(), now); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(store.retries) != 1 || !store.retries[0].availableAt.Equal(now.Add(time.Second)) {
		t.Fatalf("retry calls = %+v", store.retries)
	}
	if store.retries[0].code != FailureTransient || store.retries[0].message != "provider unavailable" {
		t.Fatalf("retry classification = %+v", store.retries[0])
	}
}

func TestProcessorDoesNotAcknowledgeHandlerTimeout(t *testing.T) {
	message := newMessage(t, 1)
	store := &processorStoreFake{messages: []Message{message}}
	config := processorConfig()
	config.ProcessTimeout = time.Nanosecond
	processor, err := New(store, func(ctx context.Context, _ Message) error {
		<-ctx.Done()
		return nil
	}, config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	now := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	if err := processor.RunOnce(context.Background(), now); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(store.processed) != 0 || len(store.retries) != 1 {
		t.Fatalf("timeout transitions: processed %d retries %d", len(store.processed), len(store.retries))
	}
	if store.retries[0].code != FailureTransient {
		t.Fatalf("timeout failure class = %q, want %q", store.retries[0].code, FailureTransient)
	}
}

func TestProcessorDeadLettersPermanentAndFinalAttempt(t *testing.T) {
	tests := []struct {
		name     string
		attempts int32
		errClass string
	}{
		{name: "permanent", attempts: 1, errClass: FailurePermanent},
		{name: "final attempt", attempts: 3, errClass: FailureUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := newMessage(t, test.attempts)
			store := &processorStoreFake{messages: []Message{message}}
			processor, err := New(store, func(context.Context, Message) error {
				return &Failure{Class: test.errClass, SafeMessage: "operator-safe failure"}
			}, processorConfig())
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}
			if err := processor.RunOnce(context.Background(), time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)); err != nil {
				t.Fatalf("RunOnce() error = %v", err)
			}
			if len(store.deadLetters) != 1 || store.deadLetters[0].id != message.ID {
				t.Fatalf("dead-letter calls = %+v", store.deadLetters)
			}
			if store.deadLetters[0].code != test.errClass {
				t.Fatalf("dead-letter class = %q, want %q", store.deadLetters[0].code, test.errClass)
			}
		})
	}
}

func TestValidateMessageRequiresIdentityAndHash(t *testing.T) {
	message := newMessage(t, 0)
	if err := ValidateMessage(message); err != nil {
		t.Fatalf("ValidateMessage(valid) error = %v", err)
	}
	message.PayloadSHA256 = [32]byte{}
	if err := ValidateMessage(message); err == nil {
		t.Fatal("ValidateMessage accepted an empty payload hash")
	}
	message = newMessage(t, 0)
	message.MessageID = ""
	if err := ValidateMessage(message); err == nil {
		t.Fatal("ValidateMessage accepted an empty message ID")
	}
}
