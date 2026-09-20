// Package config loads and validates immutable process configuration.
package config

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

type Config struct {
	App       App
	HTTP      HTTP
	Database  Database
	Log       Log
	Telemetry Telemetry
	Worker    Worker
}

// APIConfig contains only configuration used by the HTTP API process.
type APIConfig struct {
	App       App
	HTTP      HTTP
	Database  Database
	Log       Log
	Telemetry Telemetry
}

// WorkerConfig contains only configuration used by the worker process.
type WorkerConfig struct {
	App       App
	Database  Database
	Log       Log
	Telemetry Tracing
	Worker    Worker
}

// MigrationConfig contains only configuration used by the migration process.
type MigrationConfig struct {
	App      App
	Database MigrationDatabase
	Log      Log
}

type App struct {
	Environment string `env:"APP_ENV" envDefault:"development"`
	ServiceName string `env:"APP_SERVICE_NAME" envDefault:"venueos"`
}

type HTTP struct {
	Address           string        `env:"HTTP_ADDRESS" envDefault:":8080"`
	ReadHeaderTimeout time.Duration `env:"HTTP_READ_HEADER_TIMEOUT" envDefault:"5s"`
	ReadTimeout       time.Duration `env:"HTTP_READ_TIMEOUT" envDefault:"15s"`
	WriteTimeout      time.Duration `env:"HTTP_WRITE_TIMEOUT" envDefault:"30s"`
	IdleTimeout       time.Duration `env:"HTTP_IDLE_TIMEOUT" envDefault:"60s"`
	ShutdownTimeout   time.Duration `env:"HTTP_SHUTDOWN_TIMEOUT" envDefault:"15s"`
}

type Database struct {
	URL             string        `env:"DATABASE_URL,required,notEmpty"`
	ConnectTimeout  time.Duration `env:"DATABASE_CONNECT_TIMEOUT" envDefault:"10s"`
	MaxConns        int32         `env:"DATABASE_MAX_CONNS" envDefault:"20"`
	MinConns        int32         `env:"DATABASE_MIN_CONNS" envDefault:"2"`
	MaxConnLifetime time.Duration `env:"DATABASE_MAX_CONN_LIFETIME" envDefault:"30m"`
	MaxConnIdleTime time.Duration `env:"DATABASE_MAX_CONN_IDLE_TIME" envDefault:"5m"`
}

type MigrationDatabase struct {
	URL            string        `env:"DATABASE_URL,required,notEmpty"`
	ConnectTimeout time.Duration `env:"DATABASE_CONNECT_TIMEOUT" envDefault:"10s"`
}

type Log struct {
	Level  string `env:"LOG_LEVEL" envDefault:"info"`
	Format string `env:"LOG_FORMAT" envDefault:"json"`
}

