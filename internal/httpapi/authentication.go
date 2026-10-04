package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/device"
	"github.com/HK9750/venueos/internal/platform/apperror"
)

// BearerAuthenticator verifies a bearer credential and returns the complete,
// tenant-scoped authorization context. Implementations must never return a
// scope derived from request headers or bodies.
type BearerAuthenticator interface {
	Authenticate(context.Context, string) (access.Authorization, error)
}

// BearerAuthenticatorFunc adapts a credential verifier function to the
// middleware contract. It is useful for OIDC and API-key adapters without
// coupling this transport package to either provider.
type BearerAuthenticatorFunc func(context.Context, string) (access.Authorization, error)

func (function BearerAuthenticatorFunc) Authenticate(ctx context.Context, token string) (access.Authorization, error) {
	return function(ctx, token)
}

// Authenticate protects the authenticated VenueOS routes. Transitional user
// template routes and health endpoints remain outside this middleware until
// their replacement contracts are delivered.
func Authenticate(authenticator BearerAuthenticator) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !requiresBearer(r.URL.Path) {
				next.ServeHTTP(w, r)
				return
			}

			token, ok := bearerToken(r.Header.Values("Authorization"))
			if !ok {
				writeAuthenticationFailure(w, r)
				return
			}
			authorization, err := authenticator.Authenticate(r.Context(), token)
			if err != nil {
				writeAuthenticationError(w, r, err)
				return
			}
			if authorization.Principal().IsZero() || authorization.OrganizationID().IsZero() {
				writeAuthenticationFailure(w, r)
				return
			}
			next.ServeHTTP(w, r.WithContext(access.WithAuthorization(r.Context(), authorization)))
		})
	}
}

func requiresBearer(path string) bool {
	return path == "/v1/organizations" || strings.HasPrefix(path, "/v1/organizations/") || path == "/v1/invitations/accept"
}

func bearerToken(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func writeAuthenticationFailure(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="venueos"`)
	writeAPIError(w, r, http.StatusUnauthorized, apperror.CodeUnauthenticated, "Authentication is required.", nil)
}

func writeAuthenticationError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, access.ErrAPIKeyInvalid) || errors.Is(err, access.ErrAPIKeyInactive) || errors.Is(err, access.ErrAPIKeySecretFormat) || errors.Is(err, device.ErrCredentialInvalid) {
		writeAuthenticationFailure(w, r)
		return
	}
	if code, ok := apperror.CodeOf(err); ok && code == apperror.CodeUnauthenticated {
		writeAuthenticationFailure(w, r)
		return
	}
	writeAPIError(w, r, http.StatusServiceUnavailable, apperror.CodeDependencyUnavailable, "Authentication is temporarily unavailable.", nil)
}
