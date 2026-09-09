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
)

const maxBody = 16 << 10

type Handler struct {
	memberships  application.MembershipManager
	authenticate func(http.Handler) http.Handler
	tenant       tenant.Middleware
	logger       *slog.Logger
	timeout      time.Duration
}

func NewHandler(memberships application.MembershipManager, authenticate func(http.Handler) http.Handler, tenantMiddleware tenant.Middleware, logger *slog.Logger, operationTimeout ...time.Duration) Handler {
	timeout := 2 * time.Second
	if len(operationTimeout) == 1 && operationTimeout[0] > 0 {
		timeout = operationTimeout[0]
	}
	return Handler{memberships: memberships, authenticate: authenticate, tenant: tenantMiddleware, logger: logger, timeout: timeout}
}

func (handler Handler) Register(mux *http.ServeMux, hosts tenant.HostMiddleware) {
	mux.Handle("GET /api/v1/store", hosts.Tenant(handler.memberRoute(domain.PermissionStoreView, http.HandlerFunc(handler.getStore))))
	mux.Handle("GET /api/v1/memberships", hosts.Tenant(handler.memberRoute(domain.PermissionMembershipsView, http.HandlerFunc(handler.listMemberships))))
	mux.Handle("POST /api/v1/memberships", hosts.Tenant(handler.memberRoute(domain.PermissionMembershipsManage, http.HandlerFunc(handler.addMembership))))
	mux.Handle("PATCH /api/v1/memberships/{user_id}", hosts.Tenant(handler.memberRoute(domain.PermissionMembershipsManage, http.HandlerFunc(handler.updateMembership))))
}

func (handler Handler) memberRoute(permission domain.Permission, next http.Handler) http.Handler {
	return handler.withTimeout(handler.authenticate(handler.tenant.Wrap(authz.Require(permission, handler.logger, next))))
}

func (handler Handler) withTimeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), handler.timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (handler Handler) getStore(w http.ResponseWriter, r *http.Request) {
	resolved, _ := tenant.FromContext(r.Context())
	store, _ := tenant.StoreFromContext(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"store_id": resolved.StoreID().String(), "subdomain": store.Subdomain.String(), "name": resolved.StoreName(), "status": resolved.StoreStatus(), "default_currency": resolved.DefaultCurrency(), "locale": resolved.Locale(), "timezone": resolved.Timezone(), "settings_version": resolved.SettingsVersion(), "role": resolved.Role()})
}

func (handler Handler) listMemberships(w http.ResponseWriter, r *http.Request) {
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

func (handler Handler) addMembership(w http.ResponseWriter, r *http.Request) {
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

func (handler Handler) updateMembership(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Role   string `json:"role"`
		Status string `json:"status"`
	}
	if !decode(w, r, &body) {
		return
	}
	targetID, err := ids.ParseUserID(r.PathValue("user_id"))
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
