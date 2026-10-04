package entry

import (
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/ticket"
)

func TestDecideAdmitsOnlyCurrentValidTicketWithinWindow(t *testing.T) {
	ticketID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	sessionID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	claims := ticket.Claims{TicketID: ticketID, SessionID: sessionID, TicketVersion: 2}
	now := time.Date(2026, 10, 3, 19, 0, 0, 0, time.UTC)
	snapshot := Snapshot{TicketID: ticketID, SessionID: sessionID, Version: 2, Status: TicketValid, EntryOpensAt: now.Add(-time.Hour), EntryClosesAt: now.Add(time.Hour)}
	decision := Decide(claims, snapshot, now)
	if decision.Code != ResultAdmitted || !decision.Admittable || decision.TicketID != ticketID {
		t.Fatalf("decision = %#v", decision)
	}
}

func TestDecideRejectsIdentityVersionWindowAndMutableState(t *testing.T) {
	ticketID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	sessionID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	otherID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7")
	now := time.Date(2026, 10, 3, 19, 0, 0, 0, time.UTC)
	baseClaims := ticket.Claims{TicketID: ticketID, SessionID: sessionID, TicketVersion: 2}
	baseSnapshot := Snapshot{TicketID: ticketID, SessionID: sessionID, Version: 2, Status: TicketValid, EntryOpensAt: now.Add(-time.Hour), EntryClosesAt: now.Add(time.Hour)}
	tests := []struct {
		name     string
		claims   ticket.Claims
		snapshot Snapshot
		now      time.Time
		want     ResultCode
	}{
		{name: "unknown ticket", claims: ticket.Claims{TicketID: otherID, SessionID: sessionID, TicketVersion: 2}, snapshot: baseSnapshot, now: now, want: ResultUnknownTicket},
		{name: "wrong session", claims: ticket.Claims{TicketID: ticketID, SessionID: otherID, TicketVersion: 2}, snapshot: baseSnapshot, now: now, want: ResultWrongSession},
		{name: "stale version", claims: ticket.Claims{TicketID: ticketID, SessionID: sessionID, TicketVersion: 1}, snapshot: baseSnapshot, now: now, want: ResultUnknownTicket},
		{name: "before window", claims: baseClaims, snapshot: baseSnapshot, now: baseSnapshot.EntryOpensAt.Add(-time.Nanosecond), want: ResultOutsideEntryWindow},
		{name: "after window", claims: baseClaims, snapshot: baseSnapshot, now: baseSnapshot.EntryClosesAt, want: ResultOutsideEntryWindow},
		{name: "already admitted", claims: baseClaims, snapshot: Snapshot{TicketID: ticketID, SessionID: sessionID, Version: 2, Status: TicketAdmitted, EntryOpensAt: baseSnapshot.EntryOpensAt, EntryClosesAt: baseSnapshot.EntryClosesAt}, now: now, want: ResultAlreadyAdmitted},
		{name: "void", claims: baseClaims, snapshot: Snapshot{TicketID: ticketID, SessionID: sessionID, Version: 2, Status: TicketVoid}, now: now, want: ResultVoid},
		{name: "refunded", claims: baseClaims, snapshot: Snapshot{TicketID: ticketID, SessionID: sessionID, Version: 2, Status: TicketRefunded}, now: now, want: ResultRefunded},
		{name: "expired", claims: baseClaims, snapshot: Snapshot{TicketID: ticketID, SessionID: sessionID, Version: 2, Status: TicketExpired}, now: now, want: ResultExpired},
		{name: "pending", claims: baseClaims, snapshot: Snapshot{TicketID: ticketID, SessionID: sessionID, Version: 2, Status: TicketPending}, now: now, want: ResultUnknownTicket},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decision := Decide(test.claims, test.snapshot, test.now)
			if decision.Code != test.want || decision.Admittable {
				t.Fatalf("decision = %#v, want code %q and not admittable", decision, test.want)
			}
		})
	}
}

func mustEntryID(t *testing.T, value string) identifier.ID {
	t.Helper()
	id, err := identifier.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", value, err)
	}
	return id
}
