package observability

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	requests    *prometheus.CounterVec
	apiErrors   *prometheus.CounterVec
	duration    *prometheus.HistogramVec
	jobRuns     *prometheus.CounterVec
	jobDuration *prometheus.HistogramVec
	jobDepth    *prometheus.GaugeVec
	jobOldest   *prometheus.GaugeVec
	registry    *prometheus.Registry
}

func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()
	m := &Metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "service",
			Name:      "http_requests_total",
			Help:      "Total HTTP requests processed.",
		}, []string{"method", "route", "status"}),
		apiErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "service",
			Name:      "http_errors_total",
			Help:      "Total HTTP responses carrying a stable VenueOS error code.",
		}, []string{"method", "route", "code"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "service",
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request duration in seconds.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"method", "route"}),
		jobRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "service",
			Name:      "worker_jobs_total",
			Help:      "Total durable jobs completed by result and failure class.",
		}, []string{"job_type", "result", "failure_class"}),
		jobDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "service",
			Name:      "worker_job_duration_seconds",
			Help:      "Durable job handler duration in seconds.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"job_type", "result"}),
		jobDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "service", Name: "worker_queue_depth",
			Help: "Number of due durable jobs by registered job type.",
		}, []string{"job_type"}),
		jobOldest: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Namespace: "service", Name: "worker_queue_oldest_age_seconds",
			Help: "Age in seconds of the oldest due durable job by registered job type.",
		}, []string{"job_type"}),
		registry: registry,
	}
	registry.MustRegister(m.requests, m.apiErrors, m.duration, m.jobRuns, m.jobDuration, m.jobDepth, m.jobOldest, collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

func (m *Metrics) ObserveAPIError(method, route, code string) {
	if method == "" {
		method = "unknown"
	}
	if route == "" {
		route = "unmatched"
	}
	if code == "" {
		code = "unknown"
	}
	m.apiErrors.WithLabelValues(method, route, code).Inc()
}

// RegisterDatabasePool exposes bounded pool saturation and acquisition-wait
// metrics without putting connection or tenant identifiers into labels.
func (m *Metrics) RegisterDatabasePool(pool *pgxpool.Pool) error {
	if m == nil || pool == nil {
		return errors.New("metrics and database pool are required")
	}
	return m.registry.Register(&databasePoolCollector{pool: pool})
}

func (m *Metrics) ObserveJobQueue(jobType string, depth int64, oldestAge time.Duration) {
	m.jobDepth.WithLabelValues(jobType).Set(float64(depth))
	m.jobOldest.WithLabelValues(jobType).Set(oldestAge.Seconds())
}

func (m *Metrics) ObserveJob(jobType, result, failureClass string, duration time.Duration) {
	m.jobRuns.WithLabelValues(jobType, result, failureClass).Inc()
	m.jobDuration.WithLabelValues(jobType, result).Observe(duration.Seconds())
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &statusWriter{ResponseWriter: w, metrics: m, status: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		route := r.Pattern
		if route == "" {
			route = "unmatched"
		}
		m.requests.WithLabelValues(r.Method, route, strconv.Itoa(wrapped.status)).Inc()
		m.duration.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	})
}

type statusWriter struct {
	http.ResponseWriter
	metrics     *Metrics
	status      int
	wroteHeader bool
}

type databasePoolCollector struct {
	pool *pgxpool.Pool
}

var (
	databasePoolConnectionsDesc = prometheus.NewDesc(
		"service_database_pool_connections",
		"Current PostgreSQL pool connections by state.",
		[]string{"state"}, nil,
	)
	databasePoolAcquireWaitDesc = prometheus.NewDesc(
		"service_database_pool_acquire_wait_total",
		"Total PostgreSQL pool acquisitions that had to wait.",
		nil, nil,
	)
	databasePoolAcquireWaitSecondsDesc = prometheus.NewDesc(
		"service_database_pool_acquire_wait_seconds_total",
		"Total time spent waiting for PostgreSQL pool acquisitions.",
		nil, nil,
	)
	databasePoolNewConnectionsDesc = prometheus.NewDesc(
		"service_database_pool_new_connections_total",
		"Total PostgreSQL connections opened by the pool.",
		nil, nil,
	)
	databasePoolDestroyedConnectionsDesc = prometheus.NewDesc(
		"service_database_pool_destroyed_connections_total",
		"Total PostgreSQL connections destroyed by pool lifecycle policy.",
		[]string{"reason"}, nil,
	)
)

func (collector *databasePoolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- databasePoolConnectionsDesc
	ch <- databasePoolAcquireWaitDesc
	ch <- databasePoolAcquireWaitSecondsDesc
	ch <- databasePoolNewConnectionsDesc
	ch <- databasePoolDestroyedConnectionsDesc
}

func (collector *databasePoolCollector) Collect(ch chan<- prometheus.Metric) {
	stat := collector.pool.Stat()
	ch <- prometheus.MustNewConstMetric(databasePoolConnectionsDesc, prometheus.GaugeValue, float64(stat.TotalConns()), "total")
	ch <- prometheus.MustNewConstMetric(databasePoolConnectionsDesc, prometheus.GaugeValue, float64(stat.AcquiredConns()), "acquired")
	ch <- prometheus.MustNewConstMetric(databasePoolConnectionsDesc, prometheus.GaugeValue, float64(stat.IdleConns()), "idle")
	ch <- prometheus.MustNewConstMetric(databasePoolConnectionsDesc, prometheus.GaugeValue, float64(stat.MaxConns()), "max")
	ch <- prometheus.MustNewConstMetric(databasePoolAcquireWaitDesc, prometheus.CounterValue, float64(stat.EmptyAcquireCount()))
	ch <- prometheus.MustNewConstMetric(databasePoolAcquireWaitSecondsDesc, prometheus.CounterValue, stat.EmptyAcquireWaitTime().Seconds())
	ch <- prometheus.MustNewConstMetric(databasePoolNewConnectionsDesc, prometheus.CounterValue, float64(stat.NewConnsCount()))
	ch <- prometheus.MustNewConstMetric(databasePoolDestroyedConnectionsDesc, prometheus.CounterValue, float64(stat.MaxLifetimeDestroyCount()), "lifetime")
	ch <- prometheus.MustNewConstMetric(databasePoolDestroyedConnectionsDesc, prometheus.CounterValue, float64(stat.MaxIdleDestroyCount()), "idle")
}

func (w *statusWriter) RecordAPIError(method, route, code string) {
	if w.metrics != nil {
		w.metrics.ObserveAPIError(method, route, code)
	}
}

func (w *statusWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.status = status
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(bytes []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(bytes)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
