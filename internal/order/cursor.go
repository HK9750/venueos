package order

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/HK9750/venueos/internal/platform/apperror"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

const (
	DefaultLimit int32 = 50
	MaxLimit     int32 = 100
)

type Cursor struct {
	OrganizationID identifier.ID
	CreatedAt      time.Time
	ID             identifier.ID
}

type Page struct {
	Items      []Order
	NextCursor *Cursor
}

func EncodeCursor(cursor Cursor) (string, error) {
	if cursor.OrganizationID.IsZero() || cursor.ID.IsZero() || cursor.CreatedAt.IsZero() {
		return "", errors.New("cursor fields are required")
	}
	body, err := json.Marshal(struct {
		Version int    `json:"v"`
		OrgID   string `json:"organization_id"`
		Created string `json:"created_at"`
		ID      string `json:"id"`
	}{1, cursor.OrganizationID.String(), cursor.CreatedAt.UTC().Format(time.RFC3339Nano), cursor.ID.String()})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}

func DecodeCursor(value string) (Cursor, error) {
	invalid := func() (Cursor, error) {
		return Cursor{}, apperror.New(apperror.CodeInvalidCursor, "The order cursor is invalid.")
	}
	if len(value) == 0 || len(value) > 512 {
		return invalid()
	}
	var payload struct {
		Version int    `json:"v"`
		OrgID   string `json:"organization_id"`
		Created string `json:"created_at"`
		ID      string `json:"id"`
	}
	body, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(body, &payload) != nil || payload.Version != 1 {
		return invalid()
	}
	orgID, err := identifier.Parse(payload.OrgID)
	if err != nil {
		return invalid()
	}
	id, err := identifier.Parse(payload.ID)
	if err != nil {
		return invalid()
	}
	createdAt, err := time.Parse(time.RFC3339Nano, payload.Created)
	if err != nil {
		return invalid()
	}
	return Cursor{OrganizationID: orgID, CreatedAt: createdAt.UTC(), ID: id}, nil
}
