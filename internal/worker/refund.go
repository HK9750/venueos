package worker

import (
	"context"
	"errors"

	"github.com/HK9750/venueos/internal/payment"
	"github.com/HK9750/venueos/internal/platform/jobqueue"
	"github.com/HK9750/venueos/internal/refund"
)

// NewRefundExecutionHandler adapts refund execution to the durable worker
// runner. Provider failure classes are preserved so transient calls retry and
// permanent calls are finalized/dead-lettered without leaking raw details.
func NewRefundExecutionHandler(service *refund.ExecutionService) Handler {
	return func(ctx context.Context, job jobqueue.Job) error {
		if service == nil {
			return &FailureError{Class: FailurePermanent, SafeMessage: "Refund execution is not configured."}
		}
		input, err := refund.DecodeExecuteJobPayload(job.Payload)
		if err != nil {
			return &FailureError{Class: FailurePermanent, SafeMessage: "Refund execution job payload is invalid.", Cause: err}
		}
		if job.OrganizationID == nil || *job.OrganizationID != input.OrganizationID {
			return &FailureError{Class: FailurePermanent, SafeMessage: "Refund execution job tenant scope is invalid."}
		}
		if _, err := service.Execute(ctx, input); err != nil {
			var failure payment.Failure
			if errors.As(err, &failure) {
				class := FailureUnknown
				switch failure.Class {
				case payment.FailurePermanent:
					class = FailurePermanent
				case payment.FailureTransient:
					class = FailureTransient
				case payment.FailureUnknown:
					class = FailureUnknown
				}
				return &FailureError{Class: class, SafeMessage: failure.SafeMessage, RetryAfter: failure.RetryAfter, Cause: err}
			}
			return err
		}
		return nil
	}
}
