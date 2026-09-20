package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/config"
	"github.com/HK9750/venueos/internal/observability"
	"github.com/HK9750/venueos/internal/user"
	"github.com/google/uuid"
)

type stubUsers struct{}

func (stubUsers) Create(_ context.Context, input user.CreateInput) (user.User, error) {
	now := time.Now().UTC()
	return user.User{ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Email: input.Email, Name: input.Name, CreatedAt: now, UpdatedAt: now}, nil
}

func (stubUsers) Get(context.Context, uuid.UUID) (user.User, error) {
	return user.User{}, user.ErrNotFound
}

func (stubUsers) List(context.Context, int32, int32) ([]user.User, error) {
	return []user.User{}, nil
}

type ready struct{}

func (ready) Ping(context.Context) error { return nil }

type panicUsers struct{ stubUsers }

func (panicUsers) List(context.Context, int32, int32) ([]user.User, error) {
	panic("boom")
}

func TestCreateUser(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(stubUsers{}, ready{}, logger)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/users", strings.NewReader(`{"email":"ada@example.com","name":"Ada"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusCreated, recorder.Body.String())
	}
	var response User
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Email != "ada@example.com" {
		t.Fatalf("email = %q, want ada@example.com", response.Email)
	}
}

func TestGeneratedParameterErrorsUseProblemDetails(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(stubUsers{}, ready{}, logger)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/users?limit=not-a-number", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "application/problem+json" {
		t.Fatalf("Content-Type = %q", contentType)
	}
}

func TestListUsersRejectsInt32Overflow(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(stubUsers{}, ready{}, logger)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/users?limit=4294967297", nil)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestCreateUserRejectsOversizedBody(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := NewServer(stubUsers{}, ready{}, logger)
	handler := NewHandler(server, config.Telemetry{}, logger, observability.NewMetrics())
	body := `{"email":"ada@example.com","name":"` + strings.Repeat("a", maxRequestBodyBytes) + `"}`
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/users", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d; body=%s", recorder.Code, http.StatusRequestEntityTooLarge, recorder.Body.String())
	}
}

func TestRecoveredPanicIsLoggedAndMeasured(t *testing.T) {
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	metrics := observability.NewMetrics()
	server := NewServer(panicUsers{}, ready{}, logger)
	handler := NewHandler(server, config.Telemetry{MetricsEnabled: true}, logger, metrics)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/users", nil)
	request.Header.Set("X-Request-ID", "panic-request")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	logOutput := logs.String()
	if !strings.Contains(logOutput, `"request_id":"panic-request"`) || !strings.Contains(logOutput, `"status":500`) {
		t.Fatalf("logs do not contain correlated recovered request: %s", logOutput)
	}

	adminHandler := NewAdminHandler(config.Telemetry{MetricsEnabled: true}, logger, metrics)
	metricsRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	metricsRecorder := httptest.NewRecorder()
	adminHandler.ServeHTTP(metricsRecorder, metricsRequest)
	if !strings.Contains(metricsRecorder.Body.String(), `service_http_requests_total{method="GET",route="GET /v1/users",status="500"} 1`) {
		t.Fatalf("metrics do not contain recovered 500: %s", metricsRecorder.Body.String())
	}
}
