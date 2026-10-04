package entry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

type summaryRepository struct {
	summary Summary
}

func (repository *summaryRepository) RecordOnlineScan(context.Context, OnlineScanInput) (ScanResult, error) {
	return ScanResult{}, errors.New("not used")
}

func (repository *summaryRepository) ListEntrySummary(context.Context, SummaryInput) (Summary, error) {
	return repository.summary, nil
}

func TestSummaryRequiresEntryReadAndTenantScope(t *testing.T) {
	organizationID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	otherOrganizationID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	sessionID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7")
	repository := &summaryRepository{}
	service := NewService(repository)
	input := SummaryInput{OrganizationID: organizationID, SessionID: sessionID, From: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC), Until: time.Date(2026, 10, 3, 13, 0, 0, 0, time.UTC)}
	for _, test := range []struct {
		name string
		role access.Role
		org  identifier.ID
		want bool
	}{
		{name: "entry reader", role: access.RoleAdmin, org: organizationID, want: true},
		{name: "scanner cannot read dashboard", role: access.RoleScanner, org: organizationID, want: false},
		{name: "cross tenant", role: access.RoleAdmin, org: otherOrganizationID, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			principal, err := access.NewPrincipal(access.PrincipalUser, "summary-user")
			if err != nil {
				t.Fatal(err)
			}
			authorization, err := access.NewAuthorizationForRole(principal, test.org, test.role)
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.Summary(access.WithAuthorization(context.Background(), authorization), input)
			if (err == nil) != test.want {
				t.Fatalf("Summary() error = %v, want success = %t", err, test.want)
			}
		})
	}
}

func TestSummaryRejectsUnboundedWindow(t *testing.T) {
	organizationID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	sessionID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	principal, err := access.NewPrincipal(access.PrincipalUser, "summary-user")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, access.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err = NewService(&summaryRepository{}).Summary(access.WithAuthorization(context.Background(), authorization), SummaryInput{OrganizationID: organizationID, SessionID: sessionID, From: from, Until: from.Add(MaxSummaryWindow + time.Second)})
	if err == nil {
		t.Fatal("Summary() accepted an unbounded window")
	}
}
