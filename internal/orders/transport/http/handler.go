package orderhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	identityapplication "github.com/ah2909/e-commerce/internal/identity/application"
	"github.com/ah2909/e-commerce/internal/orders/application"
	"github.com/ah2909/e-commerce/internal/orders/domain"
	"github.com/ah2909/e-commerce/internal/platform/authn"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/problem"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/gin-gonic/gin"
)

type Service interface {
	Checkout(context.Context, application.CheckoutCommand) (application.CheckoutResult, error)
	GetForUser(context.Context, ids.StoreID, ids.OrderID, ids.UserID) (domain.Order, error)
	GetForGrant(context.Context, ids.StoreID, ids.OrderID, string) (domain.Order, error)
}

type CancellationService interface {
	Cancel(context.Context, application.CancellationCommand) (application.CancellationResult, error)
}

type UserAuthenticator interface {
	AuthenticateUser(context.Context, ids.StoreID, string) (identityapplication.UserRecord, string, error)
}

type Handler struct {
	service             Service
	authenticator       UserAuthenticator
	authenticateShopper gin.HandlerFunc
	logger              *slog.Logger
	timeout             time.Duration
	cancellationEnabled bool
	cancellations       CancellationService
}

func (handler Handler) WithCancellation() Handler {
	handler.cancellations, handler.cancellationEnabled = handler.service.(CancellationService), true
	return handler
}

func NewHandler(service Service, authenticator UserAuthenticator, authenticateShopper gin.HandlerFunc, logger *slog.Logger, timeout time.Duration) Handler {
	return Handler{service: service, authenticator: authenticator, authenticateShopper: authenticateShopper, logger: logger, timeout: timeout}
}

func (handler Handler) Register(router gin.IRoutes, hosts tenant.HostMiddleware) {
	checkout := []gin.HandlerFunc{hosts.TenantGin(), handler.withTimeout(), handler.authenticateShopper, handler.checkout}
	read := []gin.HandlerFunc{hosts.TenantGin(), handler.withTimeout(), handler.get}
	router.POST("/api/v1/checkout", checkout...)
	router.GET("/api/v1/orders/:order_id", read...)
	router.HEAD("/api/v1/orders/:order_id", read...)
	if handler.cancellationEnabled {
		router.POST("/api/v1/orders/:order_id/cancellations", hosts.TenantGin(), handler.withTimeout(), handler.cancel)
	}
}

