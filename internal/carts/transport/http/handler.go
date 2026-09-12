package carthttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/ah2909/e-commerce/internal/carts/application"
	"github.com/ah2909/e-commerce/internal/carts/domain"
	"github.com/ah2909/e-commerce/internal/platform/authn"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/problem"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/gin-gonic/gin"
)

const maxBodyBytes = 4 << 10

type Service interface {
	CreateOrGet(context.Context, ids.StoreID, domain.Owner) (application.View, bool, error)
	Get(context.Context, ids.StoreID, domain.Owner) (application.View, error)
	SetLine(context.Context, ids.StoreID, domain.Owner, ids.VariantID, int64, int64) (application.View, error)
	RemoveLine(context.Context, ids.StoreID, domain.Owner, ids.VariantID, int64) (application.View, error)
	Abandon(context.Context, ids.StoreID, domain.Owner, int64) error
}
type Handler struct {
	service      Service
	authenticate gin.HandlerFunc
	logger       *slog.Logger
	timeout      time.Duration
}

func NewHandler(service Service, authenticate gin.HandlerFunc, logger *slog.Logger, timeout time.Duration) Handler {
	return Handler{service: service, authenticate: authenticate, logger: logger, timeout: timeout}
}
func (handler Handler) Register(router gin.IRoutes, hosts tenant.HostMiddleware) {
	base := []gin.HandlerFunc{hosts.TenantGin(), handler.withTimeout(), handler.authenticate}
	router.POST("/api/v1/cart", append(base, handler.create)...)
	router.GET("/api/v1/cart", append(base, handler.get)...)
	router.HEAD("/api/v1/cart", append(base, handler.get)...)
	router.DELETE("/api/v1/cart", append(base, handler.abandon)...)
	router.PUT("/api/v1/cart/lines/:variant_id", append(base, handler.setLine)...)
	router.DELETE("/api/v1/cart/lines/:variant_id", append(base, handler.removeLine)...)
}

type lineRequest struct {
	Quantity        int64 `json:"quantity"`
	ExpectedVersion int64 `json:"expected_version"`
}
type versionRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}

func (handler Handler) create(ctx *gin.Context) {
	store, owner, ok := shopperScope(ctx)
	if !ok {
		return
	}
	view, created, err := handler.service.CreateOrGet(ctx, store, owner)
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(ctx, status, view)
}
func (handler Handler) get(ctx *gin.Context) {
	store, owner, ok := shopperScope(ctx)
	if !ok {
		return
	}
	view, err := handler.service.Get(ctx, store, owner)
	handler.respond(ctx, view, err)
}
func (handler Handler) setLine(ctx *gin.Context) {
	variant, ok := variantID(ctx)
	if !ok {
		return
	}
	var body lineRequest
	if !decode(ctx, &body) {
		return
	}
	store, owner, ok := shopperScope(ctx)
	if !ok {
		return
	}
	view, err := handler.service.SetLine(ctx, store, owner, variant, body.Quantity, body.ExpectedVersion)
	handler.respond(ctx, view, err)
}
func (handler Handler) removeLine(ctx *gin.Context) {
	variant, ok := variantID(ctx)
	if !ok {
		return
	}
	var body versionRequest
	if !decode(ctx, &body) {
		return
	}
	store, owner, ok := shopperScope(ctx)
	if !ok {
		return
	}
	view, err := handler.service.RemoveLine(ctx, store, owner, variant, body.ExpectedVersion)
	handler.respond(ctx, view, err)
}
func (handler Handler) abandon(ctx *gin.Context) {
	var body versionRequest
	if !decode(ctx, &body) {
		return
	}
	store, owner, ok := shopperScope(ctx)
	if !ok {
		return
	}
	if err := handler.service.Abandon(ctx, store, owner, body.ExpectedVersion); err != nil {
		handler.writeError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}
func shopperScope(ctx *gin.Context) (ids.StoreID, domain.Owner, bool) {
	store, ok := tenant.StoreFromContext(ctx.Request.Context())
	principal, has := authn.FromContext(ctx.Request.Context())
	if !ok || !has || principal.StoreID() != store.StoreID.String() {
		problem.Write(ctx.Writer, ctx.Request, http.StatusUnauthorized, "authentication_required", "Shopper authentication is required.", nil)
		return "", domain.Owner{}, false
	}
	switch principal.Kind() {
	case authn.KindUser:
		id, err := ids.ParseUserID(principal.UserID())
		if err != nil {
			problem.Write(ctx.Writer, ctx.Request, http.StatusUnauthorized, "authentication_required", "Shopper authentication is required.", nil)
			return "", domain.Owner{}, false
		}
		return store.StoreID, application.OwnerFromUser(id), true
	case authn.KindGuest:
		id, err := ids.ParseGuestID(principal.GuestID())
		if err != nil {
			problem.Write(ctx.Writer, ctx.Request, http.StatusUnauthorized, "authentication_required", "Shopper authentication is required.", nil)
			return "", domain.Owner{}, false
		}
		return store.StoreID, application.OwnerFromGuest(id), true
	default:
		problem.Write(ctx.Writer, ctx.Request, http.StatusUnauthorized, "authentication_required", "Shopper authentication is required.", nil)
		return "", domain.Owner{}, false
	}
}
func variantID(ctx *gin.Context) (ids.VariantID, bool) {
	id, err := ids.ParseVariantID(ctx.Param("variant_id"))
	if err != nil {
		problem.Write(ctx.Writer, ctx.Request, http.StatusNotFound, "cart_unavailable", "Cart resource is unavailable.", nil)
		return "", false
	}
	return id, true
}
func (handler Handler) respond(ctx *gin.Context, value application.View, err error) {
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
func decode(ctx *gin.Context, target any) bool {
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxBodyBytes)
	decoder := json.NewDecoder(ctx.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		problem.Write(ctx.Writer, ctx.Request, http.StatusBadRequest, "invalid_request", "Request body is invalid.", nil)
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		problem.Write(ctx.Writer, ctx.Request, http.StatusBadRequest, "invalid_request", "Request body is invalid.", nil)
		return false
	}
	return true
}
func (handler Handler) writeError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidCart):
		problem.Write(ctx.Writer, ctx.Request, http.StatusBadRequest, "invalid_cart", "Cart input is invalid.", nil)
	case errors.Is(err, application.ErrNotFound):
		problem.Write(ctx.Writer, ctx.Request, http.StatusNotFound, "cart_unavailable", "Cart resource is unavailable.", nil)
	case errors.Is(err, application.ErrConflict):
		problem.Write(ctx.Writer, ctx.Request, http.StatusConflict, "cart_conflict", "Cart state conflicts with this request.", nil)
	default:
		handler.logger.Error("cart operation failed", "request_id", httpserver.RequestID(ctx.Request.Context()))
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &networkError) {
			problem.Write(ctx.Writer, ctx.Request, http.StatusServiceUnavailable, "dependency_unavailable", "A required service is temporarily unavailable.", nil)
			return
		}
		problem.Write(ctx.Writer, ctx.Request, http.StatusInternalServerError, "internal_error", "An internal error occurred.", nil)
	}
}
func writeJSON(ctx *gin.Context, status int, value any) {
	ctx.Header("Content-Type", "application/json")
	ctx.Status(status)
	if ctx.Request.Method != http.MethodHead {
		_ = json.NewEncoder(ctx.Writer).Encode(value)
	}
}
