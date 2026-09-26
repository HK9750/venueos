package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/HK9750/venueos/internal/config"
	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/httpapi"
	"github.com/HK9750/venueos/internal/logging"
	"github.com/HK9750/venueos/internal/observability"
	"github.com/HK9750/venueos/internal/platform/identifier"
	platformpostgres "github.com/HK9750/venueos/internal/platform/postgres"
	"github.com/HK9750/venueos/internal/worker"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker stopped with an error", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadWorker()
	if err != nil {
		return err
	}
	logger, err := logging.New(os.Stdout, cfg.App, cfg.Log)
	if err != nil {
		return err
	}
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownTracing, err := observability.SetupTracing(ctx, cfg.App.ServiceName+"-worker", cfg.App.Environment, cfg.Telemetry.OTLPEndpoint, cfg.Telemetry.TraceSampleRate)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTracing(shutdownCtx); err != nil {
			logger.Error("telemetry shutdown failed", slog.Any("error", err))
		}
	}()

	pool, err := database.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	workerID, err := identifier.New()
	if err != nil {
		return err
	}
	metrics := observability.NewMetrics()
	runner, err := worker.New(
		platformpostgres.NewStore(pool),
		worker.NewRegistry(),
		metrics,
		logger,
		worker.Config{
			Owner:         "worker-" + workerID.String(),
			PollInterval:  cfg.Worker.Interval,
			BatchSize:     cfg.Worker.BatchSize,
			LeaseDuration: cfg.Worker.LeaseDuration,
			JobTimeout:    cfg.Worker.JobTimeout,
			RetryBase:     cfg.Worker.RetryBaseDelay,
			RetryMax:      cfg.Worker.RetryMaxDelay,
		},
	)
	if err != nil {
		return err
	}

	logger.Info("worker starting", slog.String("version", version), slog.String("commit", commit))
	if err := runServices(ctx, cfg, logger, metrics, runner); err != nil {
		return fmt.Errorf("run worker: %w", err)
	}
	return nil
}

func runServices(ctx context.Context, cfg config.WorkerConfig, logger *slog.Logger, metrics *observability.Metrics, runner *worker.Runner) error {
	if !cfg.Telemetry.MetricsEnabled && !cfg.Telemetry.PprofEnabled {
		return runner.Run(ctx)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 2)
	go func() { errCh <- runner.Run(runCtx) }()
	go func() {
		adminConfig := config.HTTP{
			Address: cfg.Telemetry.AdminAddress, ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second,
			IdleTimeout: 60 * time.Second, ShutdownTimeout: 15 * time.Second,
		}
		errCh <- httpapi.Run(runCtx, adminConfig, httpapi.NewAdminHandler(cfg.Telemetry, logger, metrics), logger)
	}()

	first := <-errCh
	cancel()
	second := <-errCh
	return errors.Join(first, second)
}
