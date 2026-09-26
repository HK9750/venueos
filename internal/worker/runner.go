// Package worker claims and executes durable PostgreSQL-backed jobs.
package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/HK9750/venueos/internal/platform/jobqueue"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("github.com/HK9750/venueos/internal/worker")

const (
	FailurePermanent = "permanent"
	FailureTransient = "transient"
	FailureConflict  = "conflict"
	FailureUnknown   = "unknown"
)

type Handler func(context.Context, jobqueue.Job) error

type Registry struct {
	handlers map[handlerKey]Handler
}

type handlerKey struct {
	typeName string
	version  int32
}

func NewRegistry() *Registry { return &Registry{handlers: make(map[handlerKey]Handler)} }

func (registry *Registry) Register(typeName string, version int32, handler Handler) error {
	if registry == nil || strings.TrimSpace(typeName) == "" || version <= 0 || handler == nil {
		return errors.New("job type, positive schema version, and handler are required")
	}
	key := handlerKey{typeName: typeName, version: version}
	if _, exists := registry.handlers[key]; exists {
		return fmt.Errorf("handler already registered for %s version %d", typeName, version)
	}
	registry.handlers[key] = handler
	return nil
}

func (registry *Registry) lookup(job jobqueue.Job) (Handler, bool) {
	if registry == nil {
		return nil, false
	}
	handler, ok := registry.handlers[handlerKey{typeName: job.Type, version: job.SchemaVersion}]
	return handler, ok
}

func (registry *Registry) jobTypes() []string {
	types := make([]string, 0, len(registry.handlers))
	for key := range registry.handlers {
		if !slices.Contains(types, key.typeName) {
			types = append(types, key.typeName)
		}
	}
	slices.Sort(types)
	return types
}

// FailureError lets a handler expose only an explicitly safe persisted message.
// Cause is available for errors.Is/errors.As but is never logged by the runner.
type FailureError struct {
	Class       string
	SafeMessage string
	RetryAfter  time.Duration
	Cause       error
}

func (failure *FailureError) Error() string {
	if failure.Cause != nil {
		return failure.Cause.Error()
	}
	return failure.SafeMessage
}

func (failure *FailureError) Unwrap() error { return failure.Cause }

type Metrics interface {
	ObserveJob(jobType, result, failureClass string, duration time.Duration)
	ObserveJobQueue(jobType string, depth int64, oldestAge time.Duration)
}

type Config struct {
	Owner         string
	PollInterval  time.Duration
	BatchSize     int32
	LeaseDuration time.Duration
	JobTimeout    time.Duration
	RetryBase     time.Duration
	RetryMax      time.Duration
	jitter        func(time.Duration) time.Duration
}

type Runner struct {
	store    jobqueue.Store
	registry *Registry
	metrics  Metrics
	logger   *slog.Logger
	config   Config
}

func New(store jobqueue.Store, registry *Registry, metrics Metrics, logger *slog.Logger, cfg Config) (*Runner, error) {
	if store == nil || registry == nil || logger == nil {
		return nil, errors.New("job store, registry, and logger are required")
	}
	if strings.TrimSpace(cfg.Owner) == "" || len(cfg.Owner) > 128 || cfg.PollInterval <= 0 ||
		cfg.BatchSize < 1 || cfg.BatchSize > 100 || cfg.LeaseDuration <= cfg.JobTimeout ||
		cfg.JobTimeout <= 0 || cfg.RetryBase <= 0 || cfg.RetryMax < cfg.RetryBase {
		return nil, errors.New("invalid worker configuration")
	}
	if cfg.jitter == nil {
		cfg.jitter = func(maximum time.Duration) time.Duration {
			if maximum <= 0 {
				return 0
			}
			return time.Duration(rand.Int64N(int64(maximum) + 1))
		}
	}
	return &Runner{store: store, registry: registry, metrics: metrics, logger: logger, config: cfg}, nil
}

func (r *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.config.PollInterval)
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
	if ctx.Err() != nil {
		return
	}
	dead, err := r.store.DeadLetterExhaustedLeases(ctx)
	if err != nil {
		r.logger.ErrorContext(ctx, "failed to sweep exhausted job leases", slog.Any("error", err))
		return
	}
	if dead > 0 {
		r.logger.WarnContext(ctx, "dead-lettered exhausted job leases", slog.Int64("count", dead))
		r.observe("expired_lease", "dead_letter", FailureUnknown, 0)
	}
	if r.metrics != nil {
		jobTypes := r.registry.jobTypes()
		for _, jobType := range jobTypes {
			r.metrics.ObserveJobQueue(jobType, 0, 0)
		}
		stats, statsErr := r.store.QueueStats(ctx, jobTypes)
		if statsErr != nil {
			r.logger.ErrorContext(ctx, "failed to inspect job queue", slog.Any("error", statsErr))
		} else {
			for _, stat := range stats {
				r.metrics.ObserveJobQueue(stat.Type, stat.Depth, stat.OldestAge)
			}
		}
	}
	jobs, err := r.store.Claim(ctx, r.config.Owner, r.config.BatchSize, r.config.LeaseDuration)
	if err != nil {
		r.logger.ErrorContext(ctx, "failed to claim jobs", slog.Any("error", err))
		return
	}
	var workers sync.WaitGroup
	for _, job := range jobs {
		if ctx.Err() != nil {
			break
		}
		workers.Go(func() { r.process(ctx, job) })
	}
	workers.Wait()
}

