// Package audit exposes the tenant-scoped audit explorer contract.
package audit

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/HK9750/venueos/internal/platform/validation"
)

const (
	DefaultLimit = int32(50)
	MaxLimit     = int32(100)
)

var (
	ErrInvalidCursor = errors.New("audit cursor is invalid")
	ErrInvalidFilter = errors.New("audit filter is invalid")
)

type Entry struct {
	ID                 identifier.ID
	OrganizationID     identifier.ID
	ActorType          string
	ActorID            string
	EffectiveActorType string
	EffectiveActorID   string
	Action             string
	SubjectType        string
	SubjectID          string
	Result             string
	Reason             string
	RequestID          string
	TraceID            string
	SourceIP           string
	DeviceID           *identifier.ID
	BeforeData         json.RawMessage
	AfterData          json.RawMessage
	OccurredAt         time.Time
}

type Cursor struct {
	OrganizationID identifier.ID
	OccurredAt     time.Time
	ID             identifier.ID
	FilterHash     [32]byte
}

type ListInput struct {
	OrganizationID identifier.ID
	ActorType      string
	ActorID        string
	Action         string
	SubjectType    string
	SubjectID      string
	Result         string
	RequestID      string
	OccurredFrom   *time.Time
	OccurredUntil  *time.Time
	Limit          int32
	After          *Cursor
}

type Page struct {
	Items      []Entry
	NextCursor *Cursor
}

type Repository interface {
	ListAuditEntries(context.Context, ListInput) (Page, error)
}

// Service authorizes and validates audit explorer reads before delegating to a
// tenant-predicate repository.
type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service { return &Service{repository: repository} }

func (service *Service) List(ctx context.Context, input ListInput) (Page, error) {
	authorization, err := access.Require(ctx, access.PermissionAuditRead)
	if err != nil {
		if errors.Is(err, access.ErrUnauthenticated) {
			return Page{}, apperror.Wrap(err, apperror.CodeUnauthenticated, "Authentication is required.")
		}
		return Page{}, apperror.Wrap(err, apperror.CodePermissionDenied, "Audit access is not permitted.")
	}
	if input.OrganizationID.IsZero() || authorization.OrganizationID() != input.OrganizationID {
		return Page{}, apperror.New(apperror.CodeNotFound, "The requested audit entries do not exist.")
	}
	normalized, err := normalize(input)
	if err != nil {
		return Page{}, err
	}
	if normalized.After != nil && normalized.After.FilterHash != filterHash(normalized) {
		return Page{}, apperror.Wrap(ErrInvalidCursor, apperror.CodeInvalidCursor, "The audit cursor is invalid.")
	}
	page, err := service.repository.ListAuditEntries(ctx, normalized)
	if err != nil {
		return Page{}, fmt.Errorf("list audit entries: %w", err)
	}
	return page, nil
}

func normalize(input ListInput) (ListInput, error) {
	input.ActorType = strings.TrimSpace(input.ActorType)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.Action = strings.TrimSpace(input.Action)
	input.SubjectType = strings.TrimSpace(input.SubjectType)
	input.SubjectID = strings.TrimSpace(input.SubjectID)
	input.Result = strings.TrimSpace(input.Result)
	input.RequestID = strings.TrimSpace(input.RequestID)
	if input.Limit == 0 {
		input.Limit = DefaultLimit
	}
	collector := validation.Collector{}
	if input.Limit < 1 || input.Limit > MaxLimit {
		collector.Add("limit", "out_of_range", fmt.Sprintf("Use a limit between 1 and %d.", MaxLimit))
	}
	for field, value := range map[string]string{
		"actor_type": input.ActorType, "actor_id": input.ActorID, "action": input.Action,
		"subject_type": input.SubjectType, "subject_id": input.SubjectID, "result": input.Result,
		"request_id": input.RequestID,
	} {
		if len(value) > 200 || strings.IndexFunc(value, func(r rune) bool { return r == '\r' || r == '\n' || r == '\x00' }) >= 0 {
			collector.Add(field, "invalid", "The audit filter is too long or contains a control character.")
		}
	}
	if input.ActorType != "" && !validToken(input.ActorType) {
		collector.Add("actor_type", "invalid", "The actor type is invalid.")
	}
	if input.Action != "" && !validAction(input.Action) {
		collector.Add("action", "invalid", "The audit action is invalid.")
	}
	if input.SubjectType != "" && !validToken(input.SubjectType) {
		collector.Add("subject_type", "invalid", "The subject type is invalid.")
	}
	if input.Result != "" && input.Result != "success" && input.Result != "denied" && input.Result != "failure" {
		collector.Add("result", "invalid", "The audit result is invalid.")
	}
	if input.OccurredFrom != nil {
		value := input.OccurredFrom.UTC()
		input.OccurredFrom = &value
	}
	if input.OccurredUntil != nil {
		value := input.OccurredUntil.UTC()
		input.OccurredUntil = &value
	}
	if input.OccurredFrom != nil && input.OccurredUntil != nil && !input.OccurredUntil.After(*input.OccurredFrom) {
		collector.Add("occurred_until", "invalid_range", "The end of the audit time range must be after its start.")
	}
	if input.After != nil && (input.After.OrganizationID.IsZero() || input.After.ID.IsZero() || input.After.OccurredAt.IsZero()) {
		collector.Add("cursor", "invalid", "The audit cursor is invalid.")
	}
	if err := collector.Error("The audit filter is invalid."); err != nil {
		return ListInput{}, err
	}
	return input, nil
}

