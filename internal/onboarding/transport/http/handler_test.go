package onboardinghttp

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	onboardingapplication "github.com/ah2909/e-commerce/internal/onboarding/application"
	"github.com/ah2909/e-commerce/internal/platform/adminauth"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/gin-gonic/gin"
)

type recordingLimiter struct{ calls int }

func (limiter *recordingLimiter) Allow(context.Context, string, string, int64, time.Duration) (bool, time.Duration, error) {
	limiter.calls++
	return true, 0, nil
}

func TestPlatformAuthenticationPrecedesProvisioningWork(t *testing.T) {
	var token [32]byte
	copy(token[:], []byte("dev-platform-admin-token-0000000"))
	limiter := &recordingLimiter{}
	handler := NewHandler(onboardingapplication.Service{}, limiter, slog.New(slog.DiscardHandler), time.Second, 10, time.Hour)
	mux := gin.New()
	hosts := tenant.NewHostMiddleware(nil, "example.test", "auth.example.test", slog.New(slog.DiscardHandler), time.Second)
	handler.Register(mux, hosts, adminauth.New(token).AuthenticateGin())

	for _, authorization := range []string{"", "Bearer tenant-user-token"} {
		request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/onboarding/stores", strings.NewReader("not-json"))
		request.Host = "example.test"
		if authorization != "" {
			request.Header.Set("Authorization", authorization)
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized || limiter.calls != 0 {
			t.Fatalf("denied request = status %d, limiter calls %d", response.Code, limiter.calls)
		}
	}

	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/onboarding/stores", strings.NewReader("not-json"))
	request.Host = "example.test"
	request.Header.Set("Authorization", "Bearer "+base64.RawURLEncoding.EncodeToString(token[:]))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || limiter.calls != 1 {
		t.Fatalf("authenticated request = status %d, limiter calls %d", response.Code, limiter.calls)
	}
}
