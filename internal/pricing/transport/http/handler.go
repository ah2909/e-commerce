package pricinghttp

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

	"github.com/ah2909/e-commerce/internal/platform/authz"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/problem"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/pricing/application"
	"github.com/ah2909/e-commerce/internal/pricing/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/gin-gonic/gin"
)

const maxBodyBytes = 256 << 10

type Service interface {
	Create(context.Context, ids.StoreID, domain.PriceListDraft) (domain.PriceList, error)
	Replace(context.Context, ids.StoreID, ids.PriceListID, int64, domain.PriceListDraft) (domain.PriceList, error)
	Get(context.Context, ids.StoreID, ids.PriceListID) (domain.PriceList, error)
	List(context.Context, ids.StoreID, int, string) (application.Page, error)
	Archive(context.Context, ids.StoreID, ids.PriceListID, int64) (domain.PriceList, error)
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
	middleware := []gin.HandlerFunc{hosts.TenantGin(), handler.withTimeout(), handler.authenticate, handler.tenant.Gin(), authz.RequireGin(storesdomain.PermissionPricingManage, handler.logger)}
	chain := func(final gin.HandlerFunc) []gin.HandlerFunc {
		return append(append([]gin.HandlerFunc{}, middleware...), final)
	}
	base := "/api/v1/admin/pricing/price-lists"
	router.POST(base, chain(handler.create)...)
	router.GET(base, chain(handler.list)...)
	router.HEAD(base, chain(handler.list)...)
	router.GET(base+"/:price_list_id", chain(handler.get)...)
	router.HEAD(base+"/:price_list_id", chain(handler.get)...)
	router.PUT(base+"/:price_list_id", chain(handler.replace)...)
	router.POST(base+"/:price_list_id/archive", chain(handler.archive)...)
}

type priceRequest struct {
	VariantID   string `json:"variant_id"`
	AmountMinor int64  `json:"amount_minor"`
}

type priceListRequest struct {
	Name            string         `json:"name"`
	Channel         string         `json:"channel"`
	Currency        string         `json:"currency"`
	Priority        int            `json:"priority"`
	ValidFrom       *time.Time     `json:"valid_from"`
	ValidUntil      *time.Time     `json:"valid_until"`
	ExpectedVersion int64          `json:"expected_version,omitempty"`
	Prices          []priceRequest `json:"prices"`
}

type versionRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}

func (handler Handler) create(ctx *gin.Context) {
	var body priceListRequest
	if !decode(ctx, &body) {
		return
	}
	draft, ok := draft(ctx, body)
	if !ok {
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	value, err := handler.service.Create(ctx.Request.Context(), resolved.StoreID(), draft)
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	writeJSON(ctx, http.StatusCreated, value)
}

func (handler Handler) replace(ctx *gin.Context) {
	id, err := ids.ParsePriceListID(ctx.Param("price_list_id"))
	if err != nil {
		handler.notFound(ctx)
		return
	}
	var body priceListRequest
	if !decode(ctx, &body) {
		return
	}
	valueDraft, ok := draft(ctx, body)
	if !ok {
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	value, err := handler.service.Replace(ctx.Request.Context(), resolved.StoreID(), id, body.ExpectedVersion, valueDraft)
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	writeJSON(ctx, http.StatusOK, value)
}

func (handler Handler) get(ctx *gin.Context) {
	id, err := ids.ParsePriceListID(ctx.Param("price_list_id"))
	if err != nil {
		handler.notFound(ctx)
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	value, err := handler.service.Get(ctx.Request.Context(), resolved.StoreID(), id)
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	writeJSON(ctx, http.StatusOK, value)
}

func (handler Handler) list(ctx *gin.Context) {
	limit := 0
	if raw := ctx.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(ctx, http.StatusBadRequest, "invalid_request", "Request is invalid.")
			return
		}
		limit = parsed
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	page, err := handler.service.List(ctx.Request.Context(), resolved.StoreID(), limit, ctx.Query("cursor"))
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"price_lists": page.PriceLists, "next_cursor": nullable(page.NextCursor)})
}

func (handler Handler) archive(ctx *gin.Context) {
	id, err := ids.ParsePriceListID(ctx.Param("price_list_id"))
	if err != nil {
		handler.notFound(ctx)
		return
	}
	var body versionRequest
	if !decode(ctx, &body) {
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	value, err := handler.service.Archive(ctx.Request.Context(), resolved.StoreID(), id, body.ExpectedVersion)
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	writeJSON(ctx, http.StatusOK, value)
}

func draft(ctx *gin.Context, body priceListRequest) (domain.PriceListDraft, bool) {
	result := domain.PriceListDraft{Name: body.Name, Channel: body.Channel, Currency: body.Currency, Priority: body.Priority, ValidFrom: body.ValidFrom, ValidUntil: body.ValidUntil}
	for _, price := range body.Prices {
		variantID, err := ids.ParseVariantID(price.VariantID)
		if err != nil {
			writeProblem(ctx, http.StatusBadRequest, "invalid_pricing", "Pricing input is invalid.")
			return domain.PriceListDraft{}, false
		}
		result.Prices = append(result.Prices, domain.PriceDraft{VariantID: variantID, AmountMinor: price.AmountMinor})
	}
	return result, true
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
	case errors.Is(err, domain.ErrInvalidPricing):
		writeProblem(ctx, http.StatusBadRequest, "invalid_pricing", "Pricing input is invalid.")
	case errors.Is(err, application.ErrNotFound):
		handler.notFound(ctx)
	case errors.Is(err, application.ErrConflict):
		writeProblem(ctx, http.StatusConflict, "pricing_conflict", "Pricing state conflicts with this request.")
	default:
		handler.logger.Error("pricing operation failed", "request_id", httpserver.RequestID(ctx.Request.Context()))
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &networkError) {
			writeProblem(ctx, http.StatusServiceUnavailable, "dependency_unavailable", "A required service is temporarily unavailable.")
			return
		}
		writeProblem(ctx, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
	}
}

func (handler Handler) notFound(ctx *gin.Context) {
	writeProblem(ctx, http.StatusNotFound, "pricing_unavailable", "Pricing resource is unavailable.")
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
