// Package worker contains background process behavior independent of process wiring.
package worker

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

var tracer = otel.Tracer("github.com/HK9750/venueos/internal/worker")

type Dependency interface {
	Ping(context.Context) error
}

type Runner struct {
	dependency Dependency
	logger     *slog.Logger
	interval   time.Duration
}

func New(dependency Dependency, logger *slog.Logger, interval time.Duration) *Runner {
	return &Runner{dependency: dependency, logger: logger, interval: interval}
}

func (r *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	r.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			r.logger.Info("worker stopped")
			return nil
		case <-ticker.C:
			r.runOnce(ctx)
		}
	}
}

func (r *Runner) runOnce(ctx context.Context) {
	jobCtx, cancel := context.WithTimeout(ctx, min(r.interval, 10*time.Second))
	defer cancel()
	jobCtx, span := tracer.Start(jobCtx, "worker.example_job")
	defer span.End()
	if err := r.dependency.Ping(jobCtx); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "dependency ping failed")
		r.logger.ErrorContext(jobCtx, "example job failed", slog.Any("error", err))
		return
	}
	r.logger.DebugContext(jobCtx, "example job completed")
}
