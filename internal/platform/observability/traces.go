package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type span struct {
	traceID string
	spanID  string
	name    string
	method  string
	status  int
	started time.Time
	ended   time.Time
}

type TraceExporter struct {
	endpoint string
	service  string
	client   *http.Client
	spans    chan span
	logger   *slog.Logger
}

func NewTraceExporter(ctx context.Context, endpoint, service string, logger *slog.Logger) (*TraceExporter, error) {
	if endpoint == "" {
		return nil, nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("OTLP endpoint must be an HTTP(S) origin or path without credentials, query, or fragment")
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/") + "/v1/traces"
	exporter := &TraceExporter{endpoint: parsed.String(), service: service, client: &http.Client{Timeout: 3 * time.Second}, spans: make(chan span, 256), logger: logger}
	go exporter.run(ctx)
	return exporter, nil
}

func (exporter *TraceExporter) Submit(ctx context.Context, route, method string, status int, started, ended time.Time) bool {
	value, ok := ctx.Value(traceKey{}).(traceContext)
	if !ok || value.traceID == "" || value.spanID == "" {
		return false
	}
	item := span{traceID: value.traceID, spanID: value.spanID, name: method + " " + route, method: method, status: status, started: started, ended: ended}
	select {
	case exporter.spans <- item:
		return true
	default:
		return false
	}
}

func (exporter *TraceExporter) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case item := <-exporter.spans:
			if err := exporter.export(ctx, item); err != nil && ctx.Err() == nil && exporter.logger != nil {
				exporter.logger.Warn("trace export failed", "error", err)
			}
		}
	}
}

func (exporter *TraceExporter) export(ctx context.Context, item span) error {
	payload := map[string]any{"resourceSpans": []any{map[string]any{
		"resource": map[string]any{"attributes": []any{attribute("service.name", exporter.service)}},
		"scopeSpans": []any{map[string]any{"scope": map[string]any{"name": "commerce-platform"}, "spans": []any{map[string]any{
			"traceId":           item.traceID,
			"spanId":            item.spanID,
			"name":              item.name,
			"kind":              2,
			"startTimeUnixNano": fmt.Sprintf("%d", item.started.UnixNano()),
			"endTimeUnixNano":   fmt.Sprintf("%d", item.ended.UnixNano()),
			"attributes":        []any{attribute("http.request.method", item.method), attribute("http.route", strings.TrimSpace(item.name[len(item.method):])), intAttribute("http.response.status_code", item.status)},
			"status":            map[string]any{"code": map[bool]int{true: 2, false: 1}[item.status >= 500]},
		}}}},
	}}}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, exporter.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := exporter.client.Do(request)
	if err != nil {
		return errors.New("OTLP collector unavailable")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("OTLP collector returned status %d", response.StatusCode)
	}
	return nil
}

func attribute(key, value string) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"stringValue": value}}
}

func intAttribute(key string, value int) map[string]any {
	return map[string]any{"key": key, "value": map[string]any{"intValue": fmt.Sprintf("%d", value)}}
}
