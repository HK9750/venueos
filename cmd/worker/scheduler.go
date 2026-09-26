package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/platform/identifier"
	platformpostgres "github.com/HK9750/venueos/internal/platform/postgres"
)

func recurringSchedules(now time.Time) []platformpostgres.JobScheduleDefinition {
	payload := json.RawMessage(`{}`)
	return []platformpostgres.JobScheduleDefinition{
		{ScheduleKey: "invitation.expire", JobType: "invitation.expire", SchemaVersion: 1, Priority: 50, Payload: payload, Interval: time.Minute, NextRunAt: now, MaxAttempts: 3, CreatedAt: now},
		{ScheduleKey: "holds.expire", JobType: "holds.expire", SchemaVersion: 1, Priority: 100, Payload: payload, Interval: 5 * time.Second, NextRunAt: now, MaxAttempts: 3, CreatedAt: now},
		{ScheduleKey: "realtime_events.prune", JobType: "realtime_events.prune", SchemaVersion: 1, Priority: 20, Payload: payload, Interval: 10 * time.Minute, NextRunAt: now, MaxAttempts: 3, CreatedAt: now},
		{ScheduleKey: "idempotency.prune", JobType: "idempotency.prune", SchemaVersion: 1, Priority: 20, Payload: payload, Interval: time.Hour, NextRunAt: now, MaxAttempts: 3, CreatedAt: now},
	}
}

func runScheduleLoop(ctx context.Context, interval time.Duration, logger *slog.Logger, store *platformpostgres.Store, transactions *database.TransactionRunner) {
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	claim := func() {
		jobID, err := identifier.New()
		if err != nil {
			logger.ErrorContext(ctx, "scheduled job identifier generation failed", slog.Any("error", err))
			return
		}
		claimed, err := store.RunDueJobSchedule(ctx, transactions, time.Now().UTC(), jobID)
		if err != nil && ctx.Err() == nil {
			logger.ErrorContext(ctx, "scheduled job enqueue failed", slog.Any("error", err))
			return
		}
		if claimed {
			logger.DebugContext(ctx, "scheduled job enqueued")
		}
	}

	claim()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			claim()
		}
	}
}
