package invitation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

type fakeRepository struct {
	issued       IssueRecord
	accepted     AcceptRecord
	revoked      RevokeRecord
	issueResult  Invitation
	acceptResult Invitation
	revokeResult Invitation
	err          error
}

func (repository *fakeRepository) Issue(_ context.Context, record IssueRecord) (Invitation, error) {
	repository.issued = record
	return repository.issueResult, repository.err
}

func (repository *fakeRepository) Accept(_ context.Context, record AcceptRecord) (Invitation, error) {
	repository.accepted = record
	return repository.acceptResult, repository.err
}

func (repository *fakeRepository) Revoke(_ context.Context, record RevokeRecord) (Invitation, error) {
	repository.revoked = record
	return repository.revokeResult, repository.err
}

func (*fakeRepository) ExpireDue(context.Context, time.Time, int32) (int, error) { return 0, nil }

func TestIssueNormalizesEmailAndReturnsHighEntropyToken(t *testing.T) {
	organizationID := newID(t)
	repository := &fakeRepository{issueResult: Invitation{ID: newID(t), OrganizationID: organizationID, Role: access.RoleFinance, Status: StatusInvited}}
	service := NewService(repository, clock.NewFixed(time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)))
	result, err := service.Issue(tenantContext(t, organizationID, access.RoleOwner), IssueInput{OrganizationID: organizationID, Email: " Staff@Example.COM ", Role: access.RoleFinance})
	if err != nil {
		t.Fatal(err)
	}
	if repository.issued.Invitation.Email != "staff@example.com" {
		t.Fatalf("normalized email = %q", repository.issued.Invitation.Email)
	}
	if len(result.Token) < 40 || result.Token == string(repository.issued.TokenHash[:]) {
		t.Fatalf("token was not returned as a high-entropy opaque value: %q", result.Token)
	}
	if repository.issued.TokenHash == ([32]byte{}) {
		t.Fatal("token hash was empty")
	}
}

func TestIssueRejectsOwnerInvitation(t *testing.T) {
	organizationID := newID(t)
	service := NewService(&fakeRepository{}, clock.System{})
	_, err := service.Issue(tenantContext(t, organizationID, access.RoleOwner), IssueInput{OrganizationID: organizationID, Email: "owner@example.com", Role: access.RoleOwner})
	var classified *apperror.Error
	if err == nil || !errors.As(err, &classified) || classified.Code() != apperror.CodeValidationFailed {
		t.Fatalf("owner invitation error = %v", err)
	}
}

func TestAcceptRequiresMatchingAuthenticatedUserAndIsSingleUse(t *testing.T) {
	organizationID := newID(t)
	userID := newID(t)
	principal, err := access.NewPrincipal(access.PrincipalUser, userID.String())
	if err != nil {
		t.Fatal(err)
	}
	ctx := access.WithPrincipal(context.Background(), principal)
	repository := &fakeRepository{acceptResult: Invitation{ID: newID(t), OrganizationID: organizationID, Status: StatusAccepted}}
	service := NewService(repository, clock.System{})
	_, err = service.Accept(ctx, AcceptInput{Token: "too-short"})
	if err == nil {
		t.Fatal("short invitation token accepted")
	}
	token, _, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := service.Accept(ctx, AcceptInput{Token: token})
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Status != StatusAccepted || repository.accepted.UserID != userID || repository.accepted.MembershipID.IsZero() {
		t.Fatalf("accepted invitation/record = %#v / %#v", accepted, repository.accepted)
	}
	repository.err = ErrAlreadyUsed
	_, err = service.Accept(ctx, AcceptInput{Token: token})
	if !errors.Is(err, ErrAlreadyUsed) {
		t.Fatalf("replayed invitation error = %v", err)
	}
}

func TestRevokeMapsStaleOrUsedInvitation(t *testing.T) {
	organizationID := newID(t)
	repository := &fakeRepository{err: ErrAlreadyUsed}
	service := NewService(repository, clock.System{})
	_, err := service.Revoke(tenantContext(t, organizationID, access.RoleAdmin), RevokeInput{OrganizationID: organizationID, InvitationID: newID(t), Version: 1})
	var classified *apperror.Error
	if err == nil || !errors.As(err, &classified) || classified.Code() != apperror.CodeConflict {
		t.Fatalf("revoke error = %v", err)
	}
}

func tenantContext(t *testing.T, organizationID identifier.ID, role access.Role) context.Context {
	t.Helper()
	principal, err := access.NewPrincipal(access.PrincipalUser, "invitation-test-user")
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, role)
	if err != nil {
		t.Fatal(err)
	}
	return access.WithAuthorization(context.Background(), authorization)
}

func newID(t *testing.T) identifier.ID {
	t.Helper()
	id, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
