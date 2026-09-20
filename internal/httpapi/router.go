package httpapi

import (
	"log/slog"
	"net/http"
	"net/http/pprof"

	"github.com/HK9750/venueos/internal/config"
	"github.com/HK9750/venueos/internal/observability"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func NewHandler(server *Server, cfg config.Telemetry, logger *slog.Logger, metrics *observability.Metrics) http.Handler {
	mux := http.NewServeMux()
	generated := HandlerWithOptions(server, StdHTTPServerOptions{
		BaseRouter: mux,
		ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeProblem(w, r, http.StatusBadRequest, "Invalid request parameter", err.Error(), nil)
		},
	})

	handler := Recover(logger)(generated)
	if cfg.MetricsEnabled {
		handler = metrics.Middleware(handler)
	}
	handler = Chain(handler, AccessLog(logger), SecurityHeaders)
	handler = otelhttp.NewHandler(handler, "http.server")
	return RequestID(handler)
}

func NewAdminHandler(cfg config.Telemetry, logger *slog.Logger, metrics *observability.Metrics) http.Handler {
	mux := http.NewServeMux()
	if cfg.MetricsEnabled {
		mux.Handle("GET /metrics", metrics.Handler())
	}
	if cfg.PprofEnabled {
		registerPprof(mux)
	}

	handler := Recover(logger)(mux)
	handler = Chain(handler, AccessLog(logger), SecurityHeaders)
	handler = otelhttp.NewHandler(handler, "http.admin")
	return RequestID(handler)
}

func registerPprof(mux *http.ServeMux) {
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("POST /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
}
