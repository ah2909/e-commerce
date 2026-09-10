package httpserver

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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

func TestGinEnginePreservesRequestContextAndRecovers(t *testing.T) {
	var output bytes.Buffer
	engine := NewEngine(slog.New(slog.NewJSONHandler(&output, nil)))
	engine.GET("/panic", func(ctx *gin.Context) {
		if RequestID(ctx.Request.Context()) != "gin-request" {
			t.Fatal("request ID was not propagated")
		}
		panic("sensitive gin panic")
	})
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/panic", nil)
	request.Header.Set(requestIDHeader, "gin-request")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || strings.Contains(output.String(), "sensitive gin panic") {
		t.Fatalf("panic response = %d, log = %q", response.Code, output.String())
	}
}

func TestGinEngineMapsUnknownRouteAndMethod(t *testing.T) {
	engine := NewEngine(slog.New(slog.DiscardHandler))
	engine.GET("/known", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	for _, test := range []struct {
		method string
		path   string
		status int
		code   string
	}{{http.MethodGet, "/missing", http.StatusNotFound, "route_not_found"}, {http.MethodPost, "/known", http.StatusMethodNotAllowed, "method_not_allowed"}} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), test.method, test.path, nil))
		if response.Code != test.status || !strings.Contains(response.Body.String(), test.code) {
			t.Fatalf("%s %s = %d %s", test.method, test.path, response.Code, response.Body.String())
		}
	}
}

func TestGinEngineDoesNotRedirectSlashMismatches(t *testing.T) {
	engine := NewEngine(slog.New(slog.DiscardHandler))
	engine.GET("/tenant", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	engine.POST("/platform", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	for _, test := range []struct {
		method string
		path   string
		host   string
	}{{http.MethodGet, "/tenant/", "shop.example.test"}, {http.MethodPost, "/platform/", "invalid.example"}} {
		request := httptest.NewRequestWithContext(context.Background(), test.method, test.path, nil)
		request.Host = test.host
		request.Header.Set("X-Forwarded-Prefix", "/attacker")
		request.Header.Set(requestIDHeader, "slash-request")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound || response.Header().Get("Location") != "" || response.Header().Get(requestIDHeader) != "slash-request" || !strings.Contains(response.Body.String(), "route_not_found") {
			t.Fatalf("%s %s = %d, headers=%v, body=%s", test.method, test.path, response.Code, response.Header(), response.Body.String())
		}
	}
}

func TestCORSAllowsConfiguredOriginAndPreflight(t *testing.T) {
	engine := NewEngine(slog.New(slog.DiscardHandler))
	engine.Use(CORS([]string{"http://localhost:5173"}))
	engine.GET("/resource", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })

	request := httptest.NewRequestWithContext(context.Background(), http.MethodOptions, "/resource", nil)
	request.Header.Set("Origin", "http://localhost:5173")
	request.Header.Set("Access-Control-Request-Method", http.MethodGet)
	request.Header.Set("Access-Control-Request-Headers", "Authorization")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != "http://localhost:5173" {
		t.Fatalf("preflight = %d, headers=%v", response.Code, response.Header())
	}
	if response.Header().Get("Access-Control-Allow-Methods") != corsAllowMethods || response.Header().Get("Access-Control-Allow-Headers") != corsAllowHeaders {
		t.Fatalf("preflight policy headers = %v", response.Header())
	}
}

func TestCORSDoesNotAuthorizeUnconfiguredOrigin(t *testing.T) {
	engine := NewEngine(slog.New(slog.DiscardHandler))
	engine.Use(CORS([]string{"http://localhost:5173"}))
	engine.GET("/resource", func(ctx *gin.Context) { ctx.Status(http.StatusNoContent) })
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/resource", nil)
	request.Header.Set("Origin", "https://attacker.example")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("cross-origin response = %d, headers=%v", response.Code, response.Header())
	}
}