type Telemetry struct {
	OTLPEndpoint    string  `env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	TraceSampleRate float64 `env:"OTEL_TRACES_SAMPLE_RATE" envDefault:"1"`
	MetricsEnabled  bool    `env:"METRICS_ENABLED" envDefault:"true"`
	PprofEnabled    bool    `env:"PPROF_ENABLED" envDefault:"false"`
	AdminAddress    string  `env:"ADMIN_ADDRESS" envDefault:"127.0.0.1:9090"`
}

type Tracing struct {
	OTLPEndpoint    string  `env:"OTEL_EXPORTER_OTLP_ENDPOINT"`
	TraceSampleRate float64 `env:"OTEL_TRACES_SAMPLE_RATE" envDefault:"1"`
}

type Worker struct {
	Interval time.Duration `env:"WORKER_INTERVAL" envDefault:"30s"`
}

func Load() (Config, error) {
	return loadAndValidate(func(cfg Config) error { return cfg.Validate() })
}

func LoadAPI() (APIConfig, error) {
	return loadAndValidate(func(cfg APIConfig) error { return cfg.Validate() })
}

func LoadWorker() (WorkerConfig, error) {
	return loadAndValidate(func(cfg WorkerConfig) error { return cfg.Validate() })
}

func LoadMigration() (MigrationConfig, error) {
	return loadAndValidate(func(cfg MigrationConfig) error { return cfg.Validate() })
}

func loadAndValidate[T any](validate func(T) error) (T, error) {
	cfg, err := env.ParseAs[T]()
	if err != nil {
		return *new(T), fmt.Errorf("parse environment: %w", err)
	}
	if err := validate(cfg); err != nil {
		return *new(T), fmt.Errorf("validate configuration: %w", err)
	}
	return cfg, nil
}

func (c Config) Validate() error {
	return errors.Join(
		validateApp(c.App),
		validateHTTP(c.HTTP),
		validateDatabase(c.Database),
		validateLog(c.App, c.Log),
		validateTelemetry(c.App, c.Telemetry, true),
		validateWorker(c.Worker),
	)
}

func (c APIConfig) Validate() error {
	return errors.Join(
		validateApp(c.App),
		validateHTTP(c.HTTP),
		validateDatabase(c.Database),
		validateLog(c.App, c.Log),
		validateTelemetry(c.App, c.Telemetry, true),
	)
}

func (c WorkerConfig) Validate() error {
	return errors.Join(
		validateApp(c.App),
		validateDatabase(c.Database),
		validateLog(c.App, c.Log),
		validateTracing(c.Telemetry),
		validateWorker(c.Worker),
	)
}

func (c MigrationConfig) Validate() error {
	var errs []error
	errs = append(errs, validateApp(c.App), validateLog(c.App, c.Log))
	if c.Database.ConnectTimeout <= 0 {
		errs = append(errs, errors.New("DATABASE_CONNECT_TIMEOUT must be positive"))
	}
	return errors.Join(errs...)
}

func validateApp(cfg App) error {
	var errs []error
	if strings.TrimSpace(cfg.ServiceName) == "" {
		errs = append(errs, errors.New("APP_SERVICE_NAME must not be empty"))
	}
	if !oneOf(cfg.Environment, "development", "test", "staging", "production") {
		errs = append(errs, errors.New("APP_ENV must be development, test, staging, or production"))
	}
	return errors.Join(errs...)
}

func validateHTTP(cfg HTTP) error {
	var errs []error
	if strings.TrimSpace(cfg.Address) == "" {
		errs = append(errs, errors.New("HTTP_ADDRESS must not be empty"))
	}
	if cfg.ReadHeaderTimeout <= 0 || cfg.ReadTimeout <= 0 || cfg.WriteTimeout <= 0 || cfg.IdleTimeout <= 0 || cfg.ShutdownTimeout <= 0 {
		errs = append(errs, errors.New("HTTP timeouts must be positive"))
	}
	return errors.Join(errs...)
}

func validateDatabase(cfg Database) error {
	var errs []error
	if cfg.ConnectTimeout <= 0 {
		errs = append(errs, errors.New("DATABASE_CONNECT_TIMEOUT must be positive"))
	}
	if cfg.MaxConns <= 0 {
		errs = append(errs, errors.New("DATABASE_MAX_CONNS must be positive"))
	}
	if cfg.MinConns < 0 || cfg.MinConns > cfg.MaxConns {
		errs = append(errs, errors.New("DATABASE_MIN_CONNS must be between zero and DATABASE_MAX_CONNS"))
	}
	if cfg.MaxConnLifetime < 0 || cfg.MaxConnIdleTime < 0 {
		errs = append(errs, errors.New("database connection lifetimes must not be negative"))
	}
	return errors.Join(errs...)
}

func validateLog(app App, cfg Log) error {
	var errs []error
	if !oneOf(strings.ToLower(cfg.Level), "debug", "info", "warn", "error") {
		errs = append(errs, errors.New("LOG_LEVEL must be debug, info, warn, or error"))
	}
	if !oneOf(strings.ToLower(cfg.Format), "json", "text") {
		errs = append(errs, errors.New("LOG_FORMAT must be json or text"))
	}
	if app.Environment == "production" && strings.ToLower(cfg.Format) != "json" {
		errs = append(errs, errors.New("LOG_FORMAT must be json in production"))
	}
	return errors.Join(errs...)
}

func validateTelemetry(app App, cfg Telemetry, requireProductionMetrics bool) error {
	var errs []error
	errs = append(errs, validateTracing(Tracing{OTLPEndpoint: cfg.OTLPEndpoint, TraceSampleRate: cfg.TraceSampleRate}))
	if requireProductionMetrics && app.Environment == "production" && !cfg.MetricsEnabled {
		errs = append(errs, errors.New("METRICS_ENABLED must be true in production"))
	}
	if (cfg.MetricsEnabled || cfg.PprofEnabled) && strings.TrimSpace(cfg.AdminAddress) == "" {
		errs = append(errs, errors.New("ADMIN_ADDRESS must not be empty when metrics or pprof is enabled"))
	}
	return errors.Join(errs...)
}

func validateTracing(cfg Tracing) error {
	if cfg.TraceSampleRate < 0 || cfg.TraceSampleRate > 1 {
		return errors.New("OTEL_TRACES_SAMPLE_RATE must be between zero and one")
	}
	return nil
}

func validateWorker(cfg Worker) error {
	if cfg.Interval <= 0 {
		return errors.New("WORKER_INTERVAL must be positive")
	}
	return nil
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
