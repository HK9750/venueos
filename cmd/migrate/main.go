package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/HK9750/venueos/internal/config"
	"github.com/HK9750/venueos/internal/logging"
	"github.com/HK9750/venueos/migrations"
	"github.com/pressly/goose/v3"

	_ "github.com/jackc/pgx/v5/stdlib"
)

var allowedCommands = map[string]struct{}{
	"up": {}, "up-by-one": {}, "up-to": {}, "down": {}, "down-to": {},
	"redo": {}, "status": {}, "version": {},
}

func main() {
	if err := run(); err != nil {
		slog.Error("migration failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: migrate <up|up-by-one|up-to|down|down-to|redo|status|version> [version]")
	}
	command := os.Args[1]
	if _, allowed := allowedCommands[command]; !allowed {
		return fmt.Errorf("unsupported migration command %q", command)
	}

	cfg, err := config.LoadMigration()
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

	db, err := sql.Open("pgx", cfg.Database.URL)
	if err != nil {
		return fmt.Errorf("open migration database: %w", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			logger.Warn("close migration database", slog.Any("error", err))
		}
	}()
	db.SetMaxOpenConns(1)
	connectCtx, cancel := context.WithTimeout(ctx, cfg.Database.ConnectTimeout)
	defer cancel()
	if err := db.PingContext(connectCtx); err != nil {
		return fmt.Errorf("ping migration database: %w", err)
	}

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set migration dialect: %w", err)
	}
	if err := goose.RunContext(ctx, command, db, ".", os.Args[2:]...); err != nil {
		return fmt.Errorf("run migration %s: %w", command, err)
	}
	logger.Info("migration command completed", slog.String("command", command))
	return nil
}
