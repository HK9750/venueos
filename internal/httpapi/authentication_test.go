package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/HK9750/venueos/internal/access"
	"github.com/HK9750/venueos/internal/platform/identifier"
)

type fakeBearerAuthenticator struct {
	authorization access.Authorization
	err           error
}

func (fake fakeBearerAuthenticator) Authenticate(context.Context, string) (access.Authorization, error) {
	return fake.authorization, fake.err
}

func TestAuthenticateRejectsMissingMalformedAndDuplicateCredentials(t *testing.T) {
	handler := Authenticate(fakeBearerAuthenticator{})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler was called without one bearer credential")
	}))
	for _, test := range []struct {
		name   string
		values []string
	}{
		{name: "missing"},
		{name: "wrong scheme", values: []string{"Basic secret"}},
		{name: "duplicate", values: []string{"Bearer one", "Bearer two"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/organizations/01890f3e-7b4c-7cc6-9c52-6d6f83394ef5", nil)
			for _, value := range test.values {
				request.Header.Add("Authorization", value)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") == "" || !strings.Contains(response.Body.String(), `"code":"unauthenticated"`) {
				t.Fatalf("status/header/body = %d/%q/%s", response.Code, response.Header().Get("WWW-Authenticate"), response.Body.String())
			}
		})
	}
}

func TestAuthenticateAddsVerifiedTenantAuthorization(t *testing.T) {
	organizationID := mustAuthID(t, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef5")
	principal, err := access.NewPrincipal(access.PrincipalAPIKey, "01890f3e-7b4c-7cc6-9c52-6d6f83394ef6")
	if err != nil {
		t.Fatalf("NewPrincipal() error = %v", err)
	}
	authorization, err := access.NewAuthorizationForRole(principal, organizationID, access.RoleAdmin)
	if err != nil {
		t.Fatalf("NewAuthorizationForRole() error = %v", err)
	}
	handler := Authenticate(fakeBearerAuthenticator{authorization: authorization})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		found, foundErr := access.AuthorizationFromContext(r.Context())
		if foundErr != nil || found.OrganizationID() != organizationID || found.Principal() != principal {
			t.Fatalf("authorization = %#v, error = %v", found, foundErr)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/organizations/"+organizationID.String(), nil)
	request.Header.Set("Authorization", "Bearer opaque-test-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestAuthenticateMapsCredentialAndDependencyFailuresSafely(t *testing.T) {
	for _, test := range []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "invalid credential", err: access.ErrAPIKeyInvalid, wantStatus: http.StatusUnauthorized, wantCode: "unauthenticated"},
		{name: "dependency", err: errors.New("database secret must not escape"), wantStatus: http.StatusServiceUnavailable, wantCode: "dependency_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := Authenticate(fakeBearerAuthenticator{err: test.err})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("protected handler was called after authentication failure")
			}))
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/organizations/01890f3e-7b4c-7cc6-9c52-6d6f83394ef5", nil)
			request.Header.Set("Authorization", "Bearer test-token")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), `"code":"`+test.wantCode+`"`) || strings.Contains(response.Body.String(), "database secret") {
				t.Fatalf("status/body = %d/%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestAuthenticateLeavesTransitionalAndHealthRoutesUnchanged(t *testing.T) {
	handler := Authenticate(fakeBearerAuthenticator{err: errors.New("should not be called")})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := access.AuthorizationFromContext(r.Context()); err == nil {
			t.Fatal("unprotected route unexpectedly received authorization")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, path := range []string{"/healthz", "/readyz", "/v1/users"} {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusNoContent {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
		})
	}
}

func mustAuthID(t *testing.T, value string) identifier.ID {
	t.Helper()
	id, err := identifier.Parse(value)
	if err != nil {
		t.Fatalf("identifier.Parse(%q) error = %v", value, err)
	}
	return id
}
