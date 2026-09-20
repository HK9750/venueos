package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/HK9750/venueos/internal/config"
	"github.com/HK9750/venueos/internal/database"
	"github.com/HK9750/venueos/internal/logging"
	"github.com/HK9750/venueos/internal/observability"
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

	logger.Info("worker starting", slog.String("version", version), slog.String("commit", commit))
	if err := worker.New(pool, logger, cfg.Worker.Interval).Run(ctx); err != nil {
		return fmt.Errorf("run worker: %w", err)
	}
	return nil
}
