package tenant

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/ah2909/e-commerce/internal/platform/authn"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/problem"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/stores/application"
	"github.com/gin-gonic/gin"
)

type contextKey struct{}

type Resolver interface {
	Execute(context.Context, ids.StoreID, ids.UserID) (application.TenantContext, error)
}

type Middleware struct {
	resolver Resolver
	logger   *slog.Logger
	timeout  time.Duration
}

func NewMiddleware(resolver Resolver, logger *slog.Logger, timeout time.Duration) Middleware {
	return Middleware{resolver: resolver, logger: logger, timeout: timeout}
}

func (middleware Middleware) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := authn.FromContext(r.Context())
		if !ok || principal.Kind() != authn.KindUser {
			middleware.logDenied(r, "missing_principal")
			problem.Write(w, r, http.StatusUnauthorized, "unauthenticated", "Authentication is required.", nil)
			return
		}
		store, hasStore := StoreFromContext(r.Context())
		userID, userErr := ids.ParseUserID(principal.UserID())
		if !hasStore || principal.StoreID() != store.StoreID.String() || userErr != nil {
			middleware.logDenied(r, "invalid_principal")
			problem.Write(w, r, http.StatusUnauthorized, "unauthenticated", "Authentication is required.", nil)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), middleware.timeout)
		defer cancel()
		resolved, err := middleware.resolver.Execute(ctx, store.StoreID, userID)
		if errors.Is(err, application.ErrTenantAccessDenied) {
			middleware.logDenied(r, "membership_denied")
			problem.Write(w, r, http.StatusForbidden, "tenant_access_denied", "Access to this store is denied.", nil)
			return
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			middleware.logger.Warn("tenant resolution unavailable", "request_id", httpserver.RequestID(r.Context()), "reason", "timeout")
			problem.Write(w, r, http.StatusServiceUnavailable, "tenant_resolution_unavailable", "Store access could not be verified.", map[string]any{"retryable": true})
			return
		}
		if err != nil {
			middleware.logger.Error("tenant resolution failed", "request_id", httpserver.RequestID(r.Context()))
			problem.Write(w, r, http.StatusInternalServerError, "internal_error", "An internal error occurred.", nil)
			return
		}
		ctx = context.WithValue(r.Context(), contextKey{}, resolved)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (middleware Middleware) Gin() gin.HandlerFunc {
	return func(request *gin.Context) {
		r := request.Request
		principal, ok := authn.FromContext(r.Context())
		if !ok || principal.Kind() != authn.KindUser {
			middleware.logDenied(r, "missing_principal")
			problem.Write(request.Writer, r, http.StatusUnauthorized, "unauthenticated", "Authentication is required.", nil)
			request.Abort()
			return
		}
		store, hasStore := StoreFromContext(r.Context())
		userID, userErr := ids.ParseUserID(principal.UserID())
		if !hasStore || principal.StoreID() != store.StoreID.String() || userErr != nil {
			middleware.logDenied(r, "invalid_principal")
			problem.Write(request.Writer, r, http.StatusUnauthorized, "unauthenticated", "Authentication is required.", nil)
			request.Abort()
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), middleware.timeout)
		defer cancel()
		resolved, err := middleware.resolver.Execute(ctx, store.StoreID, userID)
		if errors.Is(err, application.ErrTenantAccessDenied) {
			middleware.logDenied(r, "membership_denied")
			problem.Write(request.Writer, r, http.StatusForbidden, "tenant_access_denied", "Access to this store is denied.", nil)
			request.Abort()
			return
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			middleware.logger.Warn("tenant resolution unavailable", "request_id", httpserver.RequestID(r.Context()), "reason", "timeout")
			problem.Write(request.Writer, r, http.StatusServiceUnavailable, "tenant_resolution_unavailable", "Store access could not be verified.", map[string]any{"retryable": true})
			request.Abort()
			return
		}
		if err != nil {
			middleware.logger.Error("tenant resolution failed", "request_id", httpserver.RequestID(r.Context()))
			problem.Write(request.Writer, r, http.StatusInternalServerError, "internal_error", "An internal error occurred.", nil)
			request.Abort()
			return
		}
		request.Request = r.WithContext(context.WithValue(r.Context(), contextKey{}, resolved))
	}
}

func FromContext(ctx context.Context) (application.TenantContext, bool) {
	resolved, ok := ctx.Value(contextKey{}).(application.TenantContext)
	return resolved, ok
}

func (middleware Middleware) logDenied(r *http.Request, reason string) {
	middleware.logger.Warn("tenant resolution denied", "request_id", httpserver.RequestID(r.Context()), "reason", reason)
}
