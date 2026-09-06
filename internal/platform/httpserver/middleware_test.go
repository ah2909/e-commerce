package httpserver

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddlewarePropagatesRequestID(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	handler := Middleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := RequestID(r.Context()); got != "request-123" {
			t.Fatalf("RequestID() = %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health/live", nil)
	request.Header.Set(requestIDHeader, "request-123")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Header().Get(requestIDHeader) != "request-123" || !strings.Contains(output.String(), `"request_id":"request-123"`) {
		t.Fatalf("response ID = %q, log = %q", response.Header().Get(requestIDHeader), output.String())
	}
}

func TestMiddlewareReplacesInvalidRequestID(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	handler := Middleware(logger, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	request.Header.Set(requestIDHeader, "invalid request id")
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if got := response.Header().Get(requestIDHeader); got == "" || got == "invalid request id" {
		t.Fatalf("response request ID = %q", got)
	}
}

func TestMiddlewareRecoversPanic(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	handler := Middleware(logger, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("sensitive panic value")
	}))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))

	if response.Code != http.StatusInternalServerError || strings.Contains(output.String(), "sensitive panic value") {
		t.Fatalf("panic response = %d, log = %q", response.Code, output.String())
	}
}
