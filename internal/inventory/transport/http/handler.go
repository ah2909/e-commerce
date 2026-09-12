package inventoryhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/ah2909/e-commerce/internal/inventory/application"
	"github.com/ah2909/e-commerce/internal/inventory/domain"
	"github.com/ah2909/e-commerce/internal/platform/authn"
	"github.com/ah2909/e-commerce/internal/platform/authz"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/problem"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/gin-gonic/gin"
)

const maxBodyBytes = 16 << 10

var idempotencyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

type Service interface {
	Get(context.Context, ids.StoreID, ids.VariantID) (domain.StockItem, error)
	List(context.Context, ids.StoreID, int, string) (application.Page, error)
	Adjust(context.Context, ids.StoreID, domain.Adjustment) (domain.StockItem, error)
}

type Handler struct {
	service      Service
	authenticate gin.HandlerFunc
	tenant       tenant.Middleware
	logger       *slog.Logger
	timeout      time.Duration
}

func NewHandler(service Service, authenticate gin.HandlerFunc, tenantMiddleware tenant.Middleware, logger *slog.Logger, timeout time.Duration) Handler {
	return Handler{service: service, authenticate: authenticate, tenant: tenantMiddleware, logger: logger, timeout: timeout}
}

func (handler Handler) Register(router gin.IRoutes, hosts tenant.HostMiddleware) {
	base := []gin.HandlerFunc{hosts.TenantGin(), handler.withTimeout(), handler.authenticate, handler.tenant.Gin()}
	chain := func(permission storesdomain.Permission, final gin.HandlerFunc) []gin.HandlerFunc {
		return append(append([]gin.HandlerFunc{}, base...), authz.RequireGin(permission, handler.logger), final)
	}
	path := "/api/v1/admin/inventory/stock-items"
	router.GET(path, chain(storesdomain.PermissionInventoryView, handler.list)...)
	router.HEAD(path, chain(storesdomain.PermissionInventoryView, handler.list)...)
	router.GET(path+"/:variant_id", chain(storesdomain.PermissionInventoryView, handler.get)...)
	router.HEAD(path+"/:variant_id", chain(storesdomain.PermissionInventoryView, handler.get)...)
	router.POST(path+"/:variant_id/adjustments", chain(storesdomain.PermissionInventoryManage, handler.adjust)...)
}

type adjustmentRequest struct {
	QuantityDelta   int64  `json:"quantity_delta"`
	Reason          string `json:"reason"`
	ExpectedVersion int64  `json:"expected_version"`
}

func (handler Handler) get(ctx *gin.Context) {
	variantID, ok := parseVariant(ctx)
	if !ok {
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	value, err := handler.service.Get(ctx.Request.Context(), resolved.StoreID(), variantID)
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	writeJSON(ctx, http.StatusOK, value)
}
func (handler Handler) list(ctx *gin.Context) {
	limit := 0
	if raw := ctx.Query("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(ctx, http.StatusBadRequest, "invalid_request", "Request is invalid.")
			return
		}
		limit = value
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	value, err := handler.service.List(ctx.Request.Context(), resolved.StoreID(), limit, ctx.Query("cursor"))
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"stock_items": value.StockItems, "next_cursor": nullable(value.NextCursor)})
}
func (handler Handler) adjust(ctx *gin.Context) {
	variantID, ok := parseVariant(ctx)
	if !ok {
		return
	}
	key := ctx.GetHeader("Idempotency-Key")
	if !idempotencyPattern.MatchString(key) {
		writeProblem(ctx, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key is invalid.")
		return
	}
	var body adjustmentRequest
	if !decode(ctx, &body) {
		return
	}
	principal, ok := authn.FromContext(ctx.Request.Context())
	if !ok || principal.Kind() != authn.KindUser {
		writeProblem(ctx, http.StatusUnauthorized, "authentication_required", "Authentication is required.")
		return
	}
	userID, err := ids.ParseUserID(principal.UserID())
	if err != nil {
		writeProblem(ctx, http.StatusUnauthorized, "authentication_required", "Authentication is required.")
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	value, err := handler.service.Adjust(ctx.Request.Context(), resolved.StoreID(), domain.Adjustment{VariantID: variantID, QuantityDelta: body.QuantityDelta, Reason: body.Reason, ExpectedVersion: body.ExpectedVersion, IdempotencyKey: key, ActorID: userID, RequestID: httpserver.RequestID(ctx.Request.Context())})
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	writeJSON(ctx, http.StatusOK, value)
}
func (handler Handler) withTimeout() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		requestCtx, cancel := context.WithTimeout(ctx.Request.Context(), handler.timeout)
		defer cancel()
		ctx.Request = ctx.Request.WithContext(requestCtx)
		ctx.Next()
	}
}
func parseVariant(ctx *gin.Context) (ids.VariantID, bool) {
	id, err := ids.ParseVariantID(ctx.Param("variant_id"))
	if err != nil {
		writeProblem(ctx, http.StatusNotFound, "inventory_unavailable", "Inventory resource is unavailable.")
		return "", false
	}
	return id, true
}
func decode(ctx *gin.Context, target any) bool {
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxBodyBytes)
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeProblem(ctx, http.StatusBadRequest, "invalid_request", "Request body is invalid.")
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		writeProblem(ctx, http.StatusBadRequest, "invalid_request", "Request body is invalid.")
		return false
	}
	return true
}
func (handler Handler) writeError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidInventory):
		writeProblem(ctx, http.StatusBadRequest, "invalid_inventory", "Inventory input is invalid.")
	case errors.Is(err, application.ErrNotFound):
		writeProblem(ctx, http.StatusNotFound, "inventory_unavailable", "Inventory resource is unavailable.")
	case errors.Is(err, application.ErrConflict), errors.Is(err, domain.ErrInsufficientStock), errors.Is(err, domain.ErrInvalidTransition):
		writeProblem(ctx, http.StatusConflict, "inventory_conflict", "Inventory state conflicts with this request.")
	default:
		handler.logger.Error("inventory operation failed", "request_id", httpserver.RequestID(ctx.Request.Context()))
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &networkError) {
			writeProblem(ctx, http.StatusServiceUnavailable, "dependency_unavailable", "A required service is temporarily unavailable.")
			return
		}
		writeProblem(ctx, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
	}
}
func writeProblem(ctx *gin.Context, status int, code, message string) {
	problem.Write(ctx.Writer, ctx.Request, status, code, message, nil)
}
func writeJSON(ctx *gin.Context, status int, value any) {
	ctx.Header("Content-Type", "application/json")
	ctx.Status(status)
	if ctx.Request.Method != http.MethodHead {
		_ = json.NewEncoder(ctx.Writer).Encode(value)
	}
}
func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
