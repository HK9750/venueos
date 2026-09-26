// Package jobqueue defines the durable background-job contract shared by the
// worker application and persistence adapters.
package jobqueue

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
)

var ErrLeaseLost = errors.New("job lease lost")

type Job struct {
	ID             identifier.ID
	OrganizationID *identifier.ID
	Type           string
	SchemaVersion  int32
	Payload        json.RawMessage
	AttemptCount   int32
	MaxAttempts    int32
	CorrelationID  string
	RequestID      string
	TraceID        string
}

type Enqueue struct {
	ID             identifier.ID
	OrganizationID *identifier.ID
	Type           string
	SchemaVersion  int32
	Priority       int16
	DedupeKey      string
	Payload        json.RawMessage
	RunAt          time.Time
	MaxAttempts    int32
	CorrelationID  string
	RequestID      string
	TraceID        string
	CreatedAt      time.Time
}

type Failure struct {
	Class   string
	Message string
}

type QueueStat struct {
	Type      string
	Depth     int64
	OldestAge time.Duration
}

type Store interface {
	Claim(context.Context, string, int32, time.Duration) ([]Job, error)
	Succeed(context.Context, identifier.ID, string) error
	Retry(context.Context, identifier.ID, string, time.Duration, Failure) error
	DeadLetter(context.Context, identifier.ID, string, Failure) error
	DeadLetterExhaustedLeases(context.Context) (int64, error)
	QueueStats(context.Context, []string) ([]QueueStat, error)
}
