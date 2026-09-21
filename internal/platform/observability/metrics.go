package observability

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

var durationBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

type traceKey struct{}

type traceContext struct {
	traceID string
	spanID  string
}

type series struct {
	name   string
	help   string
	labels string
	value  float64
}

type histogram struct {
	name    string
	help    string
	labels  string
	count   uint64
	sum     float64
	buckets []uint64
}

type Registry struct {
	mu         sync.RWMutex
	counters   map[string]*series
	gauges     map[string]*series
	histograms map[string]*histogram
	pool       *sql.DB
	exporter   *TraceExporter
}

func (registry *Registry) SetTraceExporter(exporter *TraceExporter) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.exporter = exporter
}

func NewRegistry(pool *sql.DB) *Registry {
	return &Registry{counters: make(map[string]*series), gauges: make(map[string]*series), histograms: make(map[string]*histogram), pool: pool}
}

func (registry *Registry) Inc(name, help string, labels map[string]string) {
	key, rendered := metricKey(name, labels)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	item := registry.counters[key]
	if item == nil {
		item = &series{name: name, help: help, labels: rendered}
		registry.counters[key] = item
	}
	item.value++
}

func (registry *Registry) Add(name, help string, labels map[string]string, value float64) {
	key, rendered := metricKey(name, labels)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	item := registry.counters[key]
	if item == nil {
		item = &series{name: name, help: help, labels: rendered}
		registry.counters[key] = item
	}
	item.value += value
}

func (registry *Registry) Set(name, help string, labels map[string]string, value float64) {
	key, rendered := metricKey(name, labels)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.gauges[key] = &series{name: name, help: help, labels: rendered, value: value}
}

func (registry *Registry) AddGauge(name, help string, labels map[string]string, value float64) {
	key, rendered := metricKey(name, labels)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	item := registry.gauges[key]
	if item == nil {
		item = &series{name: name, help: help, labels: rendered}
		registry.gauges[key] = item
	}
	item.value += value
}

func (registry *Registry) Observe(name, help string, labels map[string]string, value float64) {
	key, rendered := metricKey(name, labels)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	item := registry.histograms[key]
	if item == nil {
		item = &histogram{name: name, help: help, labels: rendered, buckets: make([]uint64, len(durationBuckets))}
		registry.histograms[key] = item
	}
	item.count++
	item.sum += value
	for index, bucket := range durationBuckets {
		if value <= bucket {
			item.buckets[index]++
		}
	}
}

func (registry *Registry) Middleware() gin.HandlerFunc {
	return func(request *gin.Context) {
		started := time.Now()
		registry.AddGauge("commerce_http_requests_in_flight", "Current API requests.", nil, 1)
		request.Request = request.Request.WithContext(withTrace(request.Request.Context(), request.GetHeader("traceparent"), request.Writer.Header()))
		request.Next()
		registry.AddGauge("commerce_http_requests_in_flight", "Current API requests.", nil, -1)
		route := request.FullPath()
		if route == "" {
			route = "unmatched"
		}
		labels := map[string]string{"method": boundedMethod(request.Request.Method), "route": route, "status_class": strconv.Itoa(request.Writer.Status()/100) + "xx"}
		registry.Inc("commerce_http_requests_total", "Completed API requests.", labels)
		registry.Observe("commerce_http_request_duration_seconds", "API request duration.", labels, time.Since(started).Seconds())
		registry.mu.RLock()
		exporter := registry.exporter
		registry.mu.RUnlock()
		if exporter != nil && !exporter.Submit(request.Request.Context(), route, boundedMethod(request.Request.Method), request.Writer.Status(), started, time.Now()) {
			registry.Inc("commerce_trace_spans_dropped_total", "Trace spans dropped before export.", nil)
		}
	}
}

func (registry *Registry) Handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/metrics" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_, _ = writer.Write([]byte(registry.render()))
	})
}

