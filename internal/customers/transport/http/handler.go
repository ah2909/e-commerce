package customerhttp

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

	"github.com/ah2909/e-commerce/internal/customers/application"
	"github.com/ah2909/e-commerce/internal/customers/domain"
	"github.com/ah2909/e-commerce/internal/platform/authn"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/problem"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/gin-gonic/gin"
)

const maxBodyBytes = 16 << 10

type Service interface {
	GetProfile(context.Context, ids.StoreID, ids.UserID) (domain.Profile, error)
	PutProfile(context.Context, ids.StoreID, ids.UserID, int64, domain.ProfileDraft) (domain.Profile, error)
	CreateAddress(context.Context, ids.StoreID, ids.UserID, domain.AddressDraft) (domain.Address, error)
	GetAddress(context.Context, ids.StoreID, ids.UserID, ids.AddressID) (domain.Address, error)
	ListAddresses(context.Context, ids.StoreID, ids.UserID, int, string) (application.AddressPage, error)
	ReplaceAddress(context.Context, ids.StoreID, ids.UserID, ids.AddressID, int64, domain.AddressDraft) (domain.Address, error)
	ArchiveAddress(context.Context, ids.StoreID, ids.UserID, ids.AddressID, int64) error
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
	base := []gin.HandlerFunc{hosts.TenantGin(), handler.withTimeout(), handler.authenticate, handler.requireUser()}
	router.GET("/api/v1/customer/profile", append(base, handler.getProfile)...)
	router.HEAD("/api/v1/customer/profile", append(base, handler.getProfile)...)
	router.PUT("/api/v1/customer/profile", append(base, handler.putProfile)...)
	router.GET("/api/v1/customer/addresses", append(base, handler.listAddresses)...)
	router.HEAD("/api/v1/customer/addresses", append(base, handler.listAddresses)...)
	router.POST("/api/v1/customer/addresses", append(base, handler.createAddress)...)
	router.GET("/api/v1/customer/addresses/:address_id", append(base, handler.getAddress)...)
	router.HEAD("/api/v1/customer/addresses/:address_id", append(base, handler.getAddress)...)
	router.PUT("/api/v1/customer/addresses/:address_id", append(base, handler.replaceAddress)...)
	router.DELETE("/api/v1/customer/addresses/:address_id", append(base, handler.archiveAddress)...)
}

type profileRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	FullName        string `json:"full_name"`
	Email           string `json:"email"`
	Phone           string `json:"phone"`
}
type addressRequest struct {
	ExpectedVersion int64 `json:"expected_version,omitempty"`
	domain.AddressDraft
}
type versionRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}

