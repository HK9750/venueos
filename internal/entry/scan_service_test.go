package entry

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/ticket"
)

type scanServiceRepository struct {
	input  OnlineScanInput
	result ScanResult
	err    error
	calls  int
}

func (repository *scanServiceRepository) RecordOnlineScan(_ context.Context, input OnlineScanInput) (ScanResult, error) {
	repository.calls++
	repository.input = input
	return repository.result, repository.err
}

type scanServiceVerifier struct {
	claims ticket.Claims
	err    error
	calls  int
	now    time.Time
}

func (verifier *scanServiceVerifier) Verify(_ context.Context, _ string, now time.Time) (ticket.Claims, error) {
	verifier.calls++
	verifier.now = now
	return verifier.claims, verifier.err
}

func scanServiceContext(t *testing.T, organizationID, deviceID identifier.ID, principalType access.PrincipalType) context.Context {
	t.Helper()
	principal, err := access.NewPrincipal(principalType, deviceID.String())
	if err != nil {
		t.Fatal(err)
	}
	authorization, err := access.NewAuthorization(principal, organizationID, access.PermissionEntryScan)
	if err != nil {
		t.Fatal(err)
	}
	return access.WithAuthorization(context.Background(), authorization)
}

func TestScanAuthenticatesDeviceAndPassesVerifiedClaimsToRepository(t *testing.T) {
	organizationID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	deviceID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	ticketID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7")
	sessionID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef8")
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	verifier := &scanServiceVerifier{claims: ticket.Claims{TicketID: ticketID, SessionID: sessionID, TicketVersion: 3}}
	repository := &scanServiceRepository{result: ScanResult{ScanAttemptID: mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef9"), Decision: Decision{Code: ResultAdmitted, Admittable: true, TicketID: ticketID}}}
	service := NewService(repository).WithCredentialVerifier(verifier).WithClock(clock.NewFixed(now))
	input := ScanInput{OrganizationID: organizationID, DeviceScanID: "device-scan-0001", Credential: "vos-ticket.v1.opaque", ScannedAt: now.Add(-time.Second), Metadata: []byte(`{"source":"scanner"}`)}
	result, err := service.Scan(scanServiceContext(t, organizationID, deviceID, access.PrincipalDevice), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision.Code != ResultAdmitted || repository.calls != 1 || verifier.calls != 1 || !verifier.now.Equal(now) {
		t.Fatalf("result/calls/time = %#v/%d/%d/%v", result, repository.calls, verifier.calls, verifier.now)
	}
	expectedHash := sha256.Sum256([]byte(input.Credential))
	if repository.input.OrganizationID != organizationID || repository.input.DeviceID != deviceID || repository.input.Claims != verifier.claims || repository.input.CredentialSHA256 != expectedHash || repository.input.ActorType != string(access.PrincipalDevice) || repository.input.ActorID != deviceID.String() {
		t.Fatalf("repository input = %#v", repository.input)
	}
}

func TestScanRejectsNonDeviceAndCrossTenantContextBeforeVerification(t *testing.T) {
	organizationID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	otherOrganizationID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	deviceID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7")
	sessionID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef8")
	verifier := &scanServiceVerifier{claims: ticket.Claims{TicketID: deviceID, SessionID: sessionID, TicketVersion: 1}}
	repository := &scanServiceRepository{}
	service := NewService(repository).WithCredentialVerifier(verifier)
	input := ScanInput{OrganizationID: organizationID, DeviceScanID: "device-scan-0001", Credential: "credential", ScannedAt: time.Now().UTC()}
	for name, ctx := range map[string]context.Context{
		"user principal": scanServiceContext(t, organizationID, deviceID, access.PrincipalUser),
		"cross tenant":   scanServiceContext(t, otherOrganizationID, deviceID, access.PrincipalDevice),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := service.Scan(ctx, input)
			if err == nil {
				t.Fatal("Scan() unexpectedly succeeded")
			}
			if code, ok := apperror.CodeOf(err); !ok || code != apperror.CodePermissionDenied {
				t.Fatalf("error code = %v/%t, want permission_denied", code, ok)
			}
		})
	}
	if verifier.calls != 0 || repository.calls != 0 {
		t.Fatalf("verification/repository calls = %d/%d, want 0/0", verifier.calls, repository.calls)
	}
}

func TestScanCollapsesCredentialFailuresAndRepositorySecurityErrors(t *testing.T) {
	organizationID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	deviceID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	sessionID := mustEntryID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef7")
	baseInput := ScanInput{OrganizationID: organizationID, DeviceScanID: "device-scan-0001", Credential: "credential", ScannedAt: time.Now().UTC()}
	ctx := scanServiceContext(t, organizationID, deviceID, access.PrincipalDevice)

	verifier := &scanServiceVerifier{err: ticket.ErrInvalidSignature}
	service := NewService(&scanServiceRepository{}).WithCredentialVerifier(verifier)
	_, err := service.Scan(ctx, baseInput)
	if code, ok := apperror.CodeOf(err); !ok || code != apperror.CodeInvalidRequest || !errors.Is(err, ticket.ErrInvalidSignature) {
		t.Fatalf("credential error = %v, want invalid_request wrapping signature error", err)
	}

	for _, repositoryErr := range []error{ErrDeviceNotFound, ErrDeviceInactive, ErrDeviceNotAuthorized} {
		repository := &scanServiceRepository{err: repositoryErr}
		service = NewService(repository).WithCredentialVerifier(&scanServiceVerifier{claims: ticket.Claims{TicketID: deviceID, SessionID: sessionID, TicketVersion: 1}})
		_, err = service.Scan(ctx, baseInput)
		if code, ok := apperror.CodeOf(err); !ok || code != apperror.CodePermissionDenied {
			t.Fatalf("repository error %v mapped to %v/%t", repositoryErr, code, ok)
		}
	}
}