func (handler Handler) cancel(ctx *gin.Context) {
	var body struct {
		Reason string `json:"reason"`
	}
	if !decode(ctx, &body) {
		return
	}
	store, ok := tenant.StoreFromContext(ctx.Request.Context())
	orderID, err := ids.ParseOrderID(ctx.Param("order_id"))
	token, hasToken := bearer(ctx.Request)
	if !ok || err != nil {
		handler.notFound(ctx)
		return
	}
	if !hasToken {
		problem.Write(ctx.Writer, ctx.Request, http.StatusUnauthorized, "authentication_required", "Order authentication is required.", nil)
		return
	}
	command := application.CancellationCommand{StoreID: store.StoreID, OrderID: orderID, IdempotencyKey: idempotencyKey(ctx.Request), Reason: body.Reason, RequestID: httpserver.RequestID(ctx.Request.Context())}
	user, _, authErr := handler.authenticator.AuthenticateUser(ctx.Request.Context(), store.StoreID, token)
	if authErr == nil {
		if _, err = handler.service.GetForUser(ctx.Request.Context(), store.StoreID, orderID, user.ID); err != nil {
			handler.writeError(ctx, err)
			return
		}
		command.ActorType, command.ActorID = application.CancellationUser, user.ID.String()
	} else {
		order, grantErr := handler.service.GetForGrant(ctx.Request.Context(), store.StoreID, orderID, token)
		if grantErr != nil {
			handler.writeError(ctx, grantErr)
			return
		}
		command.ActorType, command.ActorID = application.CancellationGuest, order.Owner.GuestID.String()
	}
	if handler.cancellations == nil {
		handler.writeError(ctx, application.ErrUnprocessable)
		return
	}
	value, err := handler.cancellations.Cancel(ctx.Request.Context(), command)
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

type checkoutRequest struct {
	CartVersion     int64          `json:"cart_version"`
	PaymentProvider string         `json:"payment_provider"`
	AddressID       string         `json:"address_id"`
	Contact         domain.Contact `json:"contact"`
	DeliveryAddress domain.Address `json:"delivery_address"`
}

func (handler Handler) checkout(ctx *gin.Context) {
	var body checkoutRequest
	if !decode(ctx, &body) {
		return
	}
	store, ok := tenant.StoreFromContext(ctx.Request.Context())
	principal, authenticated := authn.FromContext(ctx.Request.Context())
	if !ok || !authenticated || principal.StoreID() != store.StoreID.String() {
		problem.Write(ctx.Writer, ctx.Request, http.StatusUnauthorized, "authentication_required", "Shopper authentication is required.", nil)
		return
	}
	command := application.CheckoutCommand{StoreID: store.StoreID, CartVersion: body.CartVersion, PaymentProvider: body.PaymentProvider, IdempotencyKey: idempotencyKey(ctx.Request), CorrelationID: httpserver.RequestID(ctx.Request.Context())}
	switch principal.Kind() {
	case authn.KindUser:
		userID, err := ids.ParseUserID(principal.UserID())
		addressID, addressErr := ids.ParseAddressID(body.AddressID)
		if err != nil || addressErr != nil {
			handler.writeError(ctx, domain.ErrInvalidOrder)
			return
		}
		command.Owner, command.AddressID = domain.UserOwner(userID), addressID
	case authn.KindGuest:
		guestID, err := ids.ParseGuestID(principal.GuestID())
		if err != nil || body.AddressID != "" {
			handler.writeError(ctx, domain.ErrInvalidOrder)
			return
		}
		command.Owner, command.GuestContact, command.GuestAddress = domain.GuestOwner(guestID), body.Contact, body.DeliveryAddress
	default:
		handler.writeError(ctx, domain.ErrInvalidOrder)
		return
	}
	result, err := handler.service.Checkout(ctx.Request.Context(), command)
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusCreated
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.JSON(status, result)
}

func (handler Handler) get(ctx *gin.Context) {
	store, ok := tenant.StoreFromContext(ctx.Request.Context())
	orderID, err := ids.ParseOrderID(ctx.Param("order_id"))
	token, hasToken := bearer(ctx.Request)
	if !ok || err != nil {
		handler.notFound(ctx)
		return
	}
	if !hasToken {
		problem.Write(ctx.Writer, ctx.Request, http.StatusUnauthorized, "authentication_required", "Order authentication is required.", nil)
		return
	}
	user, _, authErr := handler.authenticator.AuthenticateUser(ctx.Request.Context(), store.StoreID, token)
	var order domain.Order
	if authErr == nil {
		order, err = handler.service.GetForUser(ctx.Request.Context(), store.StoreID, orderID, user.ID)
	} else {
		order, err = handler.service.GetForGrant(ctx.Request.Context(), store.StoreID, orderID, token)
	}
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	if ctx.Request.Method == http.MethodHead {
		ctx.Status(http.StatusOK)
		return
	}
	ctx.JSON(http.StatusOK, order)
}

func (handler Handler) writeError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidOrder):
		problem.Write(ctx.Writer, ctx.Request, http.StatusBadRequest, "invalid_checkout", "Checkout input is invalid.", nil)
	case errors.Is(err, application.ErrNotFound):
		handler.notFound(ctx)
	case errors.Is(err, application.ErrConflict):
		problem.Write(ctx.Writer, ctx.Request, http.StatusConflict, "checkout_conflict", "Checkout conflicts with current state.", nil)
	case errors.Is(err, application.ErrForbidden):
		problem.Write(ctx.Writer, ctx.Request, http.StatusForbidden, "permission_denied", "Permission is denied.", nil)
	case errors.Is(err, application.ErrUnprocessable):
		problem.Write(ctx.Writer, ctx.Request, http.StatusUnprocessableEntity, "order_operation_unavailable", "The order operation is unavailable for the current state.", nil)
	default:
		handler.logger.Error("order operation failed", "request_id", httpserver.RequestID(ctx.Request.Context()))
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &networkError) {
			problem.Write(ctx.Writer, ctx.Request, http.StatusServiceUnavailable, "dependency_unavailable", "A required service is temporarily unavailable.", map[string]any{"retryable": true})
			return
		}
		problem.Write(ctx.Writer, ctx.Request, http.StatusInternalServerError, "internal_error", "An internal error occurred.", nil)
	}
}

func (handler Handler) notFound(ctx *gin.Context) {
	problem.Write(ctx.Writer, ctx.Request, http.StatusNotFound, "order_unavailable", "Order is unavailable.", nil)
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
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 16<<10)
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
	returnValue := len(parts) == 2 && parts[0] == "Bearer" && parts[1] != ""
	return parts[len(parts)-1], returnValue
}

func idempotencyKey(request *http.Request) string {
	values := request.Header.Values("Idempotency-Key")
	if len(values) != 1 {
		return ""
	}
	return values[0]
}
