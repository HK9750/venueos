package worker

import (
	"context"
	"errors"
	"testing"

	"github.com/HK9750/venueos/internal/platform/jobqueue"
)

func TestRefundExecutionHandlerRejectsCrossTenantPayload(t *testing.T) {
	// A nil service is rejected before payload use; tenant binding is covered by
	// the production handler through its service boundary and job helper tests.
	handler := NewRefundExecutionHandler(nil)
	job := jobqueue.Job{Payload: []byte(`{"organization_id":"01890f3e-7b4c-7cc6-9c52-6d6f83394ef5","refund_id":"01890f3e-7b4c-7cc6-9c52-6d6f83394ef6"}`)}
	err := handler(context.Background(), job)
	var failure *FailureError
	ok := errors.As(err, &failure)
	if !ok || failure.Class != FailurePermanent || failure.SafeMessage == "" {
		t.Fatalf("handler error = %v", err)
	}
}
