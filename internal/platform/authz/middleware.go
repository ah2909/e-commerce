package authz

import (
	"log/slog"
	"net/http"

	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/problem"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/stores/domain"
)

func Require(permission domain.Permission, logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resolved, ok := tenant.FromContext(r.Context())
		if !ok || !resolved.Role().Allows(permission) {
			logger.Warn("permission denied", "request_id", httpserver.RequestID(r.Context()), "permission", permission)
			problem.Write(w, r, http.StatusForbidden, "permission_denied", "Permission is denied.", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}
