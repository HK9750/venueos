package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/jobqueue"
)

type fakeStore struct {
	mu         sync.Mutex
	jobs       []jobqueue.Job
	claimCalls int
	succeeded  int
	retried    int
	dead       int
	delay      time.Duration
	failure    jobqueue.Failure
	onClaim    func()
}

func (store *fakeStore) Claim(context.Context, string, int32, time.Duration) ([]jobqueue.Job, error) {
	store.mu.Lock()
	store.claimCalls++
	jobs := store.jobs
	store.jobs = nil
	onClaim := store.onClaim
	store.mu.Unlock()
	if onClaim != nil {
		onClaim()
	}
	return jobs, nil
}
func (store *fakeStore) Succeed(context.Context, identifier.ID, string) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.succeeded++
	return nil
}
func (store *fakeStore) Retry(_ context.Context, _ identifier.ID, _ string, delay time.Duration, failure jobqueue.Failure) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.retried++
	store.delay, store.failure = delay, failure
	return nil
}
func (store *fakeStore) DeadLetter(_ context.Context, _ identifier.ID, _ string, failure jobqueue.Failure) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.dead++
	store.failure = failure
	return nil
}
func (*fakeStore) DeadLetterExhaustedLeases(context.Context) (int64, error) { return 0, nil }
func (*fakeStore) QueueStats(context.Context, []string) ([]jobqueue.QueueStat, error) {
	return nil, nil
}

type metricCall struct{ jobType, result, class string }
type fakeMetrics struct{ calls []metricCall }

func (metrics *fakeMetrics) ObserveJob(jobType, result, class string, _ time.Duration) {
	metrics.calls = append(metrics.calls, metricCall{jobType, result, class})
}
func (*fakeMetrics) ObserveJobQueue(string, int64, time.Duration) {}

func TestRunnerRunsImmediatelyAndStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &fakeStore{onClaim: cancel}
	runner := newTestRunner(t, store, NewRegistry(), nil)

	if err := runner.Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if store.claimCalls != 1 {
		t.Fatalf("claim calls = %d, want 1", store.claimCalls)
	}
}

func TestRunnerCompletesSuccessfulJob(t *testing.T) {
	job := testJob(t, 1, 3)
	store := &fakeStore{jobs: []jobqueue.Job{job}}
	registry := NewRegistry()
	if err := registry.Register(job.Type, job.SchemaVersion, func(context.Context, jobqueue.Job) error { return nil }); err != nil {
		t.Fatal(err)
	}
	metrics := &fakeMetrics{}

	newTestRunner(t, store, registry, metrics).runOnce(context.Background())
	if store.succeeded != 1 || store.retried != 0 || store.dead != 0 {
		t.Fatalf("transitions = success %d retry %d dead %d", store.succeeded, store.retried, store.dead)
	}
	if len(metrics.calls) != 1 || metrics.calls[0].result != "succeeded" {
		t.Fatalf("metric calls = %#v", metrics.calls)
	}
}

func TestRunnerRetriesTransientFailureWithBoundedDelay(t *testing.T) {
	job := testJob(t, 3, 5)
	store := &fakeStore{jobs: []jobqueue.Job{job}}
	registry := NewRegistry()
	handlerErr := &FailureError{Class: FailureTransient, SafeMessage: " provider unavailable\n", RetryAfter: time.Hour, Cause: errors.New("secret raw provider response")}
	if err := registry.Register(job.Type, job.SchemaVersion, func(context.Context, jobqueue.Job) error { return handlerErr }); err != nil {
		t.Fatal(err)
	}

	newTestRunner(t, store, registry, nil).runOnce(context.Background())
	if store.retried != 1 || store.delay != 8*time.Second {
		t.Fatalf("retry count/delay = %d/%s, want 1/8s", store.retried, store.delay)
	}
	if store.failure.Class != FailureTransient || store.failure.Message != "provider unavailable" {
		t.Fatalf("failure = %#v", store.failure)
	}
}

