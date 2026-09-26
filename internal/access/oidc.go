package access

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var ErrOIDCInvalid = errors.New("oidc identity is invalid")

// OIDCIdentity is the provider-neutral result of verifying a bearer credential.
// Signature, issuer metadata, audience, nonce, and key rotation checks belong to
// the configured adapter; this package only defines the handoff contract.
type OIDCIdentity struct {
	Issuer    string
	Subject   string
	Audiences []string
	Email     string
	Name      string
	IssuedAt  time.Time
	NotBefore time.Time
	ExpiresAt time.Time
}

type OIDCVerifier interface {
	Verify(context.Context, string) (OIDCIdentity, error)
}

func ValidateOIDCIdentity(identity OIDCIdentity, now time.Time) error {
	identity.Issuer = strings.TrimSpace(identity.Issuer)
	identity.Subject = strings.TrimSpace(identity.Subject)
	if identity.Issuer == "" || len(identity.Issuer) > 255 {
		return fmt.Errorf("%w: issuer is required", ErrOIDCInvalid)
	}
	if identity.Subject == "" || len(identity.Subject) > 255 {
		return fmt.Errorf("%w: subject is required", ErrOIDCInvalid)
	}
	if len(identity.Audiences) == 0 {
		return fmt.Errorf("%w: audience is required", ErrOIDCInvalid)
	}
	for _, audience := range identity.Audiences {
		if strings.TrimSpace(audience) == "" || len(audience) > 255 {
			return fmt.Errorf("%w: audience is invalid", ErrOIDCInvalid)
		}
	}
	if identity.ExpiresAt.IsZero() || !identity.ExpiresAt.After(now.UTC()) {
		return fmt.Errorf("%w: identity is expired", ErrOIDCInvalid)
	}
	if !identity.NotBefore.IsZero() && identity.NotBefore.After(now.UTC()) {
		return fmt.Errorf("%w: identity is not active", ErrOIDCInvalid)
	}
	return nil
}

// StaticOIDCVerifier is a deterministic test/development adapter. It is
// deliberately token-map based and must not be used as a production verifier.
type StaticOIDCVerifier struct {
	mu      sync.RWMutex
	entries map[string]staticOIDCEntry
}

type staticOIDCEntry struct {
	identity OIDCIdentity
	err      error
}

func NewStaticOIDCVerifier() *StaticOIDCVerifier {
	return &StaticOIDCVerifier{entries: make(map[string]staticOIDCEntry)}
}

func (verifier *StaticOIDCVerifier) Set(token string, identity OIDCIdentity) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("oidc test token is required")
	}
	if err := ValidateOIDCIdentity(identity, time.Now().UTC()); err != nil {
		return err
	}
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	verifier.entries[token] = staticOIDCEntry{identity: identity}
	return nil
}

func (verifier *StaticOIDCVerifier) SetError(token string, err error) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("oidc test token is required")
	}
	if err == nil {
		return errors.New("oidc test error is required")
	}
	verifier.mu.Lock()
	defer verifier.mu.Unlock()
	verifier.entries[token] = staticOIDCEntry{err: err}
	return nil
}

func (verifier *StaticOIDCVerifier) Verify(_ context.Context, token string) (OIDCIdentity, error) {
	verifier.mu.RLock()
	entry, ok := verifier.entries[token]
	verifier.mu.RUnlock()
	if !ok {
		return OIDCIdentity{}, ErrOIDCInvalid
	}
	if entry.err != nil {
		return OIDCIdentity{}, entry.err
	}
	if err := ValidateOIDCIdentity(entry.identity, time.Now().UTC()); err != nil {
		return OIDCIdentity{}, err
	}
	return entry.identity, nil
}
