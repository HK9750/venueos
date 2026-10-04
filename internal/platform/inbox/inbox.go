// Package inbox defines the durable inbound-message processing contract.
package inbox

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
)

const (
	StateReceived   = "received"
	StateProcessing = "processing"
	StateProcessed  = "processed"
	StateFailed     = "failed"
	StateDeadLetter = "dead_letter"

	FailurePermanent = "permanent"
	FailureTransient = "transient"
	FailureUnknown   = "unknown"
)

var (
	ErrInvalidMessage  = errors.New("invalid inbox message")
	ErrNotFound        = errors.New("inbox message not found")
	ErrPayloadConflict = errors.New("inbox message payload conflict")
	ErrLeaseLost       = errors.New("inbox message lease lost")
)

type Message struct {
	ID               identifier.ID
	OrganizationID   *identifier.ID
	Source           string
	MessageID        string
	PayloadReference string
	PayloadSHA256    [32]byte
	State            string
	AttemptCount     int32
	AvailableAt      time.Time
	LeaseExpiresAt   *time.Time
	ReceivedAt       time.Time
	ProcessedAt      *time.Time
	UpdatedAt        time.Time
}

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
	Claim(context.Context, string, int32, int32, time.Duration, time.Time) ([]Message, error)
	MarkProcessed(context.Context, identifier.ID, string, time.Time) error
	Retry(context.Context, identifier.ID, string, time.Time, string, string) error
	DeadLetter(context.Context, identifier.ID, string, time.Time, string, string) error
	DeadLetterExhaustedLeases(context.Context, int32, time.Time) (int64, error)
}

type Handler func(context.Context, Message) error

type Config struct {
	Owner          string
	BatchSize      int32
	MaxAttempts    int32
	LeaseDuration  time.Duration
	ProcessTimeout time.Duration
	Jitter         func(time.Duration) time.Duration
}

type Processor struct {
	store   Store
	handler Handler
	config  Config
}

func New(store Store, handler Handler, config Config) (*Processor, error) {
	if store == nil || handler == nil || strings.TrimSpace(config.Owner) == "" || len(config.Owner) > 128 ||
		config.BatchSize < 1 || config.BatchSize > 100 || config.MaxAttempts < 1 || config.MaxAttempts > 20 ||
		config.LeaseDuration <= config.ProcessTimeout || config.ProcessTimeout <= 0 {
		return nil, fmt.Errorf("%w: invalid processor configuration", ErrInvalidMessage)
	}
	if config.Jitter == nil {
		config.Jitter = func(maximum time.Duration) time.Duration {
			if maximum <= 0 {
				return 0
			}
			return time.Duration(rand.Int64N(int64(maximum) + 1))
		}
	}
	return &Processor{store: store, handler: handler, config: config}, nil
}

func (processor *Processor) RunOnce(ctx context.Context, now time.Time) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if _, err := processor.store.DeadLetterExhaustedLeases(ctx, processor.config.MaxAttempts, now.UTC()); err != nil {
		return fmt.Errorf("sweep exhausted inbox leases: %w", err)
	}
	messages, err := processor.store.Claim(ctx, processor.config.Owner, processor.config.BatchSize, processor.config.MaxAttempts, processor.config.LeaseDuration, now.UTC())
	if err != nil {
		return fmt.Errorf("claim inbox messages: %w", err)
	}
	for _, message := range messages {
		if err := processor.process(ctx, message, now.UTC()); err != nil {
			return err
		}
	}
	return nil
}

func (processor *Processor) process(ctx context.Context, message Message, now time.Time) error {
	processCtx, cancel := context.WithTimeout(ctx, processor.config.ProcessTimeout)
	err := processor.handler(processCtx, message)
	contextErr := processCtx.Err()
	cancel()
	if err == nil && contextErr != nil {
		err = contextErr
	}
	if err == nil {
		if transitionErr := processor.store.MarkProcessed(ctx, message.ID, processor.config.Owner, now); transitionErr != nil {
			return fmt.Errorf("mark inbox message processed: %w", transitionErr)
		}
		return nil
	}
	failure := Classify(err)
	if failure.Class == FailurePermanent || message.AttemptCount >= processor.config.MaxAttempts {
		if transitionErr := processor.store.DeadLetter(ctx, message.ID, processor.config.Owner, now, failure.Class, failure.SafeMessage); transitionErr != nil {
			return fmt.Errorf("dead-letter inbox message: %w", transitionErr)
		}
		return nil
	}
	if transitionErr := processor.store.Retry(ctx, message.ID, processor.config.Owner, now.Add(processor.retryDelay(message.AttemptCount)), failure.Class, failure.SafeMessage); transitionErr != nil {
		return fmt.Errorf("retry inbox message: %w", transitionErr)
	}
	return nil
}

func Classify(err error) Failure {
	var failure *Failure
	if errors.As(err, &failure) {
		class := failure.Class
		if class != FailurePermanent && class != FailureTransient && class != FailureUnknown {
			class = FailureUnknown
		}
		return Failure{Class: class, SafeMessage: safeMessage(failure.SafeMessage), Cause: failure.Cause}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return Failure{Class: FailureTransient, SafeMessage: "Inbound message processing timed out."}
	}
	return Failure{Class: FailureUnknown, SafeMessage: "Inbound message processing failed."}
}

func (processor *Processor) retryDelay(attempt int32) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := time.Second
	for current := int32(1); current < attempt && delay < 15*time.Minute; current++ {
		delay *= 2
	}
	if delay > 15*time.Minute {
		delay = 15 * time.Minute
	}
	return processor.config.Jitter(delay)
}

func safeMessage(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return "Inbound message processing failed."
	}
	if len(value) > 1000 {
		return value[:1000]
	}
	return value
}

func ValidateMessage(message Message) error {
	if message.ID.IsZero() || strings.TrimSpace(message.Source) == "" || len(message.Source) > 128 ||
		strings.TrimSpace(message.MessageID) == "" || len(message.MessageID) > 255 ||
		message.PayloadSHA256 == [32]byte{} || message.ReceivedAt.IsZero() || message.AvailableAt.IsZero() ||
		message.UpdatedAt.IsZero() || message.UpdatedAt.Before(message.ReceivedAt) {
		return ErrInvalidMessage
	}
	return nil
}
