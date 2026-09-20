// Package requestid creates and propagates opaque request identifiers.
package requestid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

const Header = "X-Request-ID"

type contextKey struct{}

var fallbackCounter atomic.Uint64

func New() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("fallback-%x-%x", time.Now().UnixNano(), fallbackCounter.Add(1))
	}
	return hex.EncodeToString(bytes[:])
}

func Valid(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	return strings.IndexFunc(id, func(character rune) bool {
		return (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '-' && character != '_' && character != '.'
	}) == -1
}

func WithContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(contextKey{}).(string)
	return id
}
