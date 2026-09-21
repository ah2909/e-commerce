package tenant

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/problem"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/stores/application"
	"github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/gin-gonic/gin"
)

type storeContextKey struct{}

type StoreResolver interface {
	Execute(context.Context, string) (application.StoreRecord, error)
}

type StoreIDResolver interface {
	ExecuteID(context.Context, ids.StoreID) (application.StoreRecord, error)
}

type HostMiddleware struct {
	resolver        StoreResolver
	baseDomain      string
	platformHost    string
	authHost        string
	singleStoreHost string
	singleStoreID   ids.StoreID
	logger          *slog.Logger
	timeout         time.Duration
}

func NewHostMiddleware(resolver StoreResolver, baseDomain, authHost string, logger *slog.Logger, timeout time.Duration) HostMiddleware {
	return NewHostMiddlewareWithRouting(resolver, baseDomain, baseDomain, authHost, "", "", logger, timeout)
}

func NewHostMiddlewareWithRouting(resolver StoreResolver, baseDomain, platformHost, authHost, singleStoreHost string, singleStoreID ids.StoreID, logger *slog.Logger, timeout time.Duration) HostMiddleware {
	return HostMiddleware{resolver: resolver, baseDomain: normalizeHost(baseDomain), platformHost: normalizeHost(platformHost), authHost: normalizeHost(authHost), singleStoreHost: normalizeHost(singleStoreHost), singleStoreID: singleStoreID, logger: logger, timeout: timeout}
}

func (middleware HostMiddleware) Tenant(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		store, ok, err := middleware.resolve(r.Context(), r.Host)
		if !ok {
			problem.Write(w, r, http.StatusNotFound, "storefront_unavailable", "Storefront is unavailable.", nil)
			return
		}
		if errors.Is(err, application.ErrStoreUnavailable) {
			problem.Write(w, r, http.StatusNotFound, "storefront_unavailable", "Storefront is unavailable.", nil)
			return
		}
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &networkError) {
			problem.Write(w, r, http.StatusServiceUnavailable, "store_resolution_unavailable", "Storefront could not be resolved.", map[string]any{"retryable": true})
			return
		}
		if err != nil {
			middleware.logger.Error("store resolution failed", "request_id", httpserver.RequestID(r.Context()))
			problem.Write(w, r, http.StatusInternalServerError, "internal_error", "An internal error occurred.", nil)
			return
		}
		ctx := context.WithValue(r.Context(), storeContextKey{}, store)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (middleware HostMiddleware) Base(next http.Handler) http.Handler {
	return middleware.exactHost(middleware.platformHost, next)
}

func (middleware HostMiddleware) Auth(next http.Handler) http.Handler {
	return middleware.exactHost(middleware.authHost, next)
}

func (middleware HostMiddleware) TenantGin() gin.HandlerFunc {
	return func(request *gin.Context) {
		r := request.Request
		store, ok, err := middleware.resolve(r.Context(), r.Host)
		if !ok {
			problem.Write(request.Writer, r, http.StatusNotFound, "storefront_unavailable", "Storefront is unavailable.", nil)
			request.Abort()
			return
		}
		if errors.Is(err, application.ErrStoreUnavailable) {
			problem.Write(request.Writer, r, http.StatusNotFound, "storefront_unavailable", "Storefront is unavailable.", nil)
			request.Abort()
			return
		}
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &networkError) {
			problem.Write(request.Writer, r, http.StatusServiceUnavailable, "store_resolution_unavailable", "Storefront could not be resolved.", map[string]any{"retryable": true})
			request.Abort()
			return
		}
		if err != nil {
			middleware.logger.Error("store resolution failed", "request_id", httpserver.RequestID(r.Context()))
			problem.Write(request.Writer, r, http.StatusInternalServerError, "internal_error", "An internal error occurred.", nil)
			request.Abort()
			return
		}
		request.Request = r.WithContext(context.WithValue(r.Context(), storeContextKey{}, store))
	}
}

func (middleware HostMiddleware) BaseGin() gin.HandlerFunc {
	return middleware.exactHostGin(middleware.platformHost)
}

func (middleware HostMiddleware) resolve(parent context.Context, host string) (application.StoreRecord, bool, error) {
	ctx, cancel := context.WithTimeout(parent, middleware.timeout)
	defer cancel()
	if middleware.singleStoreHost != "" {
		if normalizeHost(host) != middleware.singleStoreHost {
			return application.StoreRecord{}, false, nil
		}
		resolver, ok := middleware.resolver.(StoreIDResolver)
		if !ok {
			return application.StoreRecord{}, true, errors.New("single-store resolver is unavailable")
		}
		store, err := resolver.ExecuteID(ctx, middleware.singleStoreID)
		return store, true, err
	}
	subdomain, ok := tenantSubdomain(host, middleware.baseDomain)
	if !ok {
		return application.StoreRecord{}, false, nil
	}
	store, err := middleware.resolver.Execute(ctx, subdomain)
	return store, true, err
}
func (middleware HostMiddleware) AuthGin() gin.HandlerFunc {
	return middleware.exactHostGin(middleware.authHost)
}

func (middleware HostMiddleware) exactHostGin(expected string) gin.HandlerFunc {
	return func(request *gin.Context) {
		if normalizeHost(request.Request.Host) != expected {
			problem.Write(request.Writer, request.Request, http.StatusNotFound, "route_unavailable", "Route is unavailable.", nil)
			request.Abort()
		}
	}
}

func (middleware HostMiddleware) exactHost(expected string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if normalizeHost(r.Host) != expected {
			problem.Write(w, r, http.StatusNotFound, "route_unavailable", "Route is unavailable.", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func StoreFromContext(ctx context.Context) (application.StoreRecord, bool) {
	store, ok := ctx.Value(storeContextKey{}).(application.StoreRecord)
	return store, ok
}

func tenantSubdomain(host, baseDomain string) (string, bool) {
	host = normalizeHost(host)
	baseDomain = normalizeHost(baseDomain)
	suffix := "." + baseDomain
	if host == "" || baseDomain == "" || !strings.HasSuffix(host, suffix) {
		return "", false
	}
	subdomain := strings.TrimSuffix(host, suffix)
	if subdomain == "" || strings.Contains(subdomain, ".") {
		return "", false
	}
	parsed, err := domain.ParseSubdomain(subdomain)
	if err != nil {
		return "", false
	}
	return parsed.String(), true
}

func normalizeHost(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.TrimSuffix(value, ".")
}
