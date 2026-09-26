package inventory

import (
	"testing"

	"github.com/HK9750/venueos/internal/platform/identifier"
	"github.com/stretchr/testify/require"
)

func TestValidateGAHoldNormalizesCurrency(t *testing.T) {
	sessionID, err := identifier.New()
	require.NoError(t, err)
	poolID, err := identifier.New()
	require.NoError(t, err)
	input, err := validate(CreateGAHoldInput{SessionID: sessionID, PoolID: poolID, Currency: " usd ", Quantity: 2, OwnerTokenHash: [32]byte{1}})
	require.NoError(t, err)
	require.Equal(t, "USD", input.Currency)
}

func TestValidateGAHoldRejectsOversizedQuantity(t *testing.T) {
	sessionID, err := identifier.New()
	require.NoError(t, err)
	poolID, err := identifier.New()
	require.NoError(t, err)
	_, err = validate(CreateGAHoldInput{SessionID: sessionID, PoolID: poolID, Currency: "USD", Quantity: MaxHoldQuantity + 1, OwnerTokenHash: [32]byte{1}})
	require.Error(t, err)
}
