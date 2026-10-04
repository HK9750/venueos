package entry

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/ticket"
)

var (
	ErrInvalidScan         = errors.New("invalid online scan")
	ErrDeviceNotFound      = errors.New("entry device not found")
	ErrDeviceInactive      = errors.New("entry device is not active")
	ErrDeviceNotAuthorized = errors.New("entry device is not authorized for this scope")
	ErrScanNotFound        = errors.New("scan attempt not found")
	ErrScanConflict        = errors.New("device scan ID conflicts with an existing scan")
)

// CredentialVerifier is the narrow ticket-verification port used by the entry
// application service. Implementations own key resolution and rotation; the
// entry package only receives verified non-sensitive claims.
type CredentialVerifier interface {
	Verify(context.Context, string, time.Time) (ticket.Claims, error)
}

type ScanInput struct {
	OrganizationID identifier.ID
	GateID         *identifier.ID
	DeviceScanID   string
	Credential     string
	ScannedAt      time.Time
	Metadata       []byte
}

type OnlineScanInput struct {
	OrganizationID   identifier.ID
	DeviceID         identifier.ID
	GateID           *identifier.ID
	DeviceScanID     string
	CredentialSHA256 [32]byte
	Claims           ticket.Claims
	ScannedAt        time.Time
	Metadata         []byte
	ActorType        string
	ActorID          string
}

type ScanResult struct {
	ScanAttemptID identifier.ID
	AdmissionID   *identifier.ID
	Decision      Decision
	Replay        bool
}

type Repository interface {
	RecordOnlineScan(context.Context, OnlineScanInput) (ScanResult, error)
}

// Scan authenticates the caller as an enrolled scanner device, verifies the
// signed ticket credential, and delegates the authoritative admission
// transaction to the repository. Device identity and organization scope always
// come from verified authorization context, never from request-controlled IDs.
func (service *Service) Scan(ctx context.Context, input ScanInput) (ScanResult, error) {
	authorization, err := access.Require(ctx, access.PermissionEntryScan)
	if err != nil {
		if errors.Is(err, access.ErrUnauthenticated) {
			return ScanResult{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return ScanResult{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Online scanning is not permitted.")
	}
	principal := authorization.Principal()
	if principal.Type() != access.PrincipalDevice || input.OrganizationID.IsZero() || authorization.OrganizationID() != input.OrganizationID {
		return ScanResult{}, apperror.Wrap(access.ErrUnauthorized, apperror.CodePermissionDenied, "Online scanning is not permitted.")
	}
	deviceID, err := identifier.Parse(principal.ID())
	if err != nil {
		return ScanResult{}, apperror.Wrap(access.ErrUnauthorized, apperror.CodePermissionDenied, "Online scanning is not permitted.")
	}
	if err := input.validate(); err != nil {
		return ScanResult{}, apperror.Wrap(err, apperror.CodeValidationFailed, "The online scan request is invalid.")
	}
	if service.verifier == nil {
		return ScanResult{}, errors.New("entry credential verifier is not configured")
	}
	now := time.Now().UTC()
	if service.clock != nil {
		now = service.clock.Now().UTC()
	}
	claims, err := service.verifier.Verify(ctx, input.Credential, now)
	if err != nil {
		// Credential parser and key resolver details are intentionally collapsed;
		// raw QR material and key state must not become API error detail.
		return ScanResult{}, apperror.Wrap(err, apperror.CodeInvalidRequest, "The ticket credential is invalid.")
	}
	credentialHash := sha256.Sum256([]byte(input.Credential))
	online := OnlineScanInput{
		OrganizationID:   input.OrganizationID,
		DeviceID:         deviceID,
		GateID:           input.GateID,
		DeviceScanID:     input.DeviceScanID,
		CredentialSHA256: credentialHash,
		Claims:           claims,
		ScannedAt:        input.ScannedAt.UTC(),
		Metadata:         append([]byte(nil), input.Metadata...),
		ActorType:        string(principal.Type()),
		ActorID:          principal.ID(),
	}
	if err := online.Validate(); err != nil {
		return ScanResult{}, apperror.Wrap(err, apperror.CodeValidationFailed, "The online scan request is invalid.")
	}
	result, err := service.repository.RecordOnlineScan(ctx, online)
	switch {
	case errors.Is(err, ErrDeviceNotFound), errors.Is(err, ErrDeviceInactive), errors.Is(err, ErrDeviceNotAuthorized):
		return ScanResult{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Online scanning is not permitted.")
	case errors.Is(err, ErrScanNotFound):
		return ScanResult{}, apperror.Wrap(err, apperror.CodeNotFound, "The requested ticket does not exist.")
	case errors.Is(err, ErrScanConflict):
		return ScanResult{}, apperror.Wrap(err, apperror.CodeConflict, "The scan conflicts with an existing scan attempt.")
	case errors.Is(err, ErrInvalidScan):
		return ScanResult{}, apperror.Wrap(err, apperror.CodeValidationFailed, "The online scan request is invalid.")
	default:
		return result, err
	}
}

func (input ScanInput) validate() error {
	if input.OrganizationID.IsZero() || !validDeviceScanID(input.DeviceScanID) || input.Credential == "" || len(input.Credential) > ticket.MaxCredentialLength || input.ScannedAt.IsZero() {
		return fmt.Errorf("%w: organization, bounded credential, scan ID, and scan time are required", ErrInvalidScan)
	}
	if input.GateID != nil && input.GateID.IsZero() {
		return fmt.Errorf("%w: gate ID is invalid", ErrInvalidScan)
	}
	if input.Metadata != nil {
		var object map[string]json.RawMessage
		if len(input.Metadata) < 2 || len(input.Metadata) > 65536 || json.Unmarshal(input.Metadata, &object) != nil {
			return fmt.Errorf("%w: metadata must be a bounded JSON object", ErrInvalidScan)
		}
	}
	return nil
}

func (input OnlineScanInput) Validate() error {
	if input.OrganizationID.IsZero() || input.DeviceID.IsZero() || input.Claims.TicketID.IsZero() || input.Claims.SessionID.IsZero() || input.Claims.TicketVersion < 1 || input.CredentialSHA256 == [32]byte{} || input.ScannedAt.IsZero() || !validDeviceScanID(input.DeviceScanID) {
		return fmt.Errorf("%w: identifiers, claims, hash, time, and device scan ID are required", ErrInvalidScan)
	}
	if input.GateID != nil && input.GateID.IsZero() {
		return fmt.Errorf("%w: gate ID is invalid", ErrInvalidScan)
	}
	if input.Metadata != nil {
		var object map[string]json.RawMessage
		if len(input.Metadata) < 2 || len(input.Metadata) > 65536 || json.Unmarshal(input.Metadata, &object) != nil {
			return fmt.Errorf("%w: metadata must be a bounded JSON object", ErrInvalidScan)
		}
	}
	return nil
}

func validDeviceScanID(value string) bool {
	if len(value) < 8 || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '.' || char == '_' || char == ':' || char == '-' {
			continue
		}
		return false
	}
	return true
}
