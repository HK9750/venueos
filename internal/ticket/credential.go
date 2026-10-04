// Package ticket contains provider-neutral ticket credential primitives.
//
// The credential proves that VenueOS issued a ticket version for a session. It
// is deliberately not a ticket state store: callers must load the current
// tenant-scoped ticket and admission state before accepting a scan.
package ticket

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/HK9750/venueos/internal/platform/identifier"
)

const (
	// FormatVersion is the credential wire-format version.
	FormatVersion = 1
	// AlgorithmEd25519 is the only signing algorithm accepted by this format.
	AlgorithmEd25519 = "Ed25519"
	// MaxKeyIDLength bounds key identifiers before they reach a resolver or log.
	MaxKeyIDLength = 128
	// MaxCredentialLength prevents scanners from passing unbounded QR data into
	// parsers or downstream logs.
	MaxCredentialLength = 4096
	tokenPrefix         = "vos-ticket.v1"
)

var (
	ErrMalformed            = errors.New("malformed ticket credential")
	ErrInvalidClaims        = errors.New("invalid ticket credential claims")
	ErrInvalidSignature     = errors.New("invalid ticket credential signature")
	ErrUnsupportedAlgorithm = errors.New("unsupported ticket credential algorithm")
	ErrUnknownKey           = errors.New("unknown ticket credential key")
	ErrRevokedKey           = errors.New("revoked ticket credential key")
	ErrNotYetValid          = errors.New("ticket credential is not yet valid")
	ErrExpired              = errors.New("ticket credential is expired")
)

// KeyStatus controls whether an issued key may verify credentials. A
// verify-only key is retained during rotation and may not issue new tokens.
type KeyStatus string

const (
	KeyActive     KeyStatus = "active"
	KeyVerifyOnly KeyStatus = "verify_only"
	KeyRevoked    KeyStatus = "revoked"
)

// KeyResolver supplies public verification keys and their current lifecycle
// state. Implementations should scope lookup to the server's key authority;
// the key ID in a QR credential is not tenant authorization.
type KeyResolver interface {
	Resolve(context.Context, string) (ed25519.PublicKey, KeyStatus, error)
}

// Claims are the non-sensitive facts carried by a signed ticket credential.
// Customer identity, price, order details, and mutable ticket state do not
// belong here.
type Claims struct {
	TicketID      identifier.ID
	SessionID     identifier.ID
	TicketVersion int64
	KeyID         string
	NotBefore     time.Time
	ExpiresAt     time.Time
}

type wireClaims struct {
	Version       int    `json:"v"`
	Algorithm     string `json:"alg"`
	TicketID      string `json:"tid"`
	SessionID     string `json:"sid"`
	TicketVersion int64  `json:"tv"`
	KeyID         string `json:"kid"`
	NotBefore     string `json:"nbf"`
	ExpiresAt     string `json:"exp"`
}

// Signer signs credentials with one active private key. Key lifecycle and
// rotation are owned by the caller; this type never chooses a key for it.
type Signer struct {
	keyID      string
	privateKey ed25519.PrivateKey
}

// KeyID returns the public identifier carried in credentials signed by this
// signer. The private key itself is never exposed.
func (signer Signer) KeyID() string { return signer.keyID }

// NewSigner validates and copies a private key so callers cannot mutate the
// signer through the input slice after construction.
func NewSigner(keyID string, privateKey ed25519.PrivateKey) (Signer, error) {
	if !validKeyID(keyID) {
		return Signer{}, fmt.Errorf("%w: invalid key ID", ErrInvalidClaims)
	}
	if len(privateKey) != ed25519.PrivateKeySize {
		return Signer{}, fmt.Errorf("%w: invalid Ed25519 private key", ErrInvalidClaims)
	}
	return Signer{keyID: keyID, privateKey: append(ed25519.PrivateKey(nil), privateKey...)}, nil
}

// Sign creates a compact three-part credential:
// vos-ticket.v1.<base64url-json-claims>.<base64url-ed25519-signature>.
func (signer Signer) Sign(claims Claims) (string, error) {
	if len(signer.privateKey) != ed25519.PrivateKeySize || !validKeyID(signer.keyID) {
		return "", fmt.Errorf("%w: signer is not initialized", ErrInvalidClaims)
	}
	wire, err := normalizeClaims(claims, signer.keyID)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(wire)
	if err != nil {
		return "", fmt.Errorf("%w: encode claims: %w", ErrMalformed, err)
	}
	signature := ed25519.Sign(signer.privateKey, payload)
	token := tokenPrefix + "." + base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature)
	if len(token) > MaxCredentialLength {
		return "", fmt.Errorf("%w: credential exceeds maximum length", ErrInvalidClaims)
	}
	return token, nil
}

// Verifier checks credential format, signature, key state, and validity time.
// It intentionally does not check current ticket status or admission state.
type Verifier struct {
	resolver KeyResolver
}

// NewVerifier constructs a verifier with an explicit key resolver.
func NewVerifier(resolver KeyResolver) (*Verifier, error) {
	if resolver == nil {
		return nil, fmt.Errorf("%w: key resolver is required", ErrInvalidClaims)
	}
	return &Verifier{resolver: resolver}, nil
}

