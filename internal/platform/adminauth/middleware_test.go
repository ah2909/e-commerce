package adminauth

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ah2909/e-commerce/internal/platform/authn"
)

func TestAuthenticateRequiresExactPlatformToken(t *testing.T) {
	var token [32]byte
	copy(token[:], "dev-platform-admin-token-0000000")
	middleware := New(token)
	wire := base64.RawURLEncoding.EncodeToString(token[:])
	for _, test := range []struct {
		name   string
		header string
		want   int
	}{
		{name: "valid", header: "Bearer " + wire, want: http.StatusNoContent},
		{name: "missing", want: http.StatusUnauthorized},
		{name: "tenant token", header: "Bearer unrelated-opaque-token", want: http.StatusUnauthorized},
		{name: "oversized", header: "Bearer " + strings.Repeat("A", 4096), want: http.StatusUnauthorized},
		{name: "duplicate", header: "Bearer " + wire + ", Bearer " + wire, want: http.StatusUnauthorized},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			handler := middleware.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				principal, ok := authn.FromContext(r.Context())
				if !ok || principal.Kind() != authn.KindPlatformAdmin {
					t.Fatal("platform principal missing")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/onboarding/stores", nil)
			if test.name == "duplicate" {
				request.Header.Add("Authorization", "Bearer "+wire)
				request.Header.Add("Authorization", "Bearer "+wire)
			} else if test.header != "" {
				request.Header.Set("Authorization", test.header)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want || called != (test.want == http.StatusNoContent) {
				t.Fatalf("status = %d, called = %v", response.Code, called)
			}
		})
	}
}
