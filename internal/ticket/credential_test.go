package ticket

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/HK9750/venueos/internal/platform/identifier"
)

type testKeyResolver struct {
	keyID string
	key   ed25519.PublicKey
	state KeyStatus
	err   error
}

func (resolver testKeyResolver) Resolve(_ context.Context, keyID string) (ed25519.PublicKey, KeyStatus, error) {
	if resolver.err != nil || keyID != resolver.keyID {
		return nil, "", errors.New("key not found")
	}
	return resolver.key, resolver.state, nil
}

func TestCredentialRoundTripAndVerifyOnlyRotation(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	signer, err := NewSigner("ticket-2026-01", privateKey)
	if err != nil {
		t.Fatalf("NewSigner() error = %v", err)
	}
	notBefore := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	expiresAt := notBefore.Add(24 * time.Hour)
	want := Claims{
		TicketID:      mustID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5"),
		SessionID:     mustID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6"),
		TicketVersion: 2,
		NotBefore:     notBefore,
		ExpiresAt:     expiresAt,
	}
	token, err := signer.Sign(want)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	if !strings.HasPrefix(token, "vos-ticket.v1.") {
		t.Fatalf("token prefix = %q", token)
	}
	verifier, err := NewVerifier(testKeyResolver{keyID: "ticket-2026-01", key: publicKey, state: KeyVerifyOnly})
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	got, err := verifier.Verify(context.Background(), token, notBefore)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if got.TicketID != want.TicketID || got.SessionID != want.SessionID || got.TicketVersion != want.TicketVersion {
		t.Fatalf("claims identity = %#v, want %#v", got, want)
	}
	if got.KeyID != "ticket-2026-01" || !got.NotBefore.Equal(notBefore) || !got.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("claims metadata = %#v", got)
	}
}

func TestCredentialTamperingFailsSignature(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	signer, err := NewSigner("key-a", privateKey)
	if err != nil {
		t.Fatalf("NewSigner() error = %v", err)
	}
	token, err := signer.Sign(testClaims(t, time.Hour))
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	last := token[len(token)-1]
	replacement := byte('A')
	if last == replacement {
		replacement = 'B'
	}
	tampered := token[:len(token)-1] + string(replacement)
	verifier, err := NewVerifier(testKeyResolver{keyID: "key-a", key: publicKey, state: KeyActive})
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	if _, err := verifier.Verify(context.Background(), tampered, time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("Verify() error = %v, want ErrInvalidSignature", err)
	}
}

func TestCredentialValidityAndKeyLifecycle(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	signer, err := NewSigner("key-a", privateKey)
	if err != nil {
		t.Fatalf("NewSigner() error = %v", err)
	}
	notBefore := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	token, err := signer.Sign(Claims{
		TicketID:      mustID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5"),
		SessionID:     mustID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6"),
		TicketVersion: 1,
		NotBefore:     notBefore,
		ExpiresAt:     notBefore.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	tests := []struct {
		name  string
		state KeyStatus
		now   time.Time
		want  error
	}{
		{name: "expired", state: KeyActive, now: notBefore.Add(time.Hour), want: ErrExpired},
		{name: "not yet valid", state: KeyActive, now: notBefore.Add(-time.Nanosecond), want: ErrNotYetValid},
		{name: "revoked", state: KeyRevoked, now: notBefore, want: ErrRevokedKey},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verifier, err := NewVerifier(testKeyResolver{keyID: "key-a", key: publicKey, state: test.state})
			if err != nil {
				t.Fatalf("NewVerifier() error = %v", err)
			}
			if _, err := verifier.Verify(context.Background(), token, test.now); !errors.Is(err, test.want) {
				t.Fatalf("Verify() error = %v, want %v", err, test.want)
			}
		})
	}
	unknownVerifier, err := NewVerifier(testKeyResolver{keyID: "other-key", key: publicKey, state: KeyActive})
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	if _, err := unknownVerifier.Verify(context.Background(), token, notBefore); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("unknown key error = %v, want ErrUnknownKey", err)
	}
}

