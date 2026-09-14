package storeshttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/ah2909/e-commerce/internal/platform/authz"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/problem"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/stores/application"
	"github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/gin-gonic/gin"
)

const maxBody = 16 << 10

type Handler struct {
	memberships  application.MembershipManager
	authenticate gin.HandlerFunc
	tenant       tenant.Middleware
	logger       *slog.Logger
	timeout      time.Duration
}

func NewHandler(memberships application.MembershipManager, authenticate gin.HandlerFunc, tenantMiddleware tenant.Middleware, logger *slog.Logger, operationTimeout ...time.Duration) Handler {
	timeout := 2 * time.Second
	if len(operationTimeout) == 1 && operationTimeout[0] > 0 {
		timeout = operationTimeout[0]
	}
	return Handler{memberships: memberships, authenticate: authenticate, tenant: tenantMiddleware, logger: logger, timeout: timeout}
}

func (handler Handler) Register(router gin.IRoutes, hosts tenant.HostMiddleware) {
	registerGet(router, "/api/v1/store", hosts.TenantGin(), handler.withTimeout(), handler.authenticate, handler.tenant.Gin(), authz.RequireGin(domain.PermissionStoreView, handler.logger), handler.getStore)
	registerGet(router, "/api/v1/memberships", hosts.TenantGin(), handler.withTimeout(), handler.authenticate, handler.tenant.Gin(), authz.RequireGin(domain.PermissionMembershipsView, handler.logger), handler.listMemberships)
	router.POST("/api/v1/memberships", hosts.TenantGin(), handler.withTimeout(), handler.authenticate, handler.tenant.Gin(), authz.RequireGin(domain.PermissionMembershipsManage, handler.logger), handler.addMembership)
	router.PATCH("/api/v1/memberships/:user_id", hosts.TenantGin(), handler.withTimeout(), handler.authenticate, handler.tenant.Gin(), authz.RequireGin(domain.PermissionMembershipsManage, handler.logger), handler.updateMembership)
}

func (handler Handler) RegisterPaymentPermissions(router gin.IRoutes, hosts tenant.HostMiddleware) {
	router.PATCH("/api/v1/memberships/:user_id/permissions", hosts.TenantGin(), handler.withTimeout(), handler.authenticate, handler.tenant.Gin(), authz.RequireGin(domain.PermissionMembershipsManage, handler.logger), handler.replacePermissions)
}

func (handler Handler) replacePermissions(ctx *gin.Context) {
	var body struct {
		ExpectedVersion int64    `json:"expected_membership_version"`
		Permissions     []string `json:"permissions"`
	}
	if !decode(ctx.Writer, ctx.Request, &body) {
		return
	}
	targetID, err := ids.ParseUserID(ctx.Param("user_id"))
	if err != nil {
		handler.membershipError(ctx.Writer, ctx.Request, application.ErrMembershipTarget)
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	version, err := handler.memberships.ReplacePermissions(ctx.Request.Context(), application.ReplacePermissionsCommand{StoreID: resolved.StoreID(), ActorID: resolved.UserID(), UserID: targetID, Permissions: body.Permissions, ExpectedVersion: body.ExpectedVersion, RequestID: httpserver.RequestID(ctx.Request.Context())})
	if err != nil {
		handler.membershipError(ctx.Writer, ctx.Request, err)
		return
	}
	writeJSON(ctx.Writer, http.StatusOK, map[string]any{"user_id": targetID.String(), "permissions": body.Permissions, "version": version})
}

func registerGet(router gin.IRoutes, path string, handlers ...gin.HandlerFunc) {
	router.GET(path, handlers...)
	router.HEAD(path, handlers...)
}

func (handler Handler) withTimeout() gin.HandlerFunc {
	return func(request *gin.Context) {
		ctx, cancel := context.WithTimeout(request.Request.Context(), handler.timeout)
		defer cancel()
		request.Request = request.Request.WithContext(ctx)
		request.Next()
	}
}

func (handler Handler) getStore(ctx *gin.Context) {
	w, r := ctx.Writer, ctx.Request
	resolved, _ := tenant.FromContext(r.Context())
	store, _ := tenant.StoreFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"store_id": resolved.StoreID().String(), "subdomain": store.Subdomain.String(), "name": resolved.StoreName(), "status": resolved.StoreStatus(), "default_currency": resolved.DefaultCurrency(), "locale": resolved.Locale(), "timezone": resolved.Timezone(), "settings_version": resolved.SettingsVersion(), "role": resolved.Role()})
}

