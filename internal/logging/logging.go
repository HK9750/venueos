// Package logging defines the process-wide structured logging policy.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/HK9750/venueos/internal/config"
)

func New(out io.Writer, app config.App, cfg config.Log) (*slog.Logger, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToLower(cfg.Level))); err != nil {
		return nil, fmt.Errorf("parse log level: %w", err)
	}

	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	switch strings.ToLower(cfg.Format) {
	case "json":
		handler = slog.NewJSONHandler(out, opts)
	case "text":
		handler = slog.NewTextHandler(out, opts)
	default:
		return nil, fmt.Errorf("unsupported log format %q", cfg.Format)
	}

	return slog.New(handler).With(
		slog.String("service", app.ServiceName),
		slog.String("environment", app.Environment),
	), nil
}