func TestCredentialRejectsUnsupportedAlgorithmAndUnknownFields(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	valid := wireClaims{
		Version:       FormatVersion,
		Algorithm:     "RSA-SHA256",
		TicketID:      "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5",
		SessionID:     "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6",
		TicketVersion: 1,
		KeyID:         "key-a",
		NotBefore:     "2026-10-03T12:00:00Z",
		ExpiresAt:     "2026-10-03T13:00:00Z",
	}
	token := signWireForTest(t, privateKey, valid)
	verifier, err := NewVerifier(testKeyResolver{keyID: "key-a", key: publicKey, state: KeyActive})
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	if _, err := verifier.Verify(context.Background(), token, time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)); !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("unsupported algorithm error = %v, want ErrUnsupportedAlgorithm", err)
	}
	payload, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var object map[string]any
	if err := json.Unmarshal(append(payload[:len(payload)-1], []byte(`,"unexpected":true}`)...), &object); err != nil {
		t.Fatalf("construct unknown-field payload: %v", err)
	}
	unknownPayload, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("Marshal(unknown) error = %v", err)
	}
	unknownToken := tokenPrefix + "." + base64.RawURLEncoding.EncodeToString(unknownPayload) + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, unknownPayload))
	if _, err := verifier.Verify(context.Background(), unknownToken, time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)); !errors.Is(err, ErrMalformed) {
		t.Fatalf("unknown-field error = %v, want ErrMalformed", err)
	}
}

func TestCredentialDoesNotCarryPIIOrPrice(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	signer, err := NewSigner("key-a", privateKey)
	if err != nil {
		t.Fatalf("NewSigner() error = %v", err)
	}
	token, err := signer.Sign(testClaims(t, time.Hour))
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	parts := strings.Split(token, ".")
	payload, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("DecodeString() error = %v", err)
	}
	for _, forbidden := range []string{"email", "price", "customer", "order"} {
		if strings.Contains(string(payload), forbidden) {
			t.Errorf("payload contains forbidden field %q: %s", forbidden, payload)
		}
	}
}

func TestSignerValidation(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	for _, keyID := range []string{"", "key with spaces", strings.Repeat("x", MaxKeyIDLength+1)} {
		if _, err := NewSigner(keyID, privateKey); !errors.Is(err, ErrInvalidClaims) {
			t.Errorf("NewSigner(%q) error = %v, want ErrInvalidClaims", keyID, err)
		}
	}
	signer, err := NewSigner("key-a", privateKey)
	if err != nil {
		t.Fatalf("NewSigner() error = %v", err)
	}
	invalid := testClaims(t, time.Hour)
	invalid.TicketVersion = 0
	if _, err := signer.Sign(invalid); !errors.Is(err, ErrInvalidClaims) {
		t.Fatalf("Sign() error = %v, want ErrInvalidClaims", err)
	}
	invalid = testClaims(t, time.Hour)
	invalid.KeyID = "other-key"
	if _, err := signer.Sign(invalid); !errors.Is(err, ErrInvalidClaims) {
		t.Fatalf("mismatched key Sign() error = %v, want ErrInvalidClaims", err)
	}
}

func FuzzVerifyNeverPanics(f *testing.F) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		f.Fatalf("GenerateKey() error = %v", err)
	}
	verifier, err := NewVerifier(testKeyResolver{keyID: "key-a", key: publicKey, state: KeyActive})
	if err != nil {
		f.Fatalf("NewVerifier() error = %v", err)
	}
	signer, err := NewSigner("key-a", privateKey)
	if err != nil {
		f.Fatalf("NewSigner() error = %v", err)
	}
	valid, err := signer.Sign(testClaims(f, time.Hour))
	if err != nil {
		f.Fatalf("Sign() error = %v", err)
	}
	f.Add("not-a-token")
	f.Add(valid)
	f.Fuzz(func(t *testing.T, token string) {
		_, _ = verifier.Verify(context.Background(), token, time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	})
}

func testClaims(t testing.TB, lifetime time.Duration) Claims {
	t.Helper()
	notBefore := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	return Claims{
		TicketID:      mustID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5"),
		SessionID:     mustID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6"),
		TicketVersion: 1,
		NotBefore:     notBefore,
		ExpiresAt:     notBefore.Add(lifetime),
	}
}

func mustID(t testing.TB, value string) identifier.ID {
	t.Helper()
	id, err := identifier.Parse(value)
	if err != nil {
		t.Fatalf("Parse(%q) error = %v", value, err)
	}
	return id
}

func signWireForTest(t testing.TB, privateKey ed25519.PrivateKey, wire wireClaims) string {
	t.Helper()
	payload, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return tokenPrefix + "." + base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, payload))
}
