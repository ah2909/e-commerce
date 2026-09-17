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

type MoMoService interface {
	GetMoMoConfig(context.Context, ids.StoreID) (domain.MoMoConfig, error)
	ReplaceMoMoConfig(context.Context, ids.StoreID, ids.UserID, string, int64, domain.MoMoConfigDraft) (domain.MoMoConfig, error)
	StartMoMo(context.Context, application.InstructionCommand) (domain.MoMoAttempt, error)
	HandleMoMoIPN(context.Context, string, []byte, string) (domain.WebhookResult, error)
}

type RefundService interface {
	GetRefund(context.Context, ids.StoreID, ids.RefundID) (domain.Refund, error)
	ExecuteMoMoRefund(context.Context, ids.StoreID, ids.RefundID, ids.UserID, string) (domain.Refund, error)
	SettleVietQRRefund(context.Context, ids.StoreID, ids.RefundID, ids.UserID, string, string, time.Time, string) (domain.Refund, error)
}

type OrderReader interface {
	GetForUser(context.Context, ids.StoreID, ids.OrderID, ids.UserID) (ordersdomain.Order, error)
	GetForGrant(context.Context, ids.StoreID, ids.OrderID, string) (ordersdomain.Order, error)
}

type OrderCanceller interface {
	Cancel(context.Context, ordersapplication.CancellationCommand) (ordersapplication.CancellationResult, error)
}

type UserAuthenticator interface {
	AuthenticateUser(context.Context, ids.StoreID, string) (identityapplication.UserRecord, string, error)
}

type PermissionChecker interface {
	HasPermission(context.Context, ids.StoreID, ids.UserID, storesdomain.Permission) (bool, error)
}

type Handler struct {
	service        Service
	orders         OrderReader
	authenticator  UserAuthenticator
	authenticate   gin.HandlerFunc
	tenant         tenant.Middleware
	permissions    PermissionChecker
	logger         *slog.Logger
	timeout        time.Duration
	momo           MoMoService
	momoTimeout    time.Duration
	refundsEnabled bool
	refunds        RefundService
	cancellations  OrderCanceller
}

func (handler Handler) WithMoMo(service MoMoService, timeout time.Duration) Handler {
	handler.momo = service
	handler.momoTimeout = timeout
	return handler
}

func (handler Handler) WithRefunds() Handler {
	handler.refunds, handler.refundsEnabled = handler.momo.(RefundService), true
	handler.cancellations, _ = handler.orders.(OrderCanceller)
	return handler
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
	if handler.momo != nil {
		router.GET("/api/v1/admin/payment-providers/momo", append(config, handler.getMoMoConfig)...)
		router.HEAD("/api/v1/admin/payment-providers/momo", append(config, handler.getMoMoConfig)...)
		router.PUT("/api/v1/admin/payment-providers/momo", append(config, handler.replaceMoMoConfig)...)
		router.POST("/api/v1/orders/:order_id/payments/momo/attempts", hosts.TenantGin(), handler.withMoMoTimeout(), handler.startMoMo)
		router.POST("/api/v1/payment-webhooks/momo/:callback_token", handler.withTimeout(), handler.handleMoMoIPN)
		if handler.refundsEnabled {
			router.POST("/api/v1/admin/orders/:order_id/cancellations", append(workforce, handler.require(storesdomain.PermissionOrderCancel), handler.cancelOrder)...)
			router.GET("/api/v1/admin/refunds/:refund_id", append(workforce, handler.require(storesdomain.PermissionPaymentRefund), handler.getRefund)...)
			router.POST("/api/v1/admin/refunds/:refund_id/momo-executions", append(workforce, handler.require(storesdomain.PermissionPaymentRefund), handler.withMoMoTimeout(), handler.executeMoMoRefund)...)
			router.POST("/api/v1/admin/refunds/:refund_id/vietqr-settlements", append(workforce, handler.require(storesdomain.PermissionPaymentRefund), handler.settleVietQRRefund)...)
		}
	}
}

