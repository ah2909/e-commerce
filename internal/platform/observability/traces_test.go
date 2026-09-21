package observability

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTraceExporterSendsBoundedOTLPSpan(t *testing.T) {
	payloads := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/traces" || request.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		body, _ := io.ReadAll(request.Body)
		payloads <- string(body)
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exporter, err := NewTraceExporter(ctx, server.URL, "test-api", nil)
	if err != nil {
		t.Fatal(err)
	}
	requestContext := context.WithValue(context.Background(), traceKey{}, traceContext{traceID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", spanID: "bbbbbbbbbbbbbbbb"})
	if !exporter.Submit(requestContext, "/orders/:id", http.MethodGet, http.StatusNoContent, time.Unix(1, 0), time.Unix(2, 0)) {
		t.Fatal("Submit() dropped span")
	}
	select {
	case payload := <-payloads:
		if strings.Contains(payload, "customer-value") || !strings.Contains(payload, "/orders/:id") || !strings.Contains(payload, "test-api") {
			t.Fatalf("payload = %s", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("trace was not exported")
	}
}