// Verify validates token and returns its signed claims. now must be the
// server-authoritative UTC time; equality with exp is expired and equality with
// nbf is valid.
func (verifier *Verifier) Verify(ctx context.Context, token string, now time.Time) (Claims, error) {
	if verifier == nil || verifier.resolver == nil {
		return Claims{}, fmt.Errorf("%w: verifier is not initialized", ErrInvalidClaims)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if len(token) == 0 || len(token) > MaxCredentialLength || strings.IndexFunc(token, unicode.IsSpace) >= 0 {
		return Claims{}, ErrMalformed
	}
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0]+"."+parts[1] != tokenPrefix || parts[2] == "" || parts[3] == "" {
		return Claims{}, ErrMalformed
	}
	payload, err := decodeSegment(parts[2])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: claims encoding", ErrMalformed)
	}
	signature, err := decodeSegment(parts[3])
	if err != nil || len(signature) != ed25519.SignatureSize {
		// Signature bytes are untrusted authentication material. Whether the
		// segment is non-canonical base64 or decodes to the wrong bytes, expose
		// one stable tampering result and never distinguish parser details.
		return Claims{}, ErrInvalidSignature
	}
	wire, err := decodeWireClaims(payload)
	if err != nil {
		return Claims{}, err
	}
	if wire.Version != FormatVersion {
		return Claims{}, fmt.Errorf("%w: version %d", ErrMalformed, wire.Version)
	}
	if wire.Algorithm != AlgorithmEd25519 {
		return Claims{}, fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, wire.Algorithm)
	}
	claims, err := parseClaims(wire)
	if err != nil {
		return Claims{}, err
	}
	if now.IsZero() {
		return Claims{}, fmt.Errorf("%w: verification time is required", ErrInvalidClaims)
	}
	publicKey, status, err := verifier.resolver.Resolve(ctx, wire.KeyID)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return Claims{}, ctxErr
		}
		return Claims{}, ErrUnknownKey
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return Claims{}, ErrUnknownKey
	}
	switch status {
	case KeyActive, KeyVerifyOnly:
		// Both states may verify. Only active keys should be passed to Signer.
	case KeyRevoked:
		return Claims{}, ErrRevokedKey
	default:
		return Claims{}, ErrUnknownKey
	}
	if !ed25519.Verify(publicKey, payload, signature) {
		return Claims{}, ErrInvalidSignature
	}
	if !now.Before(claims.ExpiresAt) {
		return Claims{}, ErrExpired
	}
	if now.Before(claims.NotBefore) {
		return Claims{}, ErrNotYetValid
	}
	return claims, nil
}

func normalizeClaims(claims Claims, signerKeyID string) (wireClaims, error) {
	if claims.TicketID.IsZero() || claims.SessionID.IsZero() {
		return wireClaims{}, fmt.Errorf("%w: ticket and session IDs are required", ErrInvalidClaims)
	}
	if claims.TicketVersion < 1 {
		return wireClaims{}, fmt.Errorf("%w: ticket version must be positive", ErrInvalidClaims)
	}
	if claims.KeyID != "" && claims.KeyID != signerKeyID {
		return wireClaims{}, fmt.Errorf("%w: claim key ID does not match signer", ErrInvalidClaims)
	}
	if claims.NotBefore.IsZero() || claims.ExpiresAt.IsZero() || !claims.ExpiresAt.After(claims.NotBefore) {
		return wireClaims{}, fmt.Errorf("%w: validity window is invalid", ErrInvalidClaims)
	}
	return wireClaims{
		Version:       FormatVersion,
		Algorithm:     AlgorithmEd25519,
		TicketID:      claims.TicketID.String(),
		SessionID:     claims.SessionID.String(),
		TicketVersion: claims.TicketVersion,
		KeyID:         signerKeyID,
		NotBefore:     claims.NotBefore.UTC().Format(time.RFC3339Nano),
		ExpiresAt:     claims.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}, nil
}

func parseClaims(wire wireClaims) (Claims, error) {
	ticketID, err := identifier.Parse(wire.TicketID)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: ticket ID", ErrInvalidClaims)
	}
	sessionID, err := identifier.Parse(wire.SessionID)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: session ID", ErrInvalidClaims)
	}
	notBefore, err := time.Parse(time.RFC3339Nano, wire.NotBefore)
	if err != nil || notBefore.IsZero() {
		return Claims{}, fmt.Errorf("%w: not-before time", ErrInvalidClaims)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, wire.ExpiresAt)
	if err != nil || expiresAt.IsZero() || !expiresAt.After(notBefore) {
		return Claims{}, fmt.Errorf("%w: expiry time", ErrInvalidClaims)
	}
	if wire.TicketVersion < 1 || !validKeyID(wire.KeyID) {
		return Claims{}, fmt.Errorf("%w: version or key ID", ErrInvalidClaims)
	}
	return Claims{
		TicketID:      ticketID,
		SessionID:     sessionID,
		TicketVersion: wire.TicketVersion,
		KeyID:         wire.KeyID,
		NotBefore:     notBefore.UTC(),
		ExpiresAt:     expiresAt.UTC(),
	}, nil
}

func decodeWireClaims(payload []byte) (wireClaims, error) {
	if len(payload) == 0 {
		return wireClaims{}, ErrMalformed
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var wire wireClaims
	if err := decoder.Decode(&wire); err != nil {
		return wireClaims{}, fmt.Errorf("%w: claims JSON", ErrMalformed)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return wireClaims{}, fmt.Errorf("%w: trailing claims data", ErrMalformed)
	}
	return wire, nil
}

func decodeSegment(value string) ([]byte, error) {
	if strings.Contains(value, "=") {
		return nil, errors.New("padded base64 is not allowed")
	}
	return base64.RawURLEncoding.Strict().DecodeString(value)
}

func validKeyID(value string) bool {
	if len(value) < 1 || len(value) > MaxKeyIDLength {
		return false
	}
	for index := 0; index < len(value); index++ {
		char := value[index]
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '.' || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}