func TestRunnerDeadLettersPermanentFinalAndUnsupportedJobs(t *testing.T) {
	tests := []struct {
		name       string
		job        jobqueue.Job
		register   bool
		handlerErr error
		wantClass  string
		metricType string
	}{
		{name: "permanent", job: testJob(t, 1, 3), register: true, handlerErr: &FailureError{Class: FailurePermanent, SafeMessage: "Invalid payload."}, wantClass: FailurePermanent, metricType: "ticket.issue"},
		{name: "final attempt", job: testJob(t, 3, 3), register: true, handlerErr: errors.New("internal detail"), wantClass: FailureUnknown, metricType: "ticket.issue"},
		{name: "unsupported", job: testJob(t, 1, 3), wantClass: FailurePermanent, metricType: "unsupported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &fakeStore{jobs: []jobqueue.Job{test.job}}
			registry := NewRegistry()
			if test.register {
				if err := registry.Register(test.job.Type, test.job.SchemaVersion, func(context.Context, jobqueue.Job) error { return test.handlerErr }); err != nil {
					t.Fatal(err)
				}
			}
			metrics := &fakeMetrics{}
			newTestRunner(t, store, registry, metrics).runOnce(context.Background())
			if store.dead != 1 || store.failure.Class != test.wantClass {
				t.Fatalf("dead/failure = %d/%#v", store.dead, store.failure)
			}
			if len(metrics.calls) != 1 || metrics.calls[0].jobType != test.metricType {
				t.Fatalf("metric calls = %#v", metrics.calls)
			}
		})
	}
}

func TestRunnerLeavesLeaseForReclaimOnShutdown(t *testing.T) {
	job := testJob(t, 1, 3)
	store := &fakeStore{jobs: []jobqueue.Job{job}}
	registry := NewRegistry()
	ctx, cancel := context.WithCancel(context.Background())
	if err := registry.Register(job.Type, job.SchemaVersion, func(context.Context, jobqueue.Job) error {
		cancel()
		return context.Canceled
	}); err != nil {
		t.Fatal(err)
	}

	newTestRunner(t, store, registry, nil).runOnce(ctx)
	if store.succeeded+store.retried+store.dead != 0 {
		t.Fatalf("shutdown mutated leased job: %#v", store)
	}
}

func TestRunnerRetriesHandlerThatReturnsSuccessAfterDeadline(t *testing.T) {
	job := testJob(t, 1, 3)
	store := &fakeStore{jobs: []jobqueue.Job{job}}
	registry := NewRegistry()
	if err := registry.Register(job.Type, job.SchemaVersion, func(ctx context.Context, _ jobqueue.Job) error {
		<-ctx.Done()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runner := newTestRunner(t, store, registry, nil)
	runner.config.JobTimeout = time.Millisecond

	runner.runOnce(context.Background())
	if store.retried != 1 || store.failure.Class != FailureTransient {
		t.Fatalf("retry/failure = %d/%#v", store.retried, store.failure)
	}
}

func TestRunnerStartsClaimedBatchWithinOneLeaseWindow(t *testing.T) {
	first := testJob(t, 1, 3)
	second := testJob(t, 1, 3)
	store := &fakeStore{jobs: []jobqueue.Job{first, second}}
	registry := NewRegistry()
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	if err := registry.Register(first.Type, first.SchemaVersion, func(context.Context, jobqueue.Job) error {
		started <- struct{}{}
		<-release
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runner := newTestRunner(t, store, registry, nil)
	done := make(chan struct{})
	go func() {
		runner.runOnce(context.Background())
		close(done)
	}()

	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("claimed jobs did not start concurrently")
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("runner did not finish claimed batch")
	}
	if store.succeeded != 2 {
		t.Fatalf("successful jobs = %d, want 2", store.succeeded)
	}
}

func newTestRunner(t *testing.T, store jobqueue.Store, registry *Registry, metrics Metrics) *Runner {
	t.Helper()
	runner, err := New(store, registry, metrics, slog.New(slog.NewTextHandler(io.Discard, nil)), Config{
		Owner: "worker-test", PollInterval: time.Hour, BatchSize: 10,
		LeaseDuration: time.Minute, JobTimeout: 30 * time.Second,
		RetryBase: 2 * time.Second, RetryMax: 8 * time.Second,
		jitter: func(maximum time.Duration) time.Duration { return maximum },
	})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func testJob(t *testing.T, attempt, maximum int32) jobqueue.Job {
	t.Helper()
	id, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	return jobqueue.Job{ID: id, Type: "ticket.issue", SchemaVersion: 1, Payload: []byte(`{"order_id":"example"}`), AttemptCount: attempt, MaxAttempts: maximum}
}
