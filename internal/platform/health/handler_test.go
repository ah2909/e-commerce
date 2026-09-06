package health

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLiveness(t *testing.T) {
	handler := New(nil, time.Second)
	mux := http.NewServeMux()
	handler.Register(mux)

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health/live", nil))

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"ok"`) {
		t.Fatalf("liveness = %d %s", response.Code, response.Body.String())
	}
}

func TestReadiness(t *testing.T) {
	tests := []struct {
		name       string
		check      func(context.Context) error
		wantStatus int
		wantBody   string
	}{
		{name: "ready", check: func(context.Context) error { return nil }, wantStatus: http.StatusOK, wantBody: `"status":"ready"`},
		{name: "unavailable", check: func(context.Context) error { return errors.New("password=secret") }, wantStatus: http.StatusServiceUnavailable, wantBody: `"status":"not_ready"`},
		{name: "timeout", check: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }, wantStatus: http.StatusServiceUnavailable, wantBody: `"status":"not_ready"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := New([]Dependency{{Name: "database", Check: test.check}}, 10*time.Millisecond)
			mux := http.NewServeMux()
			handler.Register(mux)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health/ready", nil))

			if response.Code != test.wantStatus || !strings.Contains(response.Body.String(), test.wantBody) {
				t.Fatalf("readiness = %d %s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "secret") {
				t.Fatalf("readiness exposed dependency error: %s", response.Body.String())
			}
		})
	}
}

func TestHealthRejectsOtherMethods(t *testing.T) {
	handler := New(nil, time.Second)
	mux := http.NewServeMux()
	handler.Register(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/health/live", nil))

	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != http.MethodGet {
		t.Fatalf("method response = %d, Allow=%q", response.Code, response.Header().Get("Allow"))
	}
}
