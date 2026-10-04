// Package entry contains the pure decision boundary used after QR signature
// verification and before the transactional ticket/admission write.
package entry

import (
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/ticket"
)

type TicketStatus string

const (
	TicketPending  TicketStatus = "pending"
	TicketValid    TicketStatus = "valid"
	TicketAdmitted TicketStatus = "admitted"
	TicketVoid     TicketStatus = "void"
	TicketRefunded TicketStatus = "refunded"
	TicketExpired  TicketStatus = "expired"
)

type ResultCode string

const (
	ResultAdmitted           ResultCode = "admitted"
	ResultAlreadyAdmitted    ResultCode = "already_admitted"
	ResultUnknownTicket      ResultCode = "unknown_ticket"
	ResultWrongSession       ResultCode = "wrong_session"
	ResultOutsideEntryWindow ResultCode = "outside_entry_window"
	ResultVoid               ResultCode = "void"
	ResultRefunded           ResultCode = "refunded"
	ResultExpired            ResultCode = "expired"
)

// Snapshot is the current server-side ticket state loaded under organization
// and session scope. It is intentionally separate from signed QR claims.
type Snapshot struct {
	OrganizationID identifier.ID
	TicketID       identifier.ID
	SessionID      identifier.ID
	Version        int64
	Status         TicketStatus
	EntryOpensAt   time.Time
	EntryClosesAt  time.Time
}

type Decision struct {
	Code       ResultCode
	Admittable bool
	TicketID   identifier.ID
}

// Decide compares verified claims with the current ticket snapshot. An
// admitted decision is only eligibility: the caller must lock the current row,
// insert the scan attempt, and atomically record first admission.
func Decide(claims ticket.Claims, snapshot Snapshot, now time.Time) Decision {
	decision := Decision{Code: ResultUnknownTicket, TicketID: claims.TicketID}
	if claims.TicketID.IsZero() || snapshot.TicketID.IsZero() || claims.TicketID != snapshot.TicketID {
		return decision
	}
	if claims.SessionID.IsZero() || snapshot.SessionID.IsZero() || claims.SessionID != snapshot.SessionID {
		decision.Code = ResultWrongSession
		return decision
	}
	if claims.TicketVersion < 1 || snapshot.Version < 1 || claims.TicketVersion != snapshot.Version {
		return decision
	}
	switch snapshot.Status {
	case TicketAdmitted:
		decision.Code = ResultAlreadyAdmitted
		return decision
	case TicketVoid:
		decision.Code = ResultVoid
		return decision
	case TicketRefunded:
		decision.Code = ResultRefunded
		return decision
	case TicketExpired:
		decision.Code = ResultExpired
		return decision
	case TicketPending:
		return decision
	case TicketValid:
		// Continue with the server-side entry window below.
	default:
		return decision
	}
	if now.IsZero() || snapshot.EntryOpensAt.IsZero() || snapshot.EntryClosesAt.IsZero() || !snapshot.EntryClosesAt.After(snapshot.EntryOpensAt) || now.Before(snapshot.EntryOpensAt) || !now.Before(snapshot.EntryClosesAt) {
		decision.Code = ResultOutsideEntryWindow
		return decision
	}
	decision.Code = ResultAdmitted
	decision.Admittable = true
	return decision
}