func (handler Handler) cancelOrder(ctx *gin.Context) {
	var body struct {
		Reason string `json:"reason"`
	}
	if !decode(ctx, &body) {
		return
	}
	orderID, err := ids.ParseOrderID(ctx.Param("order_id"))
	if err != nil {
		handler.notFound(ctx)
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	if handler.cancellations == nil {
		handler.writeOrderError(ctx, ordersapplication.ErrUnprocessable)
		return
	}
	value, err := handler.cancellations.Cancel(ctx.Request.Context(), ordersapplication.CancellationCommand{StoreID: resolved.StoreID(), OrderID: orderID, ActorType: ordersapplication.CancellationWorkforce, ActorID: resolved.UserID().String(), IdempotencyKey: idempotencyKey(ctx.Request), Reason: body.Reason, RequestID: httpserver.RequestID(ctx.Request.Context())})
	if err != nil {
		handler.writeOrderError(ctx, err)
		return
	}
	status := http.StatusOK
	if value.Created {
		status = http.StatusCreated
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.JSON(status, value)
}

func (handler Handler) getRefund(ctx *gin.Context) {
	refundID, err := ids.ParseRefundID(ctx.Param("refund_id"))
	if err != nil {
		handler.notFound(ctx)
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	value, err := handler.refunds.GetRefund(ctx.Request.Context(), resolved.StoreID(), refundID)
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.JSON(http.StatusOK, value)
}

func (handler Handler) executeMoMoRefund(ctx *gin.Context) {
	refundID, err := ids.ParseRefundID(ctx.Param("refund_id"))
	if err != nil {
		handler.notFound(ctx)
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	value, err := handler.refunds.ExecuteMoMoRefund(ctx.Request.Context(), resolved.StoreID(), refundID, resolved.UserID(), idempotencyKey(ctx.Request))
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.JSON(http.StatusOK, value)
}

func (handler Handler) settleVietQRRefund(ctx *gin.Context) {
	var body struct {
		ExternalReference string `json:"external_reference"`
		SettledAt         string `json:"settled_at"`
	}
	if !decode(ctx, &body) {
		return
	}
	refundID, idErr := ids.ParseRefundID(ctx.Param("refund_id"))
	settledAt, timeErr := time.Parse(time.RFC3339, body.SettledAt)
	if idErr != nil || timeErr != nil {
		handler.writeError(ctx, domain.ErrInvalidPayment)
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	value, err := handler.refunds.SettleVietQRRefund(ctx.Request.Context(), resolved.StoreID(), refundID, resolved.UserID(), idempotencyKey(ctx.Request), body.ExternalReference, settledAt, httpserver.RequestID(ctx.Request.Context()))
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.JSON(http.StatusOK, value)
}

func (handler Handler) writeOrderError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, ordersdomain.ErrInvalidOrder):
		problem.Write(ctx.Writer, ctx.Request, http.StatusBadRequest, "invalid_cancellation", "Cancellation input is invalid.", nil)
	case errors.Is(err, ordersapplication.ErrNotFound):
		handler.notFound(ctx)
	case errors.Is(err, ordersapplication.ErrConflict):
		problem.Write(ctx.Writer, ctx.Request, http.StatusConflict, "cancellation_conflict", "Cancellation conflicts with the recorded request.", nil)
	case errors.Is(err, ordersapplication.ErrForbidden):
		problem.Write(ctx.Writer, ctx.Request, http.StatusForbidden, "permission_denied", "Permission is denied.", nil)
	case errors.Is(err, ordersapplication.ErrUnprocessable):
		problem.Write(ctx.Writer, ctx.Request, http.StatusUnprocessableEntity, "cancellation_unavailable", "Cancellation is unavailable for the current order.", nil)
	default:
		handler.writeError(ctx, err)
	}
}

func (handler Handler) getMoMoConfig(ctx *gin.Context) {
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	value, err := handler.momo.GetMoMoConfig(ctx.Request.Context(), resolved.StoreID())
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

func (handler Handler) replaceMoMoConfig(ctx *gin.Context) {
	var body struct {
		ExpectedVersion int64  `json:"expected_version"`
		Enabled         bool   `json:"enabled"`
		PartnerCode     string `json:"partner_code"`
		AccessKey       string `json:"access_key"`
		SecretKey       string `json:"secret_key"`
	}
	if !decode(ctx, &body) {
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	value, err := handler.momo.ReplaceMoMoConfig(ctx.Request.Context(), resolved.StoreID(), resolved.UserID(), httpserver.RequestID(ctx.Request.Context()), body.ExpectedVersion, domain.MoMoConfigDraft{PartnerCode: body.PartnerCode, AccessKey: body.AccessKey, SecretKey: body.SecretKey, Enabled: body.Enabled})
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.JSON(http.StatusOK, value)
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

func (handler Handler) startMoMo(ctx *gin.Context) {
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
	if err != nil || order.Payment.Provider != domain.ProviderMoMo {
		handler.notFound(ctx)
		return
	}
	value, err := handler.momo.StartMoMo(ctx.Request.Context(), application.InstructionCommand{StoreID: store.StoreID, OrderID: orderID, PaymentID: order.Payment.ID, IdempotencyKey: idempotencyKey(ctx.Request), CorrelationID: httpserver.RequestID(ctx.Request.Context())})
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

func (handler Handler) handleMoMoIPN(ctx *gin.Context) {
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 16<<10)
	raw, err := io.ReadAll(ctx.Request.Body)
	if err != nil {
		problem.Write(ctx.Writer, ctx.Request, http.StatusBadRequest, "invalid_payment_notification", "Payment notification is invalid.", nil)
		return
	}
	_, err = handler.momo.HandleMoMoIPN(ctx.Request.Context(), ctx.Param("callback_token"), raw, httpserver.RequestID(ctx.Request.Context()))
	if err != nil {
		if errors.Is(err, application.ErrDependency) || retryable(err) {
			problem.Write(ctx.Writer, ctx.Request, http.StatusServiceUnavailable, "dependency_unavailable", "A required dependency is unavailable.", map[string]any{"retryable": true})
			return
		}
		if errors.Is(err, application.ErrConflict) {
			problem.Write(ctx.Writer, ctx.Request, http.StatusConflict, "payment_notification_conflict", "Payment notification conflicts with the recorded result.", nil)
			return
		}
		problem.Write(ctx.Writer, ctx.Request, http.StatusBadRequest, "invalid_payment_notification", "Payment notification is invalid.", nil)
		return
	}
	ctx.Status(http.StatusNoContent)
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

func (handler Handler) withMoMoTimeout() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		requestCtx, cancel := context.WithTimeout(ctx.Request.Context(), handler.momoTimeout+5*time.Second)
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
