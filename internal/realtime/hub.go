// Package realtime provides a bounded in-process fan-out primitive. PostgreSQL
// replay remains the correctness/recovery authority; this hub only reduces
// latency after an outbox event has committed.
package realtime

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrInvalidEvent        = errors.New("invalid realtime event")
	ErrInvalidSubscription = errors.New("invalid realtime subscription")
)

type Event struct {
	ID            string
	Topic         string
	Sequence      int64
	SchemaVersion int32
	EventType     string
	Payload       []byte
	OccurredAt    time.Time
}

type Config struct {
	QueueSize        int
	MaxSubscriptions int
	MaxPayloadBytes  int
}

type Hub struct {
	mu            sync.Mutex
	config        Config
	subscriptions map[string]map[*Subscription]struct{}
}

type Subscription struct {
	hub       *Hub
	topic     string
	events    chan Event
	done      chan struct{}
	closeOnce sync.Once
	mu        sync.RWMutex
	closed    bool
	resync    bool
}

type PublishResult struct {
	Delivered int
	Dropped   int
}

func New(config Config) (*Hub, error) {
	if config.QueueSize < 1 || config.QueueSize > 1000 || config.MaxSubscriptions < 1 || config.MaxSubscriptions > 100000 || config.MaxPayloadBytes < 1 || config.MaxPayloadBytes > 1<<20 {
		return nil, fmt.Errorf("%w: queue, subscription, and payload limits are invalid", ErrInvalidSubscription)
	}
	return &Hub{config: config, subscriptions: make(map[string]map[*Subscription]struct{})}, nil
}

func (hub *Hub) Subscribe(topic string) (*Subscription, error) {
	if hub == nil || len(topic) < 1 || len(topic) > 256 {
		return nil, fmt.Errorf("%w: topic is invalid", ErrInvalidSubscription)
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	count := 0
	for _, subscribers := range hub.subscriptions {
		count += len(subscribers)
	}
	if count >= hub.config.MaxSubscriptions {
		return nil, fmt.Errorf("%w: subscription limit reached", ErrInvalidSubscription)
	}
	subscription := &Subscription{hub: hub, topic: topic, events: make(chan Event, hub.config.QueueSize), done: make(chan struct{})}
	if hub.subscriptions[topic] == nil {
		hub.subscriptions[topic] = make(map[*Subscription]struct{})
	}
	hub.subscriptions[topic][subscription] = struct{}{}
	return subscription, nil
}

func (hub *Hub) Publish(event Event) (PublishResult, error) {
	if hub == nil || len(event.ID) < 1 || len(event.ID) > 255 || len(event.Topic) < 1 || len(event.Topic) > 256 || event.Sequence < 1 || event.SchemaVersion < 1 || len(event.EventType) < 1 || len(event.EventType) > 128 || len(event.Payload) > hub.config.MaxPayloadBytes || event.OccurredAt.IsZero() {
		return PublishResult{}, ErrInvalidEvent
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	event.Payload = append([]byte(nil), event.Payload...)
	result := PublishResult{}
	for subscription := range hub.subscriptions[event.Topic] {
		select {
		case subscription.events <- event:
			result.Delivered++
		default:
			subscription.closeLocked(true)
			delete(hub.subscriptions[event.Topic], subscription)
			result.Dropped++
		}
	}
	return result, nil
}

func (subscription *Subscription) Events() <-chan Event {
	if subscription == nil {
		return nil
	}
	return subscription.events
}

func (subscription *Subscription) Done() <-chan struct{} {
	if subscription == nil {
		return nil
	}
	return subscription.done
}

func (subscription *Subscription) ResyncRequired() bool {
	if subscription == nil {
		return false
	}
	subscription.mu.RLock()
	defer subscription.mu.RUnlock()
	return subscription.resync
}

func (subscription *Subscription) Close() { subscription.close(false) }

func (subscription *Subscription) close(resync bool) {
	if subscription == nil || subscription.hub == nil {
		return
	}
	subscription.hub.mu.Lock()
	defer subscription.hub.mu.Unlock()
	if subscribers := subscription.hub.subscriptions[subscription.topic]; subscribers != nil {
		delete(subscribers, subscription)
		if len(subscribers) == 0 {
			delete(subscription.hub.subscriptions, subscription.topic)
		}
	}
	subscription.closeLocked(resync)
}

func (subscription *Subscription) closeLocked(resync bool) {
	subscription.closeOnce.Do(func() {
		subscription.mu.Lock()
		subscription.closed = true
		subscription.resync = resync
		subscription.mu.Unlock()
		close(subscription.done)
		close(subscription.events)
	})
}