func (r *Runner) process(ctx context.Context, job jobqueue.Job) {
	started := time.Now()
	handler, supported := r.registry.lookup(job)
	metricType := job.Type
	if !supported {
		metricType = "unsupported"
	}
	jobCtx, cancel := context.WithTimeout(ctx, r.config.JobTimeout)
	defer cancel()
	jobCtx, span := tracer.Start(jobCtx, "worker.job", trace.WithAttributes(
		attribute.String("job.type", metricType),
		attribute.Int("job.schema_version", int(job.SchemaVersion)),
		attribute.Int("job.attempt", int(job.AttemptCount)),
	))
	defer span.End()

	if !supported {
		failure := jobqueue.Failure{Class: FailurePermanent, Message: "No handler is registered for this job type and schema version."}
		r.finishDeadLetter(ctx, job, metricType, failure, started)
		span.SetStatus(codes.Error, "unsupported job")
		return
	}

	err := handler(jobCtx, job)
	if ctx.Err() != nil {
		// Shutdown leaves the durable lease intact for safe reclamation.
		span.SetStatus(codes.Error, "worker shutting down")
		return
	}
	if err == nil && jobCtx.Err() != nil {
		err = jobCtx.Err()
	}
	if err == nil {
		if transitionErr := r.store.Succeed(ctx, job.ID, r.config.Owner); transitionErr != nil {
			r.logTransitionError(jobCtx, job, "succeed", transitionErr)
			span.RecordError(transitionErr)
			span.SetStatus(codes.Error, "job completion failed")
			return
		}
		r.observe(metricType, "succeeded", "", time.Since(started))
		return
	}

	failure, retryAfter := classify(err)
	span.SetAttributes(attribute.String("job.failure_class", failure.Class))
	span.SetStatus(codes.Error, "job handler failed")
	if failure.Class == FailurePermanent || job.AttemptCount >= job.MaxAttempts {
		r.finishDeadLetter(ctx, job, metricType, failure, started)
		return
	}
	delay := r.retryDelay(job.AttemptCount, retryAfter)
	if transitionErr := r.store.Retry(ctx, job.ID, r.config.Owner, delay, failure); transitionErr != nil {
		r.logTransitionError(jobCtx, job, "retry", transitionErr)
		span.RecordError(transitionErr)
		return
	}
	r.observe(metricType, "retry", failure.Class, time.Since(started))
}

func (r *Runner) finishDeadLetter(ctx context.Context, job jobqueue.Job, metricType string, failure jobqueue.Failure, started time.Time) {
	if err := r.store.DeadLetter(ctx, job.ID, r.config.Owner, failure); err != nil {
		r.logTransitionError(ctx, job, "dead_letter", err)
		return
	}
	r.observe(metricType, "dead_letter", failure.Class, time.Since(started))
}

func (r *Runner) retryDelay(attempt int32, requested time.Duration) time.Duration {
	maximum := r.config.RetryBase
	for current := int32(1); current < attempt && maximum < r.config.RetryMax; current++ {
		if maximum > r.config.RetryMax/2 {
			maximum = r.config.RetryMax
			break
		}
		maximum *= 2
	}
	if maximum > r.config.RetryMax {
		maximum = r.config.RetryMax
	}
	if requested > 0 {
		return min(requested, r.config.RetryMax)
	}
	return r.config.jitter(maximum)
}

func (r *Runner) observe(jobType, result, failureClass string, duration time.Duration) {
	if r.metrics != nil {
		r.metrics.ObserveJob(jobType, result, failureClass, duration)
	}
}

func (r *Runner) logTransitionError(ctx context.Context, job jobqueue.Job, transition string, err error) {
	r.logger.ErrorContext(ctx, "job state transition failed",
		slog.String("job_id", job.ID.String()), slog.String("job_type", job.Type),
		slog.String("transition", transition), slog.Any("error", err))
}

func classify(err error) (jobqueue.Failure, time.Duration) {
	var failureError *FailureError
	if errors.As(err, &failureError) {
		class := failureError.Class
		if class != FailurePermanent && class != FailureTransient && class != FailureConflict && class != FailureUnknown {
			class = FailureUnknown
		}
		message := safeMessage(failureError.SafeMessage, "Job handler failed.")
		return jobqueue.Failure{Class: class, Message: message}, max(failureError.RetryAfter, 0)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return jobqueue.Failure{Class: FailureTransient, Message: "Job handler timed out."}, 0
	}
	return jobqueue.Failure{Class: FailureUnknown, Message: "Job handler failed."}, 0
}

func safeMessage(message, fallback string) string {
	message = strings.Join(strings.Fields(message), " ")
	if message == "" {
		message = fallback
	}
	if len(message) <= 1000 {
		return message
	}
	message = message[:1000]
	for !utf8.ValidString(message) {
		message = message[:len(message)-1]
	}
	return message
}
