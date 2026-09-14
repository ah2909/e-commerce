package paymenthttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	identityapplication "github.com/ah2909/e-commerce/internal/identity/application"
	ordersapplication "github.com/ah2909/e-commerce/internal/orders/application"
	ordersdomain "github.com/ah2909/e-commerce/internal/orders/domain"
	"github.com/ah2909/e-commerce/internal/payments/application"
	"github.com/ah2909/e-commerce/internal/payments/domain"
	"github.com/ah2909/e-commerce/internal/platform/authz"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/problem"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/gin-gonic/gin"
)

type Service interface {
	GetConfig(context.Context, ids.StoreID) (domain.VietQRConfig, error)
	ReplaceConfig(context.Context, ids.StoreID, ids.UserID, string, int64, domain.VietQRConfigDraft) (domain.VietQRConfig, error)
	CreateInstruction(context.Context, application.InstructionCommand) (domain.Instruction, error)
	Confirm(context.Context, application.ConfirmationCommand) (domain.Confirmation, error)
	ListReconciliation(context.Context, ids.StoreID, int, time.Time, ids.ReconciliationID) ([]domain.Reconciliation, error)
}

type OrderReader interface {
	GetForUser(context.Context, ids.StoreID, ids.OrderID, ids.UserID) (ordersdomain.Order, error)
	GetForGrant(context.Context, ids.StoreID, ids.OrderID, string) (ordersdomain.Order, error)
}

type UserAuthenticator interface {
	AuthenticateUser(context.Context, ids.StoreID, string) (identityapplication.UserRecord, string, error)
}

type PermissionChecker interface {
	HasPermission(context.Context, ids.StoreID, ids.UserID, storesdomain.Permission) (bool, error)
}

type Handler struct {
	service       Service
	orders        OrderReader
	authenticator UserAuthenticator
	authenticate  gin.HandlerFunc
	tenant        tenant.Middleware
	permissions   PermissionChecker
	logger        *slog.Logger
	timeout       time.Duration
}

func NewHandler(service Service, orders OrderReader, authenticator UserAuthenticator, authenticate gin.HandlerFunc, tenantMiddleware tenant.Middleware, permissions PermissionChecker, logger *slog.Logger, timeout time.Duration) Handler {
	return Handler{service: service, orders: orders, authenticator: authenticator, authenticate: authenticate, tenant: tenantMiddleware, permissions: permissions, logger: logger, timeout: timeout}
}

func (handler Handler) Register(router gin.IRoutes, hosts tenant.HostMiddleware) {
	config := []gin.HandlerFunc{hosts.TenantGin(), handler.withTimeout(), handler.authenticate, handler.tenant.Gin(), authz.RequireGin(storesdomain.PermissionStoreManage, handler.logger)}
	router.GET("/api/v1/admin/payment-providers/vietqr", append(config, handler.getConfig)...)
	router.HEAD("/api/v1/admin/payment-providers/vietqr", append(config, handler.getConfig)...)
	router.PUT("/api/v1/admin/payment-providers/vietqr", append(config, handler.replaceConfig)...)
	router.POST("/api/v1/orders/:order_id/payments/vietqr/instructions", hosts.TenantGin(), handler.withTimeout(), handler.createInstruction)
	workforce := []gin.HandlerFunc{hosts.TenantGin(), handler.withTimeout(), handler.authenticate, handler.tenant.Gin()}
	router.POST("/api/v1/admin/payments/:payment_id/vietqr-confirmations", append(workforce, handler.require(storesdomain.PermissionPaymentConfirm), handler.confirm)...)
	router.GET("/api/v1/admin/payment-reconciliation-items", append(workforce, handler.require(storesdomain.PermissionPaymentReconcile), handler.listReconciliation)...)
}

func (handler Handler) getConfig(ctx *gin.Context) {
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	value, err := handler.service.GetConfig(ctx.Request.Context(), resolved.StoreID())
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	if ctx.Request.Method == http.MethodHead {
		ctx.Status(http.StatusOK)
		return
	}
	ctx.JSON(http.StatusOK, value)
}

