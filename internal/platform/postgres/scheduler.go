package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/postgres/sqlc"
	"github.com/jackc/pgx/v5"
)

type JobScheduleDefinition struct {
	ScheduleKey    string
	OrganizationID *identifier.ID
	JobType        string
	SchemaVersion  int32
	Priority       int16
	Payload        json.RawMessage
	Interval       time.Duration
	NextRunAt      time.Time
	MaxAttempts    int32
	CreatedAt      time.Time
}

func (store *Store) EnsureJobSchedule(ctx context.Context, schedule JobScheduleDefinition) error {
	intervalSeconds, err := scheduleIntervalSeconds(schedule.Interval)
	if err != nil {
		return err
	}
	if schedule.ScheduleKey == "" || schedule.JobType == "" || schedule.SchemaVersion < 1 || !json.Valid(schedule.Payload) || schedule.NextRunAt.IsZero() || schedule.MaxAttempts < 1 || schedule.CreatedAt.IsZero() {
		return fmt.Errorf("%w: schedule identity, payload, times, and attempts are required", ErrInvalidRecord)
	}
	if err := store.queries.EnsureJobSchedule(ctx, sqlc.EnsureJobScheduleParams{
		ScheduleKey: schedule.ScheduleKey, OrganizationID: nullableUUID(schedule.OrganizationID), JobType: schedule.JobType,
		SchemaVersion: schedule.SchemaVersion, Priority: schedule.Priority, Payload: schedule.Payload,
		IntervalSeconds: intervalSeconds, NextRunAt: schedule.NextRunAt.UTC(), MaxAttempts: schedule.MaxAttempts, CreatedAt: schedule.CreatedAt.UTC(),
	}); err != nil {
		return fmt.Errorf("ensure job schedule: %w", err)
	}
	return nil
}

// RunDueJobSchedule claims one due schedule and enqueues its job in the same
// transaction. A schedule can therefore be claimed by only one worker replica.
func (store *Store) RunDueJobSchedule(ctx context.Context, transactions *database.TransactionRunner, now time.Time, jobID identifier.ID) (bool, error) {
	if transactions == nil || now.IsZero() || jobID.IsZero() {
		return false, fmt.Errorf("%w: transaction runner, time, and job ID are required", ErrInvalidRecord)
	}
	claimed := false
	err := transactions.Run(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(ctx context.Context, tx pgx.Tx) error {
		queries := store.queries.WithTx(tx)
		schedule, err := queries.ClaimDueJobSchedule(ctx, now.UTC())
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("claim due job schedule: %w", err)
		}
		dedupeKey := schedule.ScheduleKey + ":" + strconv.FormatInt(schedule.DueAt.UnixNano(), 10)
		if _, err := queries.EnqueueJob(ctx, sqlc.EnqueueJobParams{
			ID: jobID.UUID(), OrganizationID: schedule.OrganizationID, JobType: schedule.JobType, SchemaVersion: schedule.SchemaVersion,
			Priority: schedule.Priority, DedupeKey: dedupeKey, Payload: schedule.Payload, RunAt: now.UTC(), MaxAttempts: schedule.MaxAttempts,
			CreatedAt: now.UTC(),
		}); err != nil {
			return fmt.Errorf("enqueue scheduled job: %w", err)
		}
		claimed = true
		return nil
	})
	return claimed, err
}

func scheduleIntervalSeconds(interval time.Duration) (int32, error) {
	if interval < time.Second || interval%time.Second != 0 || interval > 24*time.Hour {
		return 0, fmt.Errorf("%w: schedule interval must be a whole number of seconds from 1s to 24h", ErrInvalidRecord)
	}
	return int32(interval / time.Second), nil
}
