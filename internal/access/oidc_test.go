package access

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStaticOIDCVerifierValidatesAndReturnsIdentity(t *testing.T) {
	verifier := NewStaticOIDCVerifier()
	now := time.Now().UTC()
	identity := OIDCIdentity{Issuer: "https://issuer.example", Subject: "subject-1", Audiences: []string{"venueos-api"}, ExpiresAt: now.Add(time.Hour)}
	require.NoError(t, verifier.Set("token-1", identity))
	verified, err := verifier.Verify(context.Background(), "token-1")
	require.NoError(t, err)
	require.Equal(t, identity.Subject, verified.Subject)
	_, err = verifier.Verify(context.Background(), "missing")
	require.ErrorIs(t, err, ErrOIDCInvalid)
}

func TestOIDCIdentityValidationRejectsExpiredOrMissingClaims(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	base := OIDCIdentity{Issuer: "https://issuer.example", Subject: "subject-1", Audiences: []string{"venueos-api"}, ExpiresAt: now.Add(time.Hour)}
	require.NoError(t, ValidateOIDCIdentity(base, now))
	base.ExpiresAt = now
	require.ErrorIs(t, ValidateOIDCIdentity(base, now), ErrOIDCInvalid)
	base.ExpiresAt = now.Add(time.Hour)
	base.Subject = ""
	require.ErrorIs(t, ValidateOIDCIdentity(base, now), ErrOIDCInvalid)
	base.Subject = "subject-1"
	base.NotBefore = now.Add(time.Minute)
	require.ErrorIs(t, ValidateOIDCIdentity(base, now), ErrOIDCInvalid)
}

func TestStaticOIDCVerifierPreservesAdapterErrors(t *testing.T) {
	verifier := NewStaticOIDCVerifier()
	want := errors.New("provider unavailable")
	require.NoError(t, verifier.SetError("token-1", want))
	_, err := verifier.Verify(context.Background(), "token-1")
	require.ErrorIs(t, err, want)
}
