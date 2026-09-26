// Package outbox provides the durable PostgreSQL outbox relay loop.
package outbox

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
	platformpostgres "github.com/HK9750/venueos/internal/platform/postgres"
)

const (
	FailurePermanent = "permanent"
	FailureTransient = "transient"
	FailureUnknown   = "unknown"
)

type Failure struct {
	Class       string
	SafeMessage string
	Cause       error
}

func (failure *Failure) Error() string {
	if failure.Cause != nil {
		return failure.Cause.Error()
	}
	return failure.SafeMessage
}

func (failure *Failure) Unwrap() error { return failure.Cause }

type Store interface {
	ClaimOutboxEvents(context.Context, string, int32, int32, time.Duration, time.Time) ([]platformpostgres.OutboxEvent, error)
	MarkOutboxPublished(context.Context, identifier.ID, string, time.Time) error
	RetryOutboxEvent(context.Context, identifier.ID, string, time.Time, string, string) error
	DeadLetterOutboxEvent(context.Context, identifier.ID, string, time.Time, string, string) error
	DeadLetterExhaustedOutboxLeases(context.Context, int32, time.Time) (int64, error)
}

type Publisher interface {
	Publish(context.Context, platformpostgres.OutboxEvent) error
}

type Config struct {
	Owner          string
	PollInterval   time.Duration
	BatchSize      int32
	MaxAttempts    int32
	LeaseDuration  time.Duration
	PublishTimeout time.Duration
	RetryBase      time.Duration
	RetryMax       time.Duration
	Jitter         func(time.Duration) time.Duration
}

type Relay struct {
	store     Store
	publisher Publisher
	config    Config
}

func New(store Store, publisher Publisher, config Config) (*Relay, error) {
	if store == nil || publisher == nil || strings.TrimSpace(config.Owner) == "" || len(config.Owner) > 128 ||
		config.PollInterval <= 0 || config.BatchSize < 1 || config.BatchSize > 100 || config.MaxAttempts < 1 || config.MaxAttempts > 20 ||
		config.LeaseDuration <= config.PublishTimeout || config.PublishTimeout <= 0 || config.RetryBase <= 0 || config.RetryMax < config.RetryBase {
		return nil, errors.New("invalid outbox relay configuration")
	}
	if config.Jitter == nil {
		config.Jitter = func(maximum time.Duration) time.Duration {
			if maximum <= 0 {
				return 0
			}
			return time.Duration(rand.Int64N(int64(maximum) + 1))
		}
	}
	return &Relay{store: store, publisher: publisher, config: config}, nil
}

func (relay *Relay) Run(ctx context.Context) error {
	ticker := time.NewTicker(relay.config.PollInterval)
	defer ticker.Stop()
	if err := relay.RunOnce(ctx, time.Now().UTC()); err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := relay.RunOnce(ctx, time.Now().UTC()); err != nil && ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
}

func (relay *Relay) RunOnce(ctx context.Context, now time.Time) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if _, err := relay.store.DeadLetterExhaustedOutboxLeases(ctx, relay.config.MaxAttempts, now.UTC()); err != nil {
		return fmt.Errorf("sweep exhausted outbox leases: %w", err)
	}
	events, err := relay.store.ClaimOutboxEvents(ctx, relay.config.Owner, relay.config.BatchSize, relay.config.MaxAttempts, relay.config.LeaseDuration, now.UTC())
	if err != nil {
		return fmt.Errorf("claim outbox events: %w", err)
	}
	for _, event := range events {
		if err := relay.publishOne(ctx, event, now.UTC()); err != nil {
			return err
		}
	}
	return nil
}

func (relay *Relay) publishOne(ctx context.Context, event platformpostgres.OutboxEvent, now time.Time) error {
	publishCtx, cancel := context.WithTimeout(ctx, relay.config.PublishTimeout)
	err := relay.publisher.Publish(publishCtx, event)
	cancel()
	if err == nil {
		if transitionErr := relay.store.MarkOutboxPublished(ctx, event.ID, relay.config.Owner, now.UTC()); transitionErr != nil {
			return fmt.Errorf("mark outbox event published: %w", transitionErr)
		}
		return nil
	}
	failure := classify(err)
	if failure.Class == FailurePermanent || event.AttemptCount >= relay.config.MaxAttempts {
		if transitionErr := relay.store.DeadLetterOutboxEvent(ctx, event.ID, relay.config.Owner, now, failure.Class, failure.SafeMessage); transitionErr != nil {
			return fmt.Errorf("dead-letter outbox event: %w", transitionErr)
		}
		return nil
	}
	delay := relay.retryDelay(event.AttemptCount)
	if transitionErr := relay.store.RetryOutboxEvent(ctx, event.ID, relay.config.Owner, now.Add(delay), failure.Class, failure.SafeMessage); transitionErr != nil {
		return fmt.Errorf("retry outbox event: %w", transitionErr)
	}
	return nil
}

func (relay *Relay) retryDelay(attempt int32) time.Duration {
	maximum := relay.config.RetryBase
	for current := int32(1); current < attempt && maximum < relay.config.RetryMax; current++ {
		if maximum > relay.config.RetryMax/2 {
			maximum = relay.config.RetryMax
			break
		}
		maximum *= 2
	}
	if maximum > relay.config.RetryMax {
		maximum = relay.config.RetryMax
	}
	return relay.config.Jitter(maximum)
}

func classify(err error) Failure {
	var failure *Failure
	if errors.As(err, &failure) {
		class := failure.Class
		if class != FailurePermanent && class != FailureTransient && class != FailureUnknown {
			class = FailureUnknown
		}
		return Failure{Class: class, SafeMessage: safeMessage(failure.SafeMessage), Cause: failure.Cause}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return Failure{Class: FailureTransient, SafeMessage: "Outbox publication timed out."}
	}
	return Failure{Class: FailureUnknown, SafeMessage: "Outbox publication failed."}
}

func safeMessage(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "Outbox publication failed."
	}
	if len(value) > 1000 {
		return value[:1000]
	}
	return value
}
