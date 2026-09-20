package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/HK9750/venueos/internal/config"
)

func Run(ctx context.Context, cfg config.HTTP, handler http.Handler, logger *slog.Logger) error {
	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           handler,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.InfoContext(ctx, "HTTP server listening", slog.String("address", cfg.Address))
		errCh <- server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down HTTP server: %w", err)
		}
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("stop HTTP server: %w", err)
		}
		logger.Info("HTTP server stopped")
		return nil
	}
}

type Listener struct {
	Name    string
	Config  config.HTTP
	Handler http.Handler
}

func RunAll(ctx context.Context, logger *slog.Logger, listeners ...Listener) error {
	if len(listeners) == 0 {
		return errors.New("at least one HTTP listener is required")
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, len(listeners))
	for _, listener := range listeners {
		go func() {
			err := Run(runCtx, listener.Config, listener.Handler, logger.With(slog.String("listener", listener.Name)))
			errCh <- err
		}()
	}

	firstErr := <-errCh
	cancel()
	errs := []error{firstErr}
	for range len(listeners) - 1 {
		errs = append(errs, <-errCh)
	}
	return errors.Join(errs...)
}