func (handler Handler) getProfile(ctx *gin.Context) {
	store, user, ok := scope(ctx)
	if !ok {
		return
	}
	value, err := handler.service.GetProfile(ctx, store, user)
	handler.respond(ctx, http.StatusOK, value, err)
}
func (handler Handler) putProfile(ctx *gin.Context) {
	var body profileRequest
	if !decode(ctx, &body) {
		return
	}
	store, user, ok := scope(ctx)
	if !ok {
		return
	}
	value, err := handler.service.PutProfile(ctx, store, user, body.ExpectedVersion, domain.ProfileDraft{FullName: body.FullName, Email: body.Email, Phone: body.Phone})
	handler.respond(ctx, http.StatusOK, value, err)
}
func (handler Handler) createAddress(ctx *gin.Context) {
	var body addressRequest
	if !decode(ctx, &body) {
		return
	}
	if body.ExpectedVersion != 0 {
		handler.invalid(ctx)
		return
	}
	store, user, ok := scope(ctx)
	if !ok {
		return
	}
	value, err := handler.service.CreateAddress(ctx, store, user, body.AddressDraft)
	handler.respond(ctx, http.StatusCreated, value, err)
}
func (handler Handler) getAddress(ctx *gin.Context) {
	id, ok := addressID(ctx)
	if !ok {
		return
	}
	store, user, ok := scope(ctx)
	if !ok {
		return
	}
	value, err := handler.service.GetAddress(ctx, store, user, id)
	handler.respond(ctx, http.StatusOK, value, err)
}
func (handler Handler) listAddresses(ctx *gin.Context) {
	limit := 0
	if raw := ctx.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			handler.invalid(ctx)
			return
		}
		limit = parsed
	}
	store, user, ok := scope(ctx)
	if !ok {
		return
	}
	value, err := handler.service.ListAddresses(ctx, store, user, limit, ctx.Query("cursor"))
	handler.respond(ctx, http.StatusOK, map[string]any{"addresses": value.Addresses, "next_cursor": nullable(value.NextCursor)}, err)
}
func (handler Handler) replaceAddress(ctx *gin.Context) {
	id, ok := addressID(ctx)
	if !ok {
		return
	}
	var body addressRequest
	if !decode(ctx, &body) {
		return
	}
	store, user, ok := scope(ctx)
	if !ok {
		return
	}
	value, err := handler.service.ReplaceAddress(ctx, store, user, id, body.ExpectedVersion, body.AddressDraft)
	handler.respond(ctx, http.StatusOK, value, err)
}
func (handler Handler) archiveAddress(ctx *gin.Context) {
	id, ok := addressID(ctx)
	if !ok {
		return
	}
	var body versionRequest
	if !decode(ctx, &body) {
		return
	}
	store, user, ok := scope(ctx)
	if !ok {
		return
	}
	err := handler.service.ArchiveAddress(ctx, store, user, id, body.ExpectedVersion)
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

func (handler Handler) requireUser() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		principal, ok := authn.FromContext(ctx.Request.Context())
		if !ok || principal.Kind() != authn.KindUser {
			problem.Write(ctx.Writer, ctx.Request, http.StatusUnauthorized, "authentication_required", "Registered customer authentication is required.", nil)
			ctx.Abort()
			return
		}
		ctx.Next()
	}
}
func scope(ctx *gin.Context) (ids.StoreID, ids.UserID, bool) {
	store, ok := tenant.StoreFromContext(ctx.Request.Context())
	principal, hasPrincipal := authn.FromContext(ctx.Request.Context())
	if !ok || !hasPrincipal || principal.Kind() != authn.KindUser || principal.StoreID() != store.StoreID.String() {
		problem.Write(ctx.Writer, ctx.Request, http.StatusUnauthorized, "authentication_required", "Registered customer authentication is required.", nil)
		return "", "", false
	}
	user, err := ids.ParseUserID(principal.UserID())
	if err != nil {
		problem.Write(ctx.Writer, ctx.Request, http.StatusUnauthorized, "authentication_required", "Registered customer authentication is required.", nil)
		return "", "", false
	}
	return store.StoreID, user, true
}
func addressID(ctx *gin.Context) (ids.AddressID, bool) {
	id, err := ids.ParseAddressID(ctx.Param("address_id"))
	if err != nil {
		problem.Write(ctx.Writer, ctx.Request, http.StatusNotFound, "customer_unavailable", "Customer resource is unavailable.", nil)
		return "", false
	}
	return id, true
}
func (handler Handler) respond(ctx *gin.Context, status int, value any, err error) {
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	writeJSON(ctx, status, value)
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
func (handler Handler) invalid(ctx *gin.Context) {
	problem.Write(ctx.Writer, ctx.Request, http.StatusBadRequest, "invalid_customer", "Customer input is invalid.", nil)
}
func (handler Handler) writeError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidCustomer):
		handler.invalid(ctx)
	case errors.Is(err, application.ErrNotFound):
		problem.Write(ctx.Writer, ctx.Request, http.StatusNotFound, "customer_unavailable", "Customer resource is unavailable.", nil)
	case errors.Is(err, application.ErrConflict):
		problem.Write(ctx.Writer, ctx.Request, http.StatusConflict, "customer_conflict", "Customer state conflicts with this request.", nil)
	default:
		handler.logger.Error("customer operation failed", "request_id", httpserver.RequestID(ctx.Request.Context()))
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
func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
