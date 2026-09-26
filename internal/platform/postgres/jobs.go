package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/jobqueue"
	"github.com/HK9750/venueos/internal/platform/postgres/sqlc"
	"github.com/jackc/pgx/v5/pgtype"
)

func (store *Store) EnqueueJob(ctx context.Context, job jobqueue.Enqueue) (bool, error) {
	if job.ID.IsZero() || job.Type == "" || job.SchemaVersion <= 0 || job.DedupeKey == "" ||
		!json.Valid(job.Payload) || job.RunAt.IsZero() || job.MaxAttempts <= 0 || job.CreatedAt.IsZero() {
		return false, fmt.Errorf("%w: job identity, type, version, dedupe key, payload, times, and attempts are required", ErrInvalidRecord)
	}
	rows, err := store.queries.EnqueueJob(ctx, sqlc.EnqueueJobParams{
		ID:             job.ID.UUID(),
		OrganizationID: nullableUUID(job.OrganizationID),
		JobType:        job.Type,
		SchemaVersion:  job.SchemaVersion,
		Priority:       job.Priority,
		DedupeKey:      job.DedupeKey,
		Payload:        job.Payload,
		RunAt:          job.RunAt.UTC(),
		MaxAttempts:    job.MaxAttempts,
		CorrelationID:  nullableText(job.CorrelationID),
		RequestID:      nullableText(job.RequestID),
		TraceID:        nullableText(job.TraceID),
		CreatedAt:      job.CreatedAt.UTC(),
	})
	if err != nil {
		return false, fmt.Errorf("enqueue job: %w", err)
	}
	return rows == 1, nil
}

func (store *Store) Claim(ctx context.Context, owner string, batchSize int32, lease time.Duration) ([]jobqueue.Job, error) {
	if owner == "" || batchSize < 1 || batchSize > 100 || lease <= 0 {
		return nil, fmt.Errorf("%w: owner, batch size from 1 to 100, and positive lease are required", ErrInvalidRecord)
	}
	rows, err := store.queries.ClaimJobs(ctx, sqlc.ClaimJobsParams{
		LeaseOwner:   nullableText(owner),
		LeaseSeconds: lease.Seconds(),
		BatchSize:    batchSize,
	})
	if err != nil {
		return nil, fmt.Errorf("claim jobs: %w", err)
	}
	jobs := make([]jobqueue.Job, 0, len(rows))
	for _, row := range rows {
		job, mapErr := mapJob(row)
		if mapErr != nil {
			return nil, mapErr
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (store *Store) Succeed(ctx context.Context, id identifier.ID, owner string) error {
	if err := validateLeaseTransition(id, owner); err != nil {
		return err
	}
	rows, err := store.queries.SucceedJob(ctx, sqlc.SucceedJobParams{ID: id.UUID(), LeaseOwner: nullableText(owner)})
	return leaseResult("succeed job", rows, err)
}

func (store *Store) Retry(ctx context.Context, id identifier.ID, owner string, delay time.Duration, failure jobqueue.Failure) error {
	if err := validateLeaseTransition(id, owner); err != nil {
		return err
	}
	if delay < 0 || !validFailure(failure) {
		return fmt.Errorf("%w: non-negative retry delay, valid failure class, and bounded message are required", ErrInvalidRecord)
	}
	rows, err := store.queries.RetryJob(ctx, sqlc.RetryJobParams{
		DelayMilliseconds: float64(delay) / float64(time.Millisecond),
		ErrorClass:        nullableText(failure.Class),
		ErrorMessage:      nullableText(failure.Message),
		ID:                id.UUID(),
		LeaseOwner:        nullableText(owner),
	})
	return leaseResult("retry job", rows, err)
}

func (store *Store) DeadLetter(ctx context.Context, id identifier.ID, owner string, failure jobqueue.Failure) error {
	if err := validateLeaseTransition(id, owner); err != nil {
		return err
	}
	if !validFailure(failure) {
		return fmt.Errorf("%w: valid failure class and bounded message are required", ErrInvalidRecord)
	}
	rows, err := store.queries.DeadLetterJob(ctx, sqlc.DeadLetterJobParams{
		ErrorClass: failureText(failure.Class), ErrorMessage: failureText(failure.Message),
		ID: id.UUID(), LeaseOwner: nullableText(owner),
	})
	return leaseResult("dead-letter job", rows, err)
}

func (store *Store) DeadLetterExhaustedLeases(ctx context.Context) (int64, error) {
	rows, err := store.queries.DeadLetterExhaustedLeases(ctx)
	if err != nil {
		return 0, fmt.Errorf("dead-letter exhausted leases: %w", err)
	}
	return rows, nil
}

func (store *Store) QueueStats(ctx context.Context, jobTypes []string) ([]jobqueue.QueueStat, error) {
	if len(jobTypes) == 0 {
		return nil, nil
	}
	rows, err := store.queries.GetJobQueueStats(ctx, jobTypes)
	if err != nil {
		return nil, fmt.Errorf("get job queue stats: %w", err)
	}
	stats := make([]jobqueue.QueueStat, 0, len(rows))
	for _, row := range rows {
		stats = append(stats, jobqueue.QueueStat{
			Type: row.JobType, Depth: row.Depth,
			OldestAge: time.Duration(row.OldestAgeSeconds * float64(time.Second)),
		})
	}
	return stats, nil
}

func leaseResult(operation string, rows int64, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	if rows != 1 {
		return fmt.Errorf("%s: %w", operation, jobqueue.ErrLeaseLost)
	}
	return nil
}

func validateLeaseTransition(id identifier.ID, owner string) error {
	if id.IsZero() || owner == "" || len(owner) > 128 {
		return fmt.Errorf("%w: job ID and bounded lease owner are required", ErrInvalidRecord)
	}
	return nil
}

func validFailure(failure jobqueue.Failure) bool {
	validClass := failure.Class == "permanent" || failure.Class == "transient" ||
		failure.Class == "conflict" || failure.Class == "unknown"
	return validClass && len(failure.Message) >= 1 && len(failure.Message) <= 1000
}

func failureText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}

func mapJob(row sqlc.Job) (jobqueue.Job, error) {
	id, err := identifier.FromUUID(row.ID)
	if err != nil {
		return jobqueue.Job{}, fmt.Errorf("map job ID: %w", err)
	}
	var organizationID *identifier.ID
	if row.OrganizationID.Valid {
		value, mapErr := identifier.FromUUID(row.OrganizationID.Bytes)
		if mapErr != nil {
			return jobqueue.Job{}, fmt.Errorf("map job organization ID: %w", mapErr)
		}
		organizationID = &value
	}
	if !json.Valid(row.Payload) {
		return jobqueue.Job{}, errors.New("map job: invalid JSON payload")
	}
	return jobqueue.Job{
		ID: id, OrganizationID: organizationID, Type: row.JobType,
		SchemaVersion: row.SchemaVersion, Payload: row.Payload,
		AttemptCount: row.AttemptCount, MaxAttempts: row.MaxAttempts,
		CorrelationID: row.CorrelationID.String, RequestID: row.RequestID.String,
		TraceID: row.TraceID.String,
	}, nil
}
