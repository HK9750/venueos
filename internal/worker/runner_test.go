package worker

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

type dependencyFunc func(context.Context) error

func (f dependencyFunc) Ping(ctx context.Context) error { return f(ctx) }

func TestRunnerRunsImmediatelyAndStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	called := make(chan struct{}, 1)
	dependency := dependencyFunc(func(context.Context) error {
		called <- struct{}{}
		cancel()
		return nil
	})
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if err := New(dependency, logger, time.Hour).Run(ctx); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	select {
	case <-called:
	default:
		t.Fatal("dependency was not called before shutdown")
	}
}
