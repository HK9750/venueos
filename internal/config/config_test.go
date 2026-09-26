package config

import (
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("LOG_FORMAT", "text")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.HTTP.Address != ":8080" {
		t.Fatalf("HTTP address = %q, want :8080", cfg.HTTP.Address)
	}
}

func TestProductionRequiresJSONLogging(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("APP_ENV", "production")
	t.Setenv("LOG_FORMAT", "text")

	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "LOG_FORMAT") {
		t.Fatalf("Load() error = %v, want LOG_FORMAT validation error", err)
	}
}

func TestWorkerIgnoresInvalidHTTPConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("HTTP_READ_TIMEOUT", "not-a-duration")

	if _, err := LoadWorker(); err != nil {
		t.Fatalf("LoadWorker() error = %v", err)
	}
	if _, err := LoadAPI(); err == nil {
		t.Fatal("LoadAPI() succeeded with an invalid HTTP timeout")
	}
}

func TestWorkerValidatesQueueConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("WORKER_BATCH_SIZE", "101")
	t.Setenv("WORKER_LEASE_DURATION", "1m")
	t.Setenv("WORKER_JOB_TIMEOUT", "1m")

	_, err := LoadWorker()
	if err == nil || !strings.Contains(err.Error(), "WORKER_BATCH_SIZE") || !strings.Contains(err.Error(), "WORKER_LEASE_DURATION") {
		t.Fatalf("LoadWorker() error = %v, want batch and lease validation errors", err)
	}
}

func TestMigrationIgnoresPoolConfiguration(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("DATABASE_MAX_CONNS", "not-a-number")

	if _, err := LoadMigration(); err != nil {
		t.Fatalf("LoadMigration() error = %v", err)
	}
}

func TestTraceSampleRateValidation(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("LOG_FORMAT", "text")
	t.Setenv("OTEL_TRACES_SAMPLE_RATE", "1.1")

	_, err := LoadAPI()
	if err == nil || !strings.Contains(err.Error(), "OTEL_TRACES_SAMPLE_RATE") {
		t.Fatalf("LoadAPI() error = %v, want trace sample rate validation error", err)
	}
}