func (handler Handler) replaceConfig(ctx *gin.Context) {
	var body struct {
		ExpectedVersion int64  `json:"expected_version"`
		Enabled         bool   `json:"enabled"`
		BankBIN         string `json:"bank_bin"`
		AccountNumber   string `json:"account_number"`
		AccountName     string `json:"account_name"`
	}
	if !decode(ctx, &body) {
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	value, err := handler.service.ReplaceConfig(ctx.Request.Context(), resolved.StoreID(), resolved.UserID(), httpserver.RequestID(ctx.Request.Context()), body.ExpectedVersion, domain.VietQRConfigDraft{BankBIN: body.BankBIN, AccountNumber: body.AccountNumber, AccountName: body.AccountName, Enabled: body.Enabled})
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.JSON(http.StatusOK, value)
}

func (handler Handler) createInstruction(ctx *gin.Context) {
	store, ok := tenant.StoreFromContext(ctx.Request.Context())
	orderID, err := ids.ParseOrderID(ctx.Param("order_id"))
	token, hasToken := bearer(ctx.Request)
	if !ok || err != nil || !hasToken {
		handler.notFound(ctx)
		return
	}
	user, _, authErr := handler.authenticator.AuthenticateUser(ctx.Request.Context(), store.StoreID, token)
	var order ordersdomain.Order
	if authErr == nil {
		order, err = handler.orders.GetForUser(ctx.Request.Context(), store.StoreID, orderID, user.ID)
	} else {
		order, err = handler.orders.GetForGrant(ctx.Request.Context(), store.StoreID, orderID, token)
	}
	if err != nil || order.Payment.Provider != domain.ProviderVietQR {
		handler.notFound(ctx)
		return
	}
	value, err := handler.service.CreateInstruction(ctx.Request.Context(), application.InstructionCommand{StoreID: store.StoreID, OrderID: orderID, PaymentID: order.Payment.ID, IdempotencyKey: idempotencyKey(ctx.Request), CorrelationID: httpserver.RequestID(ctx.Request.Context())})
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	status := http.StatusOK
	if value.Created {
		status = http.StatusCreated
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.JSON(status, value)
}

func (handler Handler) confirm(ctx *gin.Context) {
	var body struct {
		TransferReference string `json:"transfer_reference"`
		AmountMinor       int64  `json:"amount_minor"`
		ReceivedAt        string `json:"received_at"`
	}
	if !decode(ctx, &body) {
		return
	}
	paymentID, idErr := ids.ParsePaymentID(ctx.Param("payment_id"))
	receivedAt, timeErr := time.Parse(time.RFC3339, body.ReceivedAt)
	if idErr != nil || timeErr != nil {
		handler.writeError(ctx, domain.ErrInvalidPayment)
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	value, err := handler.service.Confirm(ctx.Request.Context(), application.ConfirmationCommand{StoreID: resolved.StoreID(), PaymentID: paymentID, ActorID: resolved.UserID(), IdempotencyKey: idempotencyKey(ctx.Request), TransferReference: body.TransferReference, ObservedAmountMinor: body.AmountMinor, ReceivedAt: receivedAt, RequestID: httpserver.RequestID(ctx.Request.Context()), CorrelationID: httpserver.RequestID(ctx.Request.Context())})
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	status := http.StatusOK
	if value.Created {
		status = http.StatusCreated
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.JSON(status, value)
}

func (handler Handler) listReconciliation(ctx *gin.Context) {
	limit := 50
	if raw := ctx.Query("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			handler.writeError(ctx, domain.ErrInvalidPayment)
			return
		}
		limit = value
	}
	var before time.Time
	var beforeID ids.ReconciliationID
	if raw := ctx.Query("before_created_at"); raw != "" {
		var err error
		before, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			handler.writeError(ctx, domain.ErrInvalidPayment)
			return
		}
		beforeID, err = ids.ParseReconciliationID(ctx.Query("before_id"))
		if err != nil {
			handler.writeError(ctx, domain.ErrInvalidPayment)
			return
		}
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	items, err := handler.service.ListReconciliation(ctx.Request.Context(), resolved.StoreID(), limit, before, beforeID)
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.JSON(http.StatusOK, map[string]any{"items": items})
}

func (handler Handler) require(permission storesdomain.Permission) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		resolved, ok := tenant.FromContext(ctx.Request.Context())
		if !ok {
			problem.Write(ctx.Writer, ctx.Request, http.StatusForbidden, "permission_denied", "Permission is denied.", nil)
			ctx.Abort()
			return
		}
		if resolved.Role().Allows(permission) {
			return
		}
		allowed, err := handler.permissions.HasPermission(ctx.Request.Context(), resolved.StoreID(), resolved.UserID(), permission)
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

func (handler Handler) writeError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidPayment):
		problem.Write(ctx.Writer, ctx.Request, http.StatusBadRequest, "invalid_payment_request", "Payment input is invalid.", nil)
	case errors.Is(err, application.ErrNotFound), errors.Is(err, ordersapplication.ErrNotFound):
		handler.notFound(ctx)
	case errors.Is(err, application.ErrForbidden):
		problem.Write(ctx.Writer, ctx.Request, http.StatusForbidden, "permission_denied", "Permission is denied.", nil)
	case errors.Is(err, application.ErrConflict):
		problem.Write(ctx.Writer, ctx.Request, http.StatusConflict, "payment_conflict", "Payment operation conflicts with current state.", nil)
	case errors.Is(err, application.ErrUnavailable):
		problem.Write(ctx.Writer, ctx.Request, http.StatusUnprocessableEntity, "payment_unavailable", "Payment operation is unavailable.", nil)
	case errors.Is(err, application.ErrDependency):
		problem.Write(ctx.Writer, ctx.Request, http.StatusServiceUnavailable, "dependency_unavailable", "A required dependency is unavailable.", map[string]any{"retryable": true})
	case retryable(err):
		problem.Write(ctx.Writer, ctx.Request, http.StatusServiceUnavailable, "dependency_unavailable", "A required dependency is unavailable.", map[string]any{"retryable": true})
	default:
		handler.logger.Error("payment operation failed", "request_id", httpserver.RequestID(ctx.Request.Context()))
		problem.Write(ctx.Writer, ctx.Request, http.StatusInternalServerError, "internal_error", "An internal error occurred.", nil)
	}
}

func (handler Handler) notFound(ctx *gin.Context) {
	problem.Write(ctx.Writer, ctx.Request, http.StatusNotFound, "payment_unavailable", "Payment is unavailable.", nil)
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

func bearer(request *http.Request) (string, bool) {
	values := request.Header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	parts := strings.Split(values[0], " ")
	if len(parts) != 2 || parts[0] != "Bearer" || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func idempotencyKey(request *http.Request) string {
	values := request.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		return ""
	}
	return values[0]
}

func retryable(err error) bool {
	var networkError net.Error
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &networkError)
}
