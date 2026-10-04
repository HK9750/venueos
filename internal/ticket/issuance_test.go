package ticket

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/platform/clock"
)

type issuanceRepositoryFake struct {
	record IssueRecord
	ticket Ticket
	replay bool
	err    error
	calls  int
}

func (repository *issuanceRepositoryFake) Issue(_ context.Context, record IssueRecord) (Ticket, bool, error) {
	repository.calls++
	repository.record = record
	if repository.err != nil {
		return Ticket{}, false, repository.err
	}
	if repository.replay {
		return repository.ticket, true, nil
	}
	return record.Ticket, false, nil
}

func issuanceInput(t *testing.T, now time.Time) IssueInput {
	t.Helper()
	return IssueInput{
		OrganizationID:  mustID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5"),
		EntitlementID:   mustID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6"),
		OrderLineID:     mustID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7"),
		SessionID:       mustID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef8"),
		PublicReference: "",
		NotBefore:       now.Add(-time.Minute),
		ExpiresAt:       now.Add(24 * time.Hour),
		EntryOpensAt:    now.Add(time.Hour),
		EntryClosesAt:   now.Add(3 * time.Hour),
	}
}

func TestIssuanceCreatesSignedCredentialAndUsesSystemMutationMetadata(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewSigner("key-a", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	repository := &issuanceRepositoryFake{}
	service := NewIssuanceService(repository, signer, clock.NewFixed(now))
	result, err := service.Issue(context.Background(), issuanceInput(t, now))
	if err != nil {
		t.Fatal(err)
	}
	if result.Replay || result.Credential == "" || repository.calls != 1 {
		t.Fatalf("result/calls = %#v/%d", result, repository.calls)
	}
	if repository.record.ActorType != "system" || repository.record.ActorID != "" || repository.record.Ticket.Status != StatusValid || repository.record.Ticket.SigningKeyID != "key-a" {
		t.Fatalf("issuance record = %#v", repository.record)
	}
	verifier, err := NewVerifier(testKeyResolver{keyID: "key-a", key: publicKey, state: KeyActive})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := verifier.Verify(context.Background(), result.Credential, now)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if claims.TicketID != result.Ticket.ID || claims.SessionID != result.Ticket.SessionID || claims.TicketVersion != result.Ticket.Version {
		t.Fatalf("claims = %#v, ticket = %#v", claims, result.Ticket)
	}
}

func TestIssuanceReplaysExistingEntitlementWithoutChangingTicketIdentity(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewSigner("key-a", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	input := issuanceInput(t, now)
	existing := Ticket{ID: mustID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef9"), OrganizationID: input.OrganizationID, EntitlementID: input.EntitlementID, OrderLineID: input.OrderLineID, SessionID: input.SessionID, PublicReference: "VOS-EXISTING1", Status: StatusValid, Version: 1, SigningKeyID: "key-a", NotBefore: input.NotBefore, ExpiresAt: input.ExpiresAt, EntryOpensAt: input.EntryOpensAt, EntryClosesAt: input.EntryClosesAt, IssuedAt: now, CreatedAt: now, UpdatedAt: now}
	repository := &issuanceRepositoryFake{ticket: existing, replay: true}
	result, err := NewIssuanceService(repository, signer, clock.NewFixed(now)).Issue(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Replay || result.Ticket.ID != existing.ID || result.Ticket.PublicReference != existing.PublicReference || repository.record.Ticket.ID == existing.ID {
		t.Fatalf("replay result/record = %#v/%#v", result, repository.record)
	}
}

func TestIssuanceRejectsInvalidWindowAndMapsRepositoryErrors(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := NewSigner("key-a", privateKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	input := issuanceInput(t, now)
	input.ExpiresAt = input.NotBefore
	_, err = NewIssuanceService(&issuanceRepositoryFake{}, signer, clock.NewFixed(now)).Issue(context.Background(), input)
	if err == nil || !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("invalid window error = %v", err)
	}
	repository := &issuanceRepositoryFake{err: ErrEntitlementNotFound}
	input = issuanceInput(t, now)
	_, err = NewIssuanceService(repository, signer, clock.NewFixed(now)).Issue(context.Background(), input)
	if err == nil || !errors.Is(err, ErrEntitlementNotFound) {
		t.Fatalf("not found error = %v", err)
	}
}

var _ IssuanceRepository = (*issuanceRepositoryFake)(nil)