func (registry *Registry) render() string {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	var output strings.Builder
	writeSeries := func(kind string, items map[string]*series) {
		keys := sortedKeys(items)
		seen := make(map[string]bool)
		for _, key := range keys {
			item := items[key]
			if !seen[item.name] {
				fmt.Fprintf(&output, "# HELP %s %s\n# TYPE %s %s\n", item.name, item.help, item.name, kind)
				seen[item.name] = true
			}
			fmt.Fprintf(&output, "%s%s %s\n", item.name, item.labels, strconv.FormatFloat(item.value, 'f', -1, 64))
		}
	}
	writeSeries("counter", registry.counters)
	writeSeries("gauge", registry.gauges)
	histogramKeys := sortedKeys(registry.histograms)
	seenHistograms := make(map[string]bool)
	for _, key := range histogramKeys {
		item := registry.histograms[key]
		if !seenHistograms[item.name] {
			fmt.Fprintf(&output, "# HELP %s %s\n# TYPE %s histogram\n", item.name, item.help, item.name)
			seenHistograms[item.name] = true
		}
		for index, bucket := range durationBuckets {
			fmt.Fprintf(&output, "%s_bucket%s %d\n", item.name, addLabel(item.labels, "le", strconv.FormatFloat(bucket, 'f', -1, 64)), item.buckets[index])
		}
		fmt.Fprintf(&output, "%s_bucket%s %d\n", item.name, addLabel(item.labels, "le", "+Inf"), item.count)
		fmt.Fprintf(&output, "%s_sum%s %s\n%s_count%s %d\n", item.name, item.labels, strconv.FormatFloat(item.sum, 'f', -1, 64), item.name, item.labels, item.count)
	}
	if registry.pool != nil {
		stats := registry.pool.Stats()
		fmt.Fprintf(&output, "# HELP commerce_database_connections Database connection pool state.\n# TYPE commerce_database_connections gauge\n")
		fmt.Fprintf(&output, "commerce_database_connections{state=\"open\"} %d\ncommerce_database_connections{state=\"in_use\"} %d\ncommerce_database_connections{state=\"idle\"} %d\n", stats.OpenConnections, stats.InUse, stats.Idle)
		fmt.Fprintf(&output, "# HELP commerce_database_wait_total Database pool waits.\n# TYPE commerce_database_wait_total counter\ncommerce_database_wait_total %d\n", stats.WaitCount)
	}
	return output.String()
}

func Serve(ctx context.Context, address string, handler http.Handler, shutdownTimeout time.Duration) error {
	if address == "" {
		<-ctx.Done()
		return nil
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("listen on metrics address: %w", err)
	}
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8 << 10}
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve metrics: %w", err)
		}
		return nil
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		return fmt.Errorf("shutdown metrics server: %w", err)
	}
	return nil
}

func TraceID(ctx context.Context) string {
	value, _ := ctx.Value(traceKey{}).(traceContext)
	return value.traceID
}

func withTrace(ctx context.Context, incoming string, headers http.Header) context.Context {
	traceID := incomingTraceID(incoming)
	if traceID == "" {
		traceID = randomHex(16)
	}
	spanID := randomHex(8)
	if traceID != "" && spanID != "" {
		headers.Set("traceparent", "00-"+traceID+"-"+spanID+"-01")
	}
	return context.WithValue(ctx, traceKey{}, traceContext{traceID: traceID, spanID: spanID})
}

func incomingTraceID(value string) string {
	parts := strings.Split(value, "-")
	if len(parts) != 4 || parts[0] != "00" || len(parts[1]) != 32 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return ""
	}
	for _, part := range parts[1:3] {
		if _, err := hex.DecodeString(part); err != nil || strings.Trim(part, "0") == "" {
			return ""
		}
	}
	if _, err := hex.DecodeString(parts[3]); err != nil {
		return ""
	}
	return strings.ToLower(parts[1])
}

func randomHex(length int) string {
	value := make([]byte, length)
	if _, err := rand.Read(value); err != nil {
		return ""
	}
	return hex.EncodeToString(value)
}

func metricKey(name string, labels map[string]string) (string, string) {
	if len(labels) == 0 {
		return name, ""
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"=\""+escape(labels[key])+"\"")
	}
	rendered := "{" + strings.Join(parts, ",") + "}"
	return name + rendered, rendered
}

func addLabel(labels, name, value string) string {
	if labels == "" {
		return "{" + name + "=\"" + value + "\"}"
	}
	return strings.TrimSuffix(labels, "}") + "," + name + "=\"" + value + "\"}"
}

func escape(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "\n", "\\n")
	return strings.ReplaceAll(value, "\"", "\\\"")
}

func boundedMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions:
		return method
	default:
		return "OTHER"
	}
}

func sortedKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
