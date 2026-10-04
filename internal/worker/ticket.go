package worker

import (
	"context"

	"github.com/HK9750/venueos/internal/platform/jobqueue"
	"github.com/HK9750/venueos/internal/ticket"
)

// NewTicketIssuanceHandler adapts the provider-neutral issuer to the durable
// worker contract. Payload errors are permanent; repository/provider failures
// retain the runner's normal bounded retry classification.
func NewTicketIssuanceHandler(service *ticket.IssuanceService) Handler {
	return func(ctx context.Context, job jobqueue.Job) error {
		if service == nil {
			return &FailureError{Class: FailurePermanent, SafeMessage: "Ticket issuance is not configured."}
		}
		input, err := ticket.DecodeIssueJobPayload(job.Payload)
		if err != nil {
			return &FailureError{Class: FailurePermanent, SafeMessage: "Ticket issuance job payload is invalid.", Cause: err}
		}
		if job.OrganizationID == nil || *job.OrganizationID != input.OrganizationID {
			return &FailureError{Class: FailurePermanent, SafeMessage: "Ticket issuance job tenant scope is invalid."}
		}
		if _, err := service.Issue(ctx, input); err != nil {
			return err
		}
		return nil
	}
}
