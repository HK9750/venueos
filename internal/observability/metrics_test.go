package observability

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestWorkerMetrics(t *testing.T) {
	metrics := NewMetrics()
	metrics.ObserveJob("ticket.issue", "retry", "transient", 250*time.Millisecond)
	metrics.ObserveJobQueue("ticket.issue", 4, 3*time.Second)
	metrics.ObserveAPIError("GET", "GET /v1/organizations/{organization_id}", "not_found")

	request := httptest.NewRequestWithContext(context.Background(), "GET", "/metrics", nil)
	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, request)
	body, err := io.ReadAll(response.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	output := string(body)
	for _, expected := range []string{
		`service_worker_jobs_total{failure_class="transient",job_type="ticket.issue",result="retry"} 1`,
		`service_worker_queue_depth{job_type="ticket.issue"} 4`,
		`service_worker_queue_oldest_age_seconds{job_type="ticket.issue"} 3`,
		`service_http_errors_total{code="not_found",method="GET",route="GET /v1/organizations/{organization_id}"} 1`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("metrics output does not contain %q", expected)
		}
	}
}

func TestDatabasePoolMetrics(t *testing.T) {
	poolConfig, err := pgxpool.ParseConfig("postgres://user:pass@127.0.0.1:1/venueos")
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.MinConns = 0
	poolConfig.MaxConns = 3
	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	metrics := NewMetrics()
	if err := metrics.RegisterDatabasePool(pool); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), "GET", "/metrics", nil))
	output := response.Body.String()
	for _, expected := range []string{
		`service_database_pool_connections{state="max"} 3`,
		`service_database_pool_connections{state="total"} 0`,
		`service_database_pool_acquire_wait_total 0`,
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("metrics output does not contain %q", expected)
		}
	}
}
