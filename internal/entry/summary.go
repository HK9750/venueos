package entry

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

const MaxSummaryWindow = 31 * 24 * time.Hour

var (
	ErrInvalidSummary  = errors.New("invalid entry summary request")
	ErrSessionNotFound = errors.New("entry summary session not found")
)

type SummaryInput struct {
	OrganizationID identifier.ID
	SessionID      identifier.ID
	From           time.Time
	Until          time.Time
}

type ResultCount struct {
	Result string
	Count  int64
}

type GateCount struct {
	GateID *identifier.ID
	Count  int64
}

type MinuteCount struct {
	Minute time.Time
	Count  int64
}

type TicketTypeCount struct {
	PriceTierID identifier.ID
	Count       int64
}

type Summary struct {
	OrganizationID identifier.ID
	SessionID      identifier.ID
	GeneratedAt    time.Time
	DataAsOf       *time.Time
	DataDelay      time.Duration
	TotalScans     int64
	ByResult       []ResultCount
	ByGate         []GateCount
	ByMinute       []MinuteCount
	ByTicketType   []TicketTypeCount
}

type SummaryRepository interface {
	ListEntrySummary(context.Context, SummaryInput) (Summary, error)
}

type Service struct {
	repository Repository
	clock      clock.Clock
	verifier   CredentialVerifier
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository, clock: clock.System{}}
}

// WithCredentialVerifier configures the provider-neutral ticket credential
// verifier used by online scans. It must be configured by the API process
// before exposing the scan command; an unconfigured service fails closed.
func (service *Service) WithCredentialVerifier(verifier CredentialVerifier) *Service {
	service.verifier = verifier
	return service
}

// WithClock supplies the server-authoritative clock used for credential
// validity checks. It is primarily useful for deterministic tests.
func (service *Service) WithClock(source clock.Clock) *Service {
	if source != nil {
		service.clock = source
	}
	return service
}

func (service *Service) Summary(ctx context.Context, input SummaryInput) (Summary, error) {
	authorization, err := access.Require(ctx, access.PermissionEntryRead)
	if err != nil {
		if errors.Is(err, access.ErrUnauthenticated) {
			return Summary{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return Summary{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Entry summary access is not permitted.")
	}
	if input.OrganizationID.IsZero() || input.SessionID.IsZero() || authorization.OrganizationID() != input.OrganizationID {
		return Summary{}, apperror.Wrap(ErrInvalidSummary, apperror.CodeNotFound, "The requested entry summary does not exist.")
	}
	normalized, err := normalizeSummaryInput(input)
	if err != nil {
		return Summary{}, apperror.Wrap(err, apperror.CodeValidationFailed, "The entry summary request is invalid.")
	}
	repository, ok := service.repository.(SummaryRepository)
	if !ok {
		return Summary{}, errors.New("entry repository does not support summaries")
	}
	result, err := repository.ListEntrySummary(ctx, normalized)
	if errors.Is(err, ErrSessionNotFound) {
		return Summary{}, apperror.Wrap(err, apperror.CodeNotFound, "The requested session does not exist.")
	}
	if err != nil {
		return Summary{}, err
	}
	return result, nil
}

func normalizeSummaryInput(input SummaryInput) (SummaryInput, error) {
	input.From = input.From.UTC()
	input.Until = input.Until.UTC()
	if input.OrganizationID.IsZero() || input.SessionID.IsZero() || input.From.IsZero() || input.Until.IsZero() {
		return SummaryInput{}, fmt.Errorf("%w: organization, session, from, and until are required", ErrInvalidSummary)
	}
	if !input.Until.After(input.From) {
		return SummaryInput{}, fmt.Errorf("%w: until must be after from", ErrInvalidSummary)
	}
	if input.Until.Sub(input.From) > MaxSummaryWindow {
		return SummaryInput{}, fmt.Errorf("%w: the summary window must not exceed %s", ErrInvalidSummary, MaxSummaryWindow)
	}
	return input, nil
}

// SortSummary makes repository implementations and test fixtures deterministic
// without exposing database ordering as part of the application contract.
func SortSummary(value Summary) Summary {
	sort.Slice(value.ByResult, func(left, right int) bool {
		return value.ByResult[left].Result < value.ByResult[right].Result
	})
	sort.Slice(value.ByGate, func(left, right int) bool {
		if value.ByGate[left].GateID == nil || value.ByGate[right].GateID == nil {
			return value.ByGate[left].GateID == nil && value.ByGate[right].GateID != nil
		}
		return value.ByGate[left].GateID.String() < value.ByGate[right].GateID.String()
	})
	sort.Slice(value.ByMinute, func(left, right int) bool {
		return value.ByMinute[left].Minute.Before(value.ByMinute[right].Minute)
	})
	sort.Slice(value.ByTicketType, func(left, right int) bool {
		return value.ByTicketType[left].PriceTierID.String() < value.ByTicketType[right].PriceTierID.String()
	})
	return value
}
