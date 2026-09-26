package publication

import (
	"context"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/clock"
	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/stretchr/testify/require"
)

type fixedRepository struct{ snapshot EventSnapshot }

func (repository fixedRepository) Load(context.Context, identifier.ID, identifier.ID) (EventSnapshot, error) {
	return repository.snapshot, nil
}

func TestValidateReportsSessionReadinessBlockers(t *testing.T) {
	organizationID := mustPublicationID(t)
	eventID := mustPublicationID(t)
	sessionID := mustPublicationID(t)
	principal, err := access.NewPrincipal(access.PrincipalUser, "owner")
	require.NoError(t, err)
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, access.RoleOwner)
	require.NoError(t, err)
	ctx := access.WithAuthorization(context.Background(), authorization)
	service := NewService(fixedRepository{snapshot: EventSnapshot{Status: "draft", Sessions: []SessionSnapshot{{ID: sessionID, Status: "scheduled", InventoryMode: "assigned_seating", VenueStatus: "inactive", SpaceStatus: "active", PriceTierCount: 0}}}}, fixedClock{})
	report, err := service.Validate(ctx, organizationID, eventID)
	require.NoError(t, err)
	require.False(t, report.Valid)
	require.Len(t, report.Blockers, 3)
}

func TestValidateReportsInventoryCapacityBlocker(t *testing.T) {
	organizationID := mustPublicationID(t)
	eventID := mustPublicationID(t)
	sessionID := mustPublicationID(t)
	principal, err := access.NewPrincipal(access.PrincipalUser, "owner")
	require.NoError(t, err)
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, access.RoleOwner)
	require.NoError(t, err)
	ctx := access.WithAuthorization(context.Background(), authorization)
	service := NewService(fixedRepository{snapshot: EventSnapshot{Status: "draft", Sessions: []SessionSnapshot{{ID: sessionID, Status: "scheduled", InventoryMode: "general_admission", VenueStatus: "active", SpaceStatus: "active", PriceTierCount: 1}}}}, fixedClock{})
	report, err := service.Validate(ctx, organizationID, eventID)
	require.NoError(t, err)
	require.False(t, report.Valid)
	require.Len(t, report.Blockers, 1)
	require.Equal(t, "invalid_inventory_capacity", report.Blockers[0].Code)
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC) }

var _ clock.Clock = fixedClock{}

func mustPublicationID(t *testing.T) identifier.ID {
	t.Helper()
	id, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
