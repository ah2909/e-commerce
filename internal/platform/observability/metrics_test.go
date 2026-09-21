package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMiddlewareUsesBoundedRouteAndPropagatesTrace(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	registry := NewRegistry(nil)
	router := gin.New()
	router.Use(registry.Middleware())
	router.GET("/orders/:id", func(ctx *gin.Context) {
		if TraceID(ctx.Request.Context()) != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
			t.Fatalf("TraceID() = %q", TraceID(ctx.Request.Context()))
		}
		ctx.Status(http.StatusNoContent)
	})
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/orders/customer-value", nil)
	request.Header.Set("traceparent", "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if !strings.HasPrefix(response.Header().Get("traceparent"), "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-") {
		t.Fatalf("traceparent = %q", response.Header().Get("traceparent"))
	}
	body := registry.render()
	if strings.Contains(body, "customer-value") || !strings.Contains(body, `route="/orders/:id"`) {
		t.Fatalf("metrics = %s", body)
	}
}

func TestMetricsHandlerDoesNotExposeOtherRoutes(t *testing.T) {
	registry := NewRegistry(nil)
	registry.Inc("commerce_test_total", "Test counter.", map[string]string{"result": "ok"})
	response := httptest.NewRecorder()
	registry.Handler().ServeHTTP(response, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `commerce_test_total{result="ok"} 1`) {
		t.Fatalf("metrics response = %d %q", response.Code, response.Body.String())
	}
	notFound := httptest.NewRecorder()
	registry.Handler().ServeHTTP(notFound, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/tenant", nil))
	if notFound.Code != http.StatusNotFound {
		t.Fatalf("unexpected route status = %d", notFound.Code)
	}
}

func TestMiddlewarePropagatesUnsampledTrace(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	registry := NewRegistry(nil)
	router := gin.New()
	router.Use(registry.Middleware())
	router.GET("/ready", func(ctx *gin.Context) { ctx.Status(http.StatusOK) })
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ready", nil)
	request.Header.Set("traceparent", "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-00")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if !strings.HasPrefix(response.Header().Get("traceparent"), "00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-") {
		t.Fatalf("traceparent = %q", response.Header().Get("traceparent"))
	}
}
