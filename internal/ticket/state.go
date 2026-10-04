package ticket

import (
	"errors"
	"fmt"
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
)

type Status string

const (
	StatusPending  Status = "pending"
	StatusValid    Status = "valid"
	StatusAdmitted Status = "admitted"
	StatusVoid     Status = "void"
	StatusRefunded Status = "refunded"
	StatusExpired  Status = "expired"
)

var (
	ErrInvalidTicket = errors.New("invalid ticket")
	ErrInvalidState  = errors.New("invalid ticket state")
)

type Ticket struct {
	ID              identifier.ID
	OrganizationID  identifier.ID
	EntitlementID   identifier.ID
	OrderLineID     identifier.ID
	SessionID       identifier.ID
	PublicReference string
	Status          Status
	Version         int64
	SigningKeyID    string
	NotBefore       time.Time
	ExpiresAt       time.Time
	EntryOpensAt    time.Time
	EntryClosesAt   time.Time
	IssuedAt        time.Time
	AdmittedAt      *time.Time
	VoidAt          *time.Time
	RefundedAt      *time.Time
	ExpiredAt       *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (ticket Ticket) Validate() error {
	if ticket.ID.IsZero() || ticket.OrganizationID.IsZero() || ticket.EntitlementID.IsZero() || ticket.OrderLineID.IsZero() || ticket.SessionID.IsZero() || ticket.Version < 1 || ticket.PublicReference == "" || ticket.SigningKeyID == "" || ticket.NotBefore.IsZero() || ticket.ExpiresAt.IsZero() || !ticket.ExpiresAt.After(ticket.NotBefore) || ticket.EntryOpensAt.IsZero() || ticket.EntryClosesAt.IsZero() || !ticket.EntryClosesAt.After(ticket.EntryOpensAt) || ticket.IssuedAt.IsZero() || ticket.CreatedAt.IsZero() || ticket.UpdatedAt.IsZero() || ticket.UpdatedAt.Before(ticket.CreatedAt) {
		return fmt.Errorf("%w: required fields or time windows are invalid", ErrInvalidTicket)
	}
	if !validStatus(ticket.Status) {
		return ErrInvalidState
	}
	return nil
}

func validStatus(value Status) bool {
	switch value {
	case StatusPending, StatusValid, StatusAdmitted, StatusVoid, StatusRefunded, StatusExpired:
		return true
	default:
		return false
	}
}
