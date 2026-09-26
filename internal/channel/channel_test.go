package channel

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateNormalizesKeyAndDefaultsConfiguration(t *testing.T) {
	input, err := validate(CreateInput{Key: "  Box-Office ", DisplayName: " Box Office ", Type: TypeBoxOffice})
	require.NoError(t, err)
	require.Equal(t, "box-office", input.Key)
	require.Equal(t, "Box Office", input.DisplayName)
	require.Empty(t, input.Configuration)
}

func TestValidateRejectsUnsupportedTypeAndOversizedConfiguration(t *testing.T) {
	_, err := validate(CreateInput{Key: "public", DisplayName: "Public", Type: Type("unknown")})
	require.Error(t, err)
	_, err = validate(CreateInput{Key: "public", DisplayName: "Public", Type: TypePublic, Configuration: map[string]any{"value": string(make([]byte, 32769))}})
	require.Error(t, err)
}
