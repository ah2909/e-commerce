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
	"github.com/ah2909/e-commerce/internal/stores/application"
	"github.com/ah2909/e-commerce/internal/stores/domain"
)

type contextKey struct{}

type Resolver interface {
	Execute(context.Context, domain.StoreID, domain.UserID) (application.TenantContext, error)
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
		if !ok {
			middleware.logDenied(r, "missing_principal")
			problem.Write(w, r, http.StatusUnauthorized, "unauthenticated", "Authentication is required.", nil)
			return
		}
		storeID, storeErr := domain.ParseStoreID(principal.StoreID())
		userID, userErr := domain.ParseUserID(principal.UserID())
		if storeErr != nil || userErr != nil {
			middleware.logDenied(r, "invalid_principal")
			problem.Write(w, r, http.StatusUnauthorized, "unauthenticated", "Authentication is required.", nil)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), middleware.timeout)
		defer cancel()
		resolved, err := middleware.resolver.Execute(ctx, storeID, userID)
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

func FromContext(ctx context.Context) (application.TenantContext, bool) {
	resolved, ok := ctx.Value(contextKey{}).(application.TenantContext)
	return resolved, ok
}

func (middleware Middleware) logDenied(r *http.Request, reason string) {
	middleware.logger.Warn("tenant resolution denied", "request_id", httpserver.RequestID(r.Context()), "reason", reason)
}
