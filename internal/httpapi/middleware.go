package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/HK9750/venueos/pkg/requestid"
	"go.opentelemetry.io/otel/trace"
)

type Middleware func(http.Handler) http.Handler

func Chain(handler http.Handler, middleware ...Middleware) http.Handler {
	for index := len(middleware) - 1; index >= 0; index-- {
		handler = middleware[index](handler)
	}
	return handler
}

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(requestid.Header)
		if !requestid.Valid(id) {
			id = requestid.New()
		}
		w.Header().Set(requestid.Header, id)
		next.ServeHTTP(w, r.WithContext(requestid.WithContext(r.Context(), id)))
	})
}

func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func Recover(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writer := &responseWriter{ResponseWriter: w, status: http.StatusOK}
			defer func() {
				if recovered := recover(); recovered != nil {
					logger.ErrorContext(r.Context(), "panic recovered",
						slog.Any("panic", recovered),
						slog.String("stack", string(debug.Stack())),
						slog.Group("request", requestLogAttrs(r.Context())...),
					)
					if !writer.wroteHeader {
						writeProblem(writer, r, http.StatusInternalServerError, "Internal server error", "an unexpected error occurred", nil)
					}
				}
			}()
			next.ServeHTTP(writer, r)
		})
	}
}

func AccessLog(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			writer := &responseWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(writer, r)
			logger.InfoContext(r.Context(), "request completed",
				slog.String("request_id", requestid.FromContext(r.Context())),
				slog.String("method", r.Method),
				slog.String("route", r.Pattern),
				slog.Int("status", writer.status),
				slog.Int64("response_bytes", writer.bytes),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.String("trace_id", traceID(r.Context())),
			)
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	bytes       int64
}

func (w *responseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(bytes []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	written, err := w.ResponseWriter.Write(bytes)
	w.bytes += int64(written)
	return written, err
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func requestLogAttrs(ctx context.Context) []any {
	return []any{
		slog.String("request_id", requestid.FromContext(ctx)),
		slog.String("trace_id", traceID(ctx)),
	}
}

func traceID(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}
