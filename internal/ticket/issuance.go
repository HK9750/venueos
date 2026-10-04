package ticket

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

const (
	MaxPublicReferenceLength = 96
	issuanceActorType        = "system"
)

var (
	ErrEntitlementNotFound     = errors.New("ticket entitlement not found")
	ErrEntitlementNotIssuable  = errors.New("ticket entitlement is not issuable")
	ErrPublicReferenceConflict = errors.New("ticket public reference already exists")
	ErrSigningKeyMismatch      = errors.New("ticket signing key does not match existing ticket")
)

// IssueInput contains the immutable facts needed to create one ticket for one
// confirmed entitlement. Payment confirmation and delivery policy stay outside
// this package; the caller invokes issuance only after those decisions commit.
type IssueInput struct {
	OrganizationID  identifier.ID
	EntitlementID   identifier.ID
	OrderLineID     identifier.ID
	SessionID       identifier.ID
	PublicReference string
	NotBefore       time.Time
	ExpiresAt       time.Time
	EntryOpensAt    time.Time
	EntryClosesAt   time.Time
}

type IssueRecord struct {
	Ticket     Ticket
	AuditID    identifier.ID
	OutboxID   identifier.ID
	ActorType  string
	ActorID    string
	OccurredAt time.Time
}

type IssueResult struct {
	Ticket     Ticket
	Credential string
	Replay     bool
}

type IssuanceRepository interface {
	Issue(context.Context, IssueRecord) (Ticket, bool, error)
}

type IssuanceService struct {
	repository IssuanceRepository
	signer     Signer
	clock      clock.Clock
}

func NewIssuanceService(repository IssuanceRepository, signer Signer, timeSource clock.Clock) *IssuanceService {
	if timeSource == nil {
		timeSource = clock.System{}
	}
	return &IssuanceService{repository: repository, signer: signer, clock: timeSource}
}

func (service *IssuanceService) Issue(ctx context.Context, input IssueInput) (IssueResult, error) {
	if service == nil || service.repository == nil {
		return IssueResult{}, errors.New("ticket issuance repository is not configured")
	}
	now := service.clock.Now().UTC()
	if err := validateIssueInput(input, now); err != nil {
		return IssueResult{}, apperror.Wrap(err, apperror.CodeValidationFailed, "The ticket issuance request is invalid.")
	}
	if !validKeyID(service.signer.KeyID()) {
		return IssueResult{}, errors.New("ticket issuance signer is not configured")
	}
	publicReference := input.PublicReference
	if publicReference == "" {
		var err error
		publicReference, err = newPublicReference()
		if err != nil {
			return IssueResult{}, apperror.Wrap(err, apperror.CodeInternal, "A ticket reference could not be created.")
		}
	}
	ticketID, err := identifier.New()
	if err != nil {
		return IssueResult{}, apperror.Wrap(err, apperror.CodeInternal, "A ticket identifier could not be created.")
	}
	auditID, err := identifier.New()
	if err != nil {
		return IssueResult{}, apperror.Wrap(err, apperror.CodeInternal, "A ticket audit identifier could not be created.")
	}
	outboxID, err := identifier.New()
	if err != nil {
		return IssueResult{}, apperror.Wrap(err, apperror.CodeInternal, "A ticket event identifier could not be created.")
	}
	value := Ticket{
		ID: ticketID, OrganizationID: input.OrganizationID, EntitlementID: input.EntitlementID,
		OrderLineID: input.OrderLineID, SessionID: input.SessionID, PublicReference: publicReference,
		Status: StatusValid, Version: 1, SigningKeyID: service.signer.KeyID(),
		NotBefore: input.NotBefore.UTC(), ExpiresAt: input.ExpiresAt.UTC(),
		EntryOpensAt: input.EntryOpensAt.UTC(), EntryClosesAt: input.EntryClosesAt.UTC(),
		IssuedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	issued, replay, err := service.repository.Issue(ctx, IssueRecord{Ticket: value, AuditID: auditID, OutboxID: outboxID, ActorType: issuanceActorType, OccurredAt: now})
	if errors.Is(err, ErrPublicReferenceConflict) {
		return IssueResult{}, apperror.Wrap(err, apperror.CodeConflict, "The ticket reference is already in use.")
	}
	if errors.Is(err, ErrEntitlementNotFound) {
		return IssueResult{}, apperror.Wrap(err, apperror.CodeNotFound, "The ticket entitlement does not exist.")
	}
	if errors.Is(err, ErrEntitlementNotIssuable) {
		return IssueResult{}, apperror.Wrap(err, apperror.CodeConflict, "The ticket entitlement cannot be issued.")
	}
	if err != nil {
		return IssueResult{}, err
	}
	if replay && issued.SigningKeyID != service.signer.KeyID() {
		return IssueResult{}, apperror.Wrap(ErrSigningKeyMismatch, apperror.CodeConflict, "The ticket credential key is no longer available for replay.")
	}
	credential, err := service.signer.Sign(Claims{TicketID: issued.ID, SessionID: issued.SessionID, TicketVersion: issued.Version, NotBefore: issued.NotBefore, ExpiresAt: issued.ExpiresAt})
	if err != nil {
		return IssueResult{}, apperror.Wrap(err, apperror.CodeInternal, "The ticket credential could not be created.")
	}
	return IssueResult{Ticket: issued, Credential: credential, Replay: replay}, nil
}

func validateIssueInput(input IssueInput, now time.Time) error {
	if input.OrganizationID.IsZero() || input.EntitlementID.IsZero() || input.OrderLineID.IsZero() || input.SessionID.IsZero() || input.NotBefore.IsZero() || input.ExpiresAt.IsZero() || input.EntryOpensAt.IsZero() || input.EntryClosesAt.IsZero() {
		return fmt.Errorf("%w: tenant, entitlement, order line, session, and time windows are required", ErrInvalidTicket)
	}
	if !input.ExpiresAt.After(input.NotBefore) || !input.EntryClosesAt.After(input.EntryOpensAt) || !input.ExpiresAt.After(now) {
		return fmt.Errorf("%w: ticket validity and entry windows are invalid", ErrInvalidTicket)
	}
	if input.PublicReference != "" && !validPublicReference(input.PublicReference) {
		return fmt.Errorf("%w: public reference is invalid", ErrInvalidTicket)
	}
	return nil
}

func validPublicReference(value string) bool {
	if len(value) < 8 || len(value) > MaxPublicReferenceLength || strings.TrimSpace(value) != value {
		return false
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '-' {
			continue
		}
		return false
	}
	return true
}

func newPublicReference() (string, error) {
	bytes := make([]byte, 12)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "VOS-" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(bytes), nil
}