func (handler Handler) listMemberships(ctx *gin.Context) {
	w, r := ctx.Writer, ctx.Request
	resolved, _ := tenant.FromContext(r.Context())
	memberships, err := handler.memberships.List(r.Context(), resolved.StoreID())
	if err != nil {
		handler.internalError(w, r, err, "membership listing failed")
		return
	}
	items := make([]map[string]any, 0, len(memberships))
	for _, membership := range memberships {
		items = append(items, membershipResponse(membership))
	}
	writeJSON(w, http.StatusOK, map[string]any{"memberships": items})
}

func (handler Handler) addMembership(ctx *gin.Context) {
	w, r := ctx.Writer, ctx.Request
	var body struct {
		UserID string `json:"user_id"`
		Role   string `json:"role"`
	}
	if !decode(w, r, &body) {
		return
	}
	resolved, _ := tenant.FromContext(r.Context())
	membership, err := handler.memberships.Add(r.Context(), application.AddMembershipCommand{StoreID: resolved.StoreID(), ActorID: resolved.UserID(), UserID: body.UserID, Role: body.Role, RequestID: httpserver.RequestID(r.Context())})
	if err != nil {
		handler.membershipError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, membershipResponse(membership))
}

func (handler Handler) updateMembership(ctx *gin.Context) {
	w, r := ctx.Writer, ctx.Request
	var body struct {
		Role   string `json:"role"`
		Status string `json:"status"`
	}
	if !decode(w, r, &body) {
		return
	}
	targetID, err := ids.ParseUserID(ctx.Param("user_id"))
	if err != nil {
		problem.Write(w, r, http.StatusNotFound, "membership_unavailable", "Membership is unavailable.", nil)
		return
	}
	resolved, _ := tenant.FromContext(r.Context())
	membership, err := handler.memberships.Update(r.Context(), application.UpdateMembershipCommand{StoreID: resolved.StoreID(), ActorID: resolved.UserID(), UserID: targetID, Role: body.Role, Status: body.Status, RequestID: httpserver.RequestID(r.Context())})
	if err != nil {
		handler.membershipError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, membershipResponse(membership))
}

func (handler Handler) membershipError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, application.ErrLastOwner) {
		problem.Write(w, r, http.StatusConflict, "last_owner_required", "The store must retain an active owner.", nil)
		return
	}
	if errors.Is(err, application.ErrMembershipTarget) {
		problem.Write(w, r, http.StatusConflict, "membership_unavailable", "Membership is unavailable.", nil)
		return
	}
	if errors.Is(err, application.ErrTenantAccessDenied) {
		problem.Write(w, r, http.StatusForbidden, "permission_denied", "Permission is denied.", nil)
		return
	}
	if errors.Is(err, domain.ErrInvalidMembership) {
		problem.Write(w, r, http.StatusBadRequest, "invalid_membership", "Membership input is invalid.", nil)
		return
	}
	handler.internalError(w, r, err, "membership mutation failed")
}

func (handler Handler) internalError(w http.ResponseWriter, r *http.Request, err error, event string) {
	handler.logger.Error(event, "request_id", httpserver.RequestID(r.Context()))
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) {
		problem.Write(w, r, http.StatusServiceUnavailable, "dependency_unavailable", "A required service is temporarily unavailable.", map[string]any{"retryable": true})
		return
	}
	problem.Write(w, r, http.StatusInternalServerError, "internal_error", "An internal error occurred.", nil)
}

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		problem.Write(w, r, http.StatusBadRequest, "invalid_request", "Request body is invalid.", nil)
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		problem.Write(w, r, http.StatusBadRequest, "invalid_request", "Request body is invalid.", nil)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func membershipResponse(membership domain.Membership) map[string]any {
	return map[string]any{"store_id": membership.StoreID().String(), "user_id": membership.UserID().String(), "status": membership.Status(), "role": membership.Role(), "created_at": membership.CreatedAt(), "updated_at": membership.UpdatedAt()}
}
