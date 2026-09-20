package shippinghttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	ordersdomain "github.com/ah2909/e-commerce/internal/orders/domain"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/problem"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shipping/application"
	"github.com/ah2909/e-commerce/internal/shipping/domain"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/gin-gonic/gin"
)

type FulfillmentService interface {
	Mutate(context.Context, application.Command) (domain.Shipment, bool, error)
	GetOrder(context.Context, ids.StoreID, ids.OrderID) (ordersdomain.Order, error)
	ListOrders(context.Context, ids.StoreID, string, int, string) ([]ordersdomain.Order, string, error)
}

type FulfillmentHandler struct {
	service      FulfillmentService
	authenticate gin.HandlerFunc
	tenant       tenant.Middleware
	permissions  FulfillmentPermissionChecker
	logger       *slog.Logger
	timeout      time.Duration
}

type FulfillmentPermissionChecker interface {
	HasPermission(context.Context, ids.StoreID, ids.UserID, storesdomain.Permission) (bool, error)
}

func NewFulfillmentHandler(service FulfillmentService, authenticate gin.HandlerFunc, tenantMiddleware tenant.Middleware, permissions FulfillmentPermissionChecker, logger *slog.Logger, timeout time.Duration) FulfillmentHandler {
	return FulfillmentHandler{service: service, authenticate: authenticate, tenant: tenantMiddleware, permissions: permissions, logger: logger, timeout: timeout}
}

func (handler FulfillmentHandler) Register(router gin.IRoutes, hosts tenant.HostMiddleware) {
	base := []gin.HandlerFunc{hosts.TenantGin(), handler.withTimeout(), handler.authenticate, handler.tenant.Gin(), handler.requireFulfillment()}
	register := func(method, path string, endpoint gin.HandlerFunc) {
		router.Handle(method, path, append(base, endpoint)...)
	}
	register(http.MethodGet, "/api/v1/admin/orders", handler.listOrders)
	register(http.MethodHead, "/api/v1/admin/orders", handler.listOrders)
	register(http.MethodGet, "/api/v1/admin/orders/:order_id", handler.getOrder)
	register(http.MethodHead, "/api/v1/admin/orders/:order_id", handler.getOrder)
	register(http.MethodPost, "/api/v1/admin/orders/:order_id/shipments", handler.createShipment)
	register(http.MethodPut, "/api/v1/admin/shipments/:shipment_id", handler.updateShipment)
	register(http.MethodPost, "/api/v1/admin/shipments/:shipment_id/dispatches", handler.dispatchShipment)
	register(http.MethodPost, "/api/v1/admin/shipments/:shipment_id/completions", handler.completeShipment)
}

func (handler FulfillmentHandler) requireFulfillment() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		resolved, ok := tenant.FromContext(ctx.Request.Context())
		if !ok {
			problem.Write(ctx.Writer, ctx.Request, http.StatusForbidden, "permission_denied", "Permission is denied.", nil)
			ctx.Abort()
			return
		}
		if resolved.Role().Allows(storesdomain.PermissionOrderFulfill) {
			return
		}
		allowed, err := handler.permissions.HasPermission(ctx.Request.Context(), resolved.StoreID(), resolved.UserID(), storesdomain.PermissionOrderFulfill)
		if err != nil {
			handler.writeError(ctx, err)
			ctx.Abort()
			return
		}
		if !allowed {
			problem.Write(ctx.Writer, ctx.Request, http.StatusForbidden, "permission_denied", "Permission is denied.", nil)
			ctx.Abort()
		}
	}
}

func (handler FulfillmentHandler) listOrders(ctx *gin.Context) {
	limit := 25
	if raw := ctx.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			handler.writeError(ctx, domain.ErrInvalidShipment)
			return
		}
		limit = parsed
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	orders, next, err := handler.service.ListOrders(ctx, resolved.StoreID(), ctx.Query("state"), limit, ctx.Query("cursor"))
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.JSON(http.StatusOK, map[string]any{"orders": orders, "next_cursor": nullable(next)})
}

