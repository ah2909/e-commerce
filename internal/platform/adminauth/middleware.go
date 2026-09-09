package adminauth

import (
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/ah2909/e-commerce/internal/platform/authn"
	"github.com/ah2909/e-commerce/internal/platform/problem"
)

type Middleware struct {
	token [32]byte
}

const encodedTokenLength = 43

func New(token [32]byte) Middleware {
	return Middleware{token: token}
}

func (middleware Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		candidate, ok := bearer(r)
		if !ok {
			w.Header().Set("Cache-Control", "no-store")
			problem.Write(w, r, http.StatusUnauthorized, "platform_unauthenticated", "Platform authentication is required.", nil)
			return
		}
		if len(candidate) != encodedTokenLength {
			w.Header().Set("Cache-Control", "no-store")
			problem.Write(w, r, http.StatusUnauthorized, "platform_unauthenticated", "Platform authentication is required.", nil)
			return
		}
		decoded, err := base64.RawURLEncoding.DecodeString(candidate)
		if err != nil || len(decoded) != len(middleware.token) || subtle.ConstantTimeCompare(decoded, middleware.token[:]) != 1 {
			w.Header().Set("Cache-Control", "no-store")
			problem.Write(w, r, http.StatusUnauthorized, "platform_unauthenticated", "Platform authentication is required.", nil)
			return
		}
		principal := authn.NewPlatformAdminPrincipal("deployment")
		next.ServeHTTP(w, r.WithContext(authn.WithPrincipal(r.Context(), principal)))
	})
}

func bearer(r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	parts := strings.Split(values[0], " ")
	if len(parts) != 2 || parts[0] != "Bearer" || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}
