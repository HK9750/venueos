package observability

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWorkerMetrics(t *testing.T) {
	metrics := NewMetrics()
	metrics.ObserveJob("ticket.issue", "retry", "transient", 250*time.Millisecond)
	metrics.ObserveJobQueue("ticket.issue", 4, 3*time.Second)

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
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("metrics output does not contain %q", expected)
		}
	}
}
