package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/ah2909/e-commerce/internal/platform/observability"
	"github.com/gin-gonic/gin"
)

const requestIDHeader = "X-Request-ID"

type contextKey int

const requestIDKey contextKey = iota

var (
	requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
	fallbackID       atomic.Uint64
)

type responseRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(body []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(body)
}

func Middleware(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get(requestIDHeader)
		if !requestIDPattern.MatchString(requestID) {
			requestID = newRequestID()
		}
		w.Header().Set(requestIDHeader, requestID)
		ctx := context.WithValue(r.Context(), requestIDKey, requestID)
		recorder := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
		started := time.Now()

		defer func() {
			if recover() != nil {
				logger.Error("request panic recovered", "request_id", requestID)
				if !recorder.wroteHeader {
					recorder.Header().Set("Content-Type", "application/json")
					recorder.WriteHeader(http.StatusInternalServerError)
					_ = json.NewEncoder(recorder).Encode(map[string]string{"status": "internal_error"})
				}
			}
			logger.Info("request completed",
				"request_id", requestID,
				"method", r.Method,
				"path", r.URL.Path,
				"status", recorder.status,
				"duration_ms", time.Since(started).Milliseconds(),
			)
		}()

		next.ServeHTTP(recorder, r.WithContext(ctx))
	})
}

func NewEngine(logger *slog.Logger) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	_ = engine.SetTrustedProxies(nil)
	engine.HandleMethodNotAllowed = true
	engine.RedirectTrailingSlash = false
	engine.RedirectFixedPath = false
	engine.Use(ginRequestMiddleware(logger))
	engine.NoRoute(func(ctx *gin.Context) {
		writeRouteProblem(ctx, http.StatusNotFound, "route_not_found", "Route was not found.")
	})
	engine.NoMethod(func(ctx *gin.Context) {
		writeRouteProblem(ctx, http.StatusMethodNotAllowed, "method_not_allowed", "Method is not allowed.")
	})
	return engine
}

func ginRequestMiddleware(logger *slog.Logger) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		requestID := ctx.GetHeader(requestIDHeader)
		if !requestIDPattern.MatchString(requestID) {
			requestID = newRequestID()
		}
		ctx.Header(requestIDHeader, requestID)
		ctx.Request = ctx.Request.WithContext(context.WithValue(ctx.Request.Context(), requestIDKey, requestID))
		started := time.Now()
		defer func() {
			if recover() != nil {
				logger.Error("request panic recovered", "request_id", requestID)
				if !ctx.Writer.Written() {
					ctx.JSON(http.StatusInternalServerError, map[string]string{"status": "internal_error"})
				}
				ctx.Abort()
			}
			path := ctx.FullPath()
			if path == "" {
				path = "<unmatched>"
			}
			logger.Info("request completed", "request_id", requestID, "trace_id", observability.TraceID(ctx.Request.Context()), "method", ctx.Request.Method, "path", path, "status", ctx.Writer.Status(), "duration_ms", time.Since(started).Milliseconds())
		}()
		ctx.Next()
	}
}

func writeRouteProblem(ctx *gin.Context, status int, code, message string) {
	ctx.Header("Content-Type", "application/problem+json")
	ctx.JSON(status, map[string]any{"code": code, "message": message, "details": map[string]any{}, "request_id": RequestID(ctx.Request.Context())})
}

func RequestID(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDKey).(string)
	return requestID
}

func newRequestID() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err == nil {
		return hex.EncodeToString(bytes)
	}
	return "fallback-" + strconv.FormatUint(fallbackID.Add(1), 10)
}
