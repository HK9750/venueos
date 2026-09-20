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
	"github.com/HK9750/venueos/internal/httpapi"
	"github.com/HK9750/venueos/internal/logging"
	"github.com/HK9750/venueos/internal/observability"
	"github.com/HK9750/venueos/internal/user"
	userpostgres "github.com/HK9750/venueos/internal/user/postgres"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if err := run(); err != nil {
		slog.Error("API stopped with an error", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadAPI()
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

	shutdownTracing, err := observability.SetupTracing(ctx, cfg.App.ServiceName, cfg.App.Environment, cfg.Telemetry.OTLPEndpoint, cfg.Telemetry.TraceSampleRate)
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

	repository := userpostgres.New(pool)
	service := user.NewService(repository)
	server := httpapi.NewServer(service, pool, logger)
	metrics := observability.NewMetrics()
	handler := httpapi.NewHandler(server, cfg.Telemetry, logger, metrics)
	listeners := []httpapi.Listener{{Name: "api", Config: cfg.HTTP, Handler: handler}}
	if cfg.Telemetry.MetricsEnabled || cfg.Telemetry.PprofEnabled {
		adminHTTP := cfg.HTTP
		adminHTTP.Address = cfg.Telemetry.AdminAddress
		listeners = append(listeners, httpapi.Listener{
			Name:    "admin",
			Config:  adminHTTP,
			Handler: httpapi.NewAdminHandler(cfg.Telemetry, logger, metrics),
		})
	}

	logger.Info("API starting", slog.String("version", version), slog.String("commit", commit))
	if err := httpapi.RunAll(ctx, logger, listeners...); err != nil {
		return fmt.Errorf("run API: %w", err)
	}
	return nil
}
