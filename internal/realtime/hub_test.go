package realtime

import (
	"testing"
	"time"
)

func TestHubFansOutOnlyToTopicAndCopiesPayload(t *testing.T) {
	hub, err := New(Config{QueueSize: 2, MaxSubscriptions: 4, MaxPayloadBytes: 1024})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	first, err := hub.Subscribe("session:a")
	if err != nil {
		t.Fatalf("Subscribe(first) error = %v", err)
	}
	defer first.Close()
	other, err := hub.Subscribe("session:b")
	if err != nil {
		t.Fatalf("Subscribe(other) error = %v", err)
	}
	defer other.Close()
	payload := []byte(`{"revision":1}`)
	result, err := hub.Publish(Event{ID: "evt-1", Topic: "session:a", Sequence: 1, SchemaVersion: 1, EventType: "availability.changed", Payload: payload, OccurredAt: time.Now().UTC()})
	if err != nil || result.Delivered != 1 || result.Dropped != 0 {
		t.Fatalf("Publish() = %#v, %v", result, err)
	}
	payload[2] = 'X'
	select {
	case event := <-first.Events():
		if string(event.Payload) != `{"revision":1}` {
			t.Fatalf("payload was not copied: %s", event.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for topic event")
	}
	select {
	case event := <-other.Events():
		t.Fatalf("cross-topic event = %#v", event)
	default:
	}
}

func TestHubDisconnectsSlowSubscriberAndRequiresReplay(t *testing.T) {
	hub, err := New(Config{QueueSize: 1, MaxSubscriptions: 2, MaxPayloadBytes: 64})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	subscription, err := hub.Subscribe("session:a")
	if err != nil {
		t.Fatalf("Subscribe() error = %v", err)
	}
	event := func(sequence int64) Event {
		return Event{ID: "evt", Topic: "session:a", Sequence: sequence, SchemaVersion: 1, EventType: "availability.changed", Payload: []byte(`{}`), OccurredAt: time.Now().UTC()}
	}
	if _, err := hub.Publish(event(1)); err != nil {
		t.Fatalf("first Publish() error = %v", err)
	}
	result, err := hub.Publish(event(2))
	if err != nil || result.Dropped != 1 {
		t.Fatalf("slow Publish() = %#v, %v", result, err)
	}
	if !subscription.ResyncRequired() {
		t.Fatal("slow subscriber did not require replay")
	}
	select {
	case <-subscription.Done():
	case <-time.After(time.Second):
		t.Fatal("slow subscriber was not closed")
	}
}
