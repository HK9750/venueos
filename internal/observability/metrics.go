package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	requests    *prometheus.CounterVec
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
	registry.MustRegister(m.requests, m.duration, m.jobRuns, m.jobDuration, m.jobDepth, m.jobOldest, collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
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
		wrapped := &statusWriter{ResponseWriter: w, status: http.StatusOK}
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
	status      int
	wroteHeader bool
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