func validToken(value string) bool {
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || (index > 0 && char == '_') {
			continue
		}
		return false
	}
	return value != ""
}

func validAction(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) < 2 || len(parts) > 8 {
		return false
	}
	for _, part := range parts {
		if !validToken(part) {
			return false
		}
	}
	return true
}

func filterHash(input ListInput) [32]byte {
	value := strings.Join([]string{
		input.OrganizationID.String(), input.ActorType, input.ActorID, input.Action,
		input.SubjectType, input.SubjectID, input.Result, input.RequestID,
		formatTime(input.OccurredFrom), formatTime(input.OccurredUntil),
	}, "\x00")
	return sha256.Sum256([]byte(value))
}

// FilterHash returns the stable binding used by opaque audit cursors.
func FilterHash(input ListInput) [32]byte { return filterHash(input) }

func formatTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func EncodeCursor(cursor Cursor) (string, error) {
	if cursor.OrganizationID.IsZero() || cursor.ID.IsZero() || cursor.OccurredAt.IsZero() || cursor.FilterHash == [32]byte{} {
		return "", ErrInvalidCursor
	}
	payload := cursorPayload{Version: 1, OrganizationID: cursor.OrganizationID.String(), OccurredAt: cursor.OccurredAt.UTC().Format(time.RFC3339Nano), ID: cursor.ID.String(), FilterHash: hex.EncodeToString(cursor.FilterHash[:])}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode audit cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func DecodeCursor(value string) (Cursor, error) {
	if len(value) == 0 || len(value) > 512 {
		return Cursor{}, invalidCursorError()
	}
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return Cursor{}, invalidCursorError()
	}
	var payload cursorPayload
	if json.Unmarshal(body, &payload) != nil || payload.Version != 1 {
		return Cursor{}, invalidCursorError()
	}
	organizationID, err := identifier.Parse(payload.OrganizationID)
	if err != nil {
		return Cursor{}, invalidCursorError()
	}
	id, err := identifier.Parse(payload.ID)
	if err != nil {
		return Cursor{}, invalidCursorError()
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, payload.OccurredAt)
	if err != nil {
		return Cursor{}, invalidCursorError()
	}
	hash, err := hex.DecodeString(payload.FilterHash)
	if err != nil || len(hash) != sha256.Size {
		return Cursor{}, invalidCursorError()
	}
	var filter [32]byte
	copy(filter[:], hash)
	return Cursor{OrganizationID: organizationID, OccurredAt: occurredAt.UTC(), ID: id, FilterHash: filter}, nil
}

func invalidCursorError() error {
	return apperror.Wrap(ErrInvalidCursor, apperror.CodeInvalidCursor, "The audit cursor is invalid.")
}

type cursorPayload struct {
	Version        int    `json:"v"`
	OrganizationID string `json:"organization_id"`
	OccurredAt     string `json:"occurred_at"`
	ID             string `json:"id"`
	FilterHash     string `json:"filter_hash"`
}

// RedactJSON provides a defense-in-depth boundary for the audit explorer. Audit
// writers already store safe diffs, but the response path must not expose a
// secret if a future writer accidentally includes one.
func RedactJSON(value json.RawMessage) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	var decoded any
	if json.Unmarshal(value, &decoded) != nil {
		return json.RawMessage(`{"redacted":true}`)
	}
	redactValue(decoded)
	result, err := json.Marshal(decoded)
	if err != nil {
		return json.RawMessage(`{"redacted":true}`)
	}
	return result
}

func redactValue(value any) {
	switch current := value.(type) {
	case map[string]any:
		for key, child := range current {
			if sensitiveKey(key) {
				current[key] = "[REDACTED]"
				continue
			}
			redactValue(child)
		}
	case []any:
		for _, child := range current {
			redactValue(child)
		}
	}
}

func sensitiveKey(value string) bool {
	value = strings.ToLower(strings.ReplaceAll(value, "-", "_"))
	for _, fragment := range []string{"secret", "token", "password", "authorization", "cookie", "credential", "private_key"} {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}