func (handler FulfillmentHandler) getOrder(ctx *gin.Context) {
	orderID, err := ids.ParseOrderID(ctx.Param("order_id"))
	if err != nil {
		handler.writeError(ctx, application.ErrNotFound)
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	order, err := handler.service.GetOrder(ctx, resolved.StoreID(), orderID)
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.JSON(http.StatusOK, order)
}

func (handler FulfillmentHandler) createShipment(ctx *gin.Context) {
	orderID, err := ids.ParseOrderID(ctx.Param("order_id"))
	if err != nil {
		handler.writeError(ctx, application.ErrNotFound)
		return
	}
	var body domain.Details
	if !decodeFulfillment(ctx, &body) {
		return
	}
	handler.mutate(ctx, application.Command{OrderID: orderID, Mutation: application.MutationCreate, Details: body})
}

func (handler FulfillmentHandler) updateShipment(ctx *gin.Context) {
	shipmentID, err := ids.ParseShipmentID(ctx.Param("shipment_id"))
	if err != nil {
		handler.writeError(ctx, application.ErrNotFound)
		return
	}
	var body struct {
		ExpectedVersion int64  `json:"expected_version"`
		Carrier         string `json:"carrier"`
		TrackingNumber  string `json:"tracking_number"`
	}
	if !decodeFulfillment(ctx, &body) {
		return
	}
	handler.mutate(ctx, application.Command{ShipmentID: shipmentID, Mutation: application.MutationUpdate, ExpectedVersion: body.ExpectedVersion, Details: domain.Details{Carrier: body.Carrier, TrackingNumber: body.TrackingNumber}})
}

func (handler FulfillmentHandler) dispatchShipment(ctx *gin.Context) {
	handler.transition(ctx, application.MutationDispatch)
}

func (handler FulfillmentHandler) completeShipment(ctx *gin.Context) {
	handler.transition(ctx, application.MutationComplete)
}

func (handler FulfillmentHandler) transition(ctx *gin.Context, mutation application.Mutation) {
	shipmentID, err := ids.ParseShipmentID(ctx.Param("shipment_id"))
	if err != nil {
		handler.writeError(ctx, application.ErrNotFound)
		return
	}
	var body struct {
		ExpectedVersion int64 `json:"expected_version"`
	}
	if !decodeFulfillment(ctx, &body) {
		return
	}
	handler.mutate(ctx, application.Command{ShipmentID: shipmentID, Mutation: mutation, ExpectedVersion: body.ExpectedVersion})
}

func (handler FulfillmentHandler) mutate(ctx *gin.Context, command application.Command) {
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	command.StoreID, command.ActorID = resolved.StoreID(), resolved.UserID()
	command.IdempotencyKey = ctx.GetHeader("Idempotency-Key")
	command.RequestID = httpserver.RequestID(ctx.Request.Context())
	shipment, created, err := handler.service.Mutate(ctx, command)
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	status := http.StatusOK
	if created && command.Mutation == application.MutationCreate {
		status = http.StatusCreated
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.JSON(status, shipment)
}

func (handler FulfillmentHandler) writeError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidShipment), errors.Is(err, ordersdomain.ErrInvalidOrder):
		problem.Write(ctx.Writer, ctx.Request, http.StatusBadRequest, "invalid_fulfillment", "Fulfillment input is invalid.", nil)
	case errors.Is(err, application.ErrForbidden):
		problem.Write(ctx.Writer, ctx.Request, http.StatusForbidden, "permission_denied", "Permission is denied.", nil)
	case errors.Is(err, application.ErrNotFound):
		problem.Write(ctx.Writer, ctx.Request, http.StatusNotFound, "fulfillment_unavailable", "Fulfillment resource is unavailable.", nil)
	case errors.Is(err, application.ErrConflict):
		problem.Write(ctx.Writer, ctx.Request, http.StatusConflict, "fulfillment_conflict", "Fulfillment conflicts with this request.", nil)
	case errors.Is(err, application.ErrUnprocessable):
		problem.Write(ctx.Writer, ctx.Request, http.StatusUnprocessableEntity, "fulfillment_transition_unavailable", "Fulfillment transition is unavailable.", nil)
	default:
		handler.logger.Error("fulfillment operation failed", "request_id", httpserver.RequestID(ctx.Request.Context()))
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &networkError) {
			problem.Write(ctx.Writer, ctx.Request, http.StatusServiceUnavailable, "dependency_unavailable", "A required dependency is unavailable.", nil)
			return
		}
		problem.Write(ctx.Writer, ctx.Request, http.StatusInternalServerError, "internal_error", "An internal error occurred.", nil)
	}
}

func (handler FulfillmentHandler) withTimeout() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		requestCtx, cancel := context.WithTimeout(ctx.Request.Context(), handler.timeout)
		defer cancel()
		ctx.Request = ctx.Request.WithContext(requestCtx)
		ctx.Next()
	}
}

func decodeFulfillment(ctx *gin.Context, target any) bool {
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 8<<10)
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

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
