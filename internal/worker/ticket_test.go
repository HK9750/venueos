package worker

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/jobqueue"
	"github.com/HK9750/venueos/internal/ticket"
)

type ticketIssuanceRepositoryFake struct {
	issued ticket.Ticket
	called int
}

func (repository *ticketIssuanceRepositoryFake) Issue(_ context.Context, record ticket.IssueRecord) (ticket.Ticket, bool, error) {
	repository.called++
	if repository.issued.ID.IsZero() {
		repository.issued = record.Ticket
	}
	return repository.issued, repository.called > 1, nil
}

func TestTicketIssuanceHandlerRequiresTenantBoundJob(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	_ = publicKey
	signer, err := ticket.NewSigner("ticket-2026-01", privateKey)
	if err != nil {
		t.Fatalf("NewSigner() error = %v", err)
	}
	repository := &ticketIssuanceRepositoryFake{}
	service := ticket.NewIssuanceService(repository, signer, fixedTicketClock{now: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)})
	organizationID := mustWorkerID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	payload, err := ticket.EncodeIssueJobPayload(ticket.IssueInput{OrganizationID: organizationID, EntitlementID: mustWorkerID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6"), OrderLineID: mustWorkerID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7"), SessionID: mustWorkerID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef8"), NotBefore: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC), ExpiresAt: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC), EntryOpensAt: time.Date(2026, 10, 3, 11, 0, 0, 0, time.UTC), EntryClosesAt: time.Date(2026, 10, 3, 15, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatalf("EncodeIssueJobPayload() error = %v", err)
	}
	handler := NewTicketIssuanceHandler(service)
	job := jobqueue.Job{OrganizationID: &organizationID, Type: ticket.IssueJobType, SchemaVersion: ticket.IssueJobSchemaVersion, Payload: payload}
	if err := handler(context.Background(), job); err != nil {
		t.Fatalf("handler() error = %v", err)
	}
	if repository.called != 1 || repository.issued.Status != ticket.StatusValid {
		t.Fatalf("repository calls/ticket = %d/%#v", repository.called, repository.issued)
	}
	otherOrganization := mustWorkerID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef9")
	job.OrganizationID = &otherOrganization
	if err := handler(context.Background(), job); err == nil {
		t.Fatal("handler accepted a cross-tenant job")
	}
}

type fixedTicketClock struct{ now time.Time }

func (clock fixedTicketClock) Now() time.Time { return clock.now }

func mustWorkerID(t *testing.T, value string) identifier.ID {
	t.Helper()
	id, err := identifier.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", value, err)
	}
	return id
}
