package shippinghttp

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
	"github.com/ah2909/e-commerce/internal/shipping/application"
	"github.com/ah2909/e-commerce/internal/shipping/domain"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/gin-gonic/gin"
)

type Service interface {
	Get(context.Context, ids.StoreID) (domain.Rule, error)
	Replace(context.Context, ids.StoreID, int64, domain.Draft) (domain.Rule, error)
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
	base := []gin.HandlerFunc{hosts.TenantGin(), handler.withTimeout(), handler.authenticate, handler.tenant.Gin(), authz.RequireGin(storesdomain.PermissionStoreManage, handler.logger)}
	router.GET("/api/v1/admin/shipping/rule", append(base, handler.get)...)
	router.HEAD("/api/v1/admin/shipping/rule", append(base, handler.get)...)
	router.PUT("/api/v1/admin/shipping/rule", append(base, handler.replace)...)
}

func (handler Handler) get(ctx *gin.Context) {
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	value, err := handler.service.Get(ctx, resolved.StoreID())
	handler.respond(ctx, http.StatusOK, value, err)
}

func (handler Handler) replace(ctx *gin.Context) {
	var body struct {
		ExpectedVersion int64  `json:"expected_version"`
		Mode            string `json:"mode"`
		FlatAmountMinor int64  `json:"flat_amount_minor"`
	}
	if !decode(ctx, &body) {
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	value, err := handler.service.Replace(ctx, resolved.StoreID(), body.ExpectedVersion, domain.Draft{Mode: body.Mode, AmountMinor: body.FlatAmountMinor})
	handler.respond(ctx, http.StatusOK, value, err)
}

func (handler Handler) respond(ctx *gin.Context, status int, value domain.Rule, err error) {
	if err == nil {
		ctx.JSON(status, value)
		return
	}
	switch {
	case errors.Is(err, domain.ErrInvalidRule):
		problem.Write(ctx.Writer, ctx.Request, http.StatusBadRequest, "invalid_shipping_rule", "Shipping rule is invalid.", nil)
	case errors.Is(err, application.ErrNotFound):
		problem.Write(ctx.Writer, ctx.Request, http.StatusNotFound, "shipping_rule_unavailable", "Shipping rule is unavailable.", nil)
	case errors.Is(err, application.ErrConflict):
		problem.Write(ctx.Writer, ctx.Request, http.StatusConflict, "shipping_rule_conflict", "Shipping rule conflicts with this request.", nil)
	case retryable(err):
		problem.Write(ctx.Writer, ctx.Request, http.StatusServiceUnavailable, "dependency_unavailable", "A required dependency is unavailable.", nil)
	default:
		handler.logger.Error("shipping rule operation failed", "request_id", httpserver.RequestID(ctx.Request.Context()))
		problem.Write(ctx.Writer, ctx.Request, http.StatusInternalServerError, "internal_error", "An internal error occurred.", nil)
	}
}

func retryable(err error) bool {
	var networkError net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &networkError)
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
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 4<<10)
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
