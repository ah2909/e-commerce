package cataloghttp

import (
	"bytes"
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

	"github.com/ah2909/e-commerce/internal/catalog/application"
	"github.com/ah2909/e-commerce/internal/catalog/domain"
	"github.com/ah2909/e-commerce/internal/platform/authz"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/problem"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/gin-gonic/gin"
	"golang.org/x/text/unicode/norm"
)

const maxBodyBytes = 256 << 10

type Service interface {
	CreateProduct(context.Context, ids.StoreID, domain.ProductDraft) (domain.Product, error)
	ReplaceProduct(context.Context, ids.StoreID, ids.ProductID, int64, domain.ProductDraft) (domain.Product, error)
	GetProduct(context.Context, ids.StoreID, ids.ProductID) (domain.Product, error)
	ListProducts(context.Context, ids.StoreID, int, string) (application.Page, error)
	ArchiveProduct(context.Context, ids.StoreID, ids.ProductID, int64) (domain.Product, error)
	CreateCategory(context.Context, ids.StoreID, *ids.CategoryID, string, string) (domain.Category, error)
	ReplaceCategory(context.Context, ids.StoreID, ids.CategoryID, int64, *ids.CategoryID, string, string) (domain.Category, error)
	ListCategories(context.Context, ids.StoreID) ([]domain.Category, error)
	ArchiveCategory(context.Context, ids.StoreID, ids.CategoryID, int64) (domain.Category, error)
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
	middleware := []gin.HandlerFunc{hosts.TenantGin(), handler.withTimeout(), handler.authenticate, handler.tenant.Gin(), authz.RequireGin(storesdomain.PermissionCatalogManage, handler.logger)}
	chain := func(final gin.HandlerFunc) []gin.HandlerFunc {
		return append(append([]gin.HandlerFunc{}, middleware...), final)
	}
	base := "/api/v1/admin/catalog"
	router.POST(base+"/categories", chain(handler.createCategory)...)
	router.GET(base+"/categories", chain(handler.listCategories)...)
	router.HEAD(base+"/categories", chain(handler.listCategories)...)
	router.PUT(base+"/categories/:category_id", chain(handler.replaceCategory)...)
	router.POST(base+"/categories/:category_id/archive", chain(handler.archiveCategory)...)
	router.POST(base+"/products", chain(handler.createProduct)...)
	router.GET(base+"/products", chain(handler.listProducts)...)
	router.HEAD(base+"/products", chain(handler.listProducts)...)
	router.GET(base+"/products/:product_id", chain(handler.getProduct)...)
	router.HEAD(base+"/products/:product_id", chain(handler.getProduct)...)
	router.PUT(base+"/products/:product_id", chain(handler.replaceProduct)...)
	router.POST(base+"/products/:product_id/archive", chain(handler.archiveProduct)...)
}

func (handler Handler) withTimeout() gin.HandlerFunc {
	return func(request *gin.Context) {
		ctx, cancel := context.WithTimeout(request.Request.Context(), handler.timeout)
		defer cancel()
		request.Request = request.Request.WithContext(ctx)
		request.Next()
	}
}

type categoryRequest struct {
	ParentID        *string `json:"parent_id"`
	Name            string  `json:"name"`
	Slug            string  `json:"slug"`
	ExpectedVersion int64   `json:"expected_version,omitempty"`
}

type dimensionsRequest struct {
	LengthMM *int64 `json:"length_mm"`
	WidthMM  *int64 `json:"width_mm"`
	HeightMM *int64 `json:"height_mm"`
}

type variantRequest struct {
	VariantID    string              `json:"variant_id,omitempty"`
	SKU          string              `json:"sku"`
	Barcode      *string             `json:"barcode"`
	OptionValues optionValuesRequest `json:"option_values"`
	WeightGrams  int64               `json:"weight_grams"`
	Dimensions   dimensionsRequest   `json:"dimensions"`
}

type mediaRequest struct {
	MediaID   string `json:"media_id,omitempty"`
	ObjectKey string `json:"object_key"`
	AltText   string `json:"alt_text"`
	Position  int    `json:"position"`
}

type productRequest struct {
	Name            string           `json:"name"`
	Slug            string           `json:"slug"`
	Description     string           `json:"description"`
	ExpectedVersion int64            `json:"expected_version,omitempty"`
	Variants        []variantRequest `json:"variants"`
	CategoryIDs     []string         `json:"category_ids"`
	Media           []mediaRequest   `json:"media"`
}

type versionRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}

type optionValuesRequest map[string]string

func (values *optionValuesRequest) UnmarshalJSON(encoded []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return domain.ErrInvalidCatalog
	}
	result := make(optionValuesRequest)
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return domain.ErrInvalidCatalog
		}
		key, ok := token.(string)
		if !ok {
			return domain.ErrInvalidCatalog
		}
		canonical := strings.ToLower(norm.NFC.String(strings.TrimSpace(key)))
		if seen[canonical] {
			return domain.ErrInvalidCatalog
		}
		seen[canonical] = true
		var value string
		if err := decoder.Decode(&value); err != nil {
			return domain.ErrInvalidCatalog
		}
		result[key] = value
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') {
		return domain.ErrInvalidCatalog
	}
	*values = result
	return nil
}

func (handler Handler) createCategory(ctx *gin.Context) {
	var body categoryRequest
	if !decode(ctx, &body) {
		return
	}
	parent, ok := parseOptionalCategory(ctx, body.ParentID)
	if !ok {
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	category, err := handler.service.CreateCategory(ctx.Request.Context(), resolved.StoreID(), parent, body.Name, body.Slug)
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	writeJSON(ctx, http.StatusCreated, category)
}

func (handler Handler) listCategories(ctx *gin.Context) {
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	categories, err := handler.service.ListCategories(ctx.Request.Context(), resolved.StoreID())
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"categories": categories})
}

func (handler Handler) replaceCategory(ctx *gin.Context) {
	id, err := ids.ParseCategoryID(ctx.Param("category_id"))
	if err != nil {
		handler.notFound(ctx)
		return
	}
	var body categoryRequest
	if !decode(ctx, &body) {
		return
	}
	parent, ok := parseOptionalCategory(ctx, body.ParentID)
	if !ok {
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	category, err := handler.service.ReplaceCategory(ctx.Request.Context(), resolved.StoreID(), id, body.ExpectedVersion, parent, body.Name, body.Slug)
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	writeJSON(ctx, http.StatusOK, category)
}

func (handler Handler) archiveCategory(ctx *gin.Context) {
	id, err := ids.ParseCategoryID(ctx.Param("category_id"))
	if err != nil {
		handler.notFound(ctx)
		return
	}
	var body versionRequest
	if !decode(ctx, &body) {
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	category, err := handler.service.ArchiveCategory(ctx.Request.Context(), resolved.StoreID(), id, body.ExpectedVersion)
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	writeJSON(ctx, http.StatusOK, category)
}

func (handler Handler) createProduct(ctx *gin.Context) {
	var body productRequest
	if !decode(ctx, &body) {
		return
	}
	draft, ok := productDraft(ctx, body)
	if !ok {
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	product, err := handler.service.CreateProduct(ctx.Request.Context(), resolved.StoreID(), draft)
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	writeJSON(ctx, http.StatusCreated, product)
}

func (handler Handler) listProducts(ctx *gin.Context) {
	limit := 0
	if raw := ctx.Query("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			handler.invalid(ctx)
			return
		}
		limit = parsed
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	page, err := handler.service.ListProducts(ctx.Request.Context(), resolved.StoreID(), limit, ctx.Query("cursor"))
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	writeJSON(ctx, http.StatusOK, map[string]any{"products": page.Products, "next_cursor": nullableCursor(page.NextCursor)})
}

func (handler Handler) getProduct(ctx *gin.Context) {
	id, err := ids.ParseProductID(ctx.Param("product_id"))
	if err != nil {
		handler.notFound(ctx)
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	product, err := handler.service.GetProduct(ctx.Request.Context(), resolved.StoreID(), id)
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	writeJSON(ctx, http.StatusOK, product)
}

func (handler Handler) replaceProduct(ctx *gin.Context) {
	id, err := ids.ParseProductID(ctx.Param("product_id"))
	if err != nil {
		handler.notFound(ctx)
		return
	}
	var body productRequest
	if !decode(ctx, &body) {
		return
	}
	draft, ok := productDraft(ctx, body)
	if !ok {
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	product, err := handler.service.ReplaceProduct(ctx.Request.Context(), resolved.StoreID(), id, body.ExpectedVersion, draft)
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	writeJSON(ctx, http.StatusOK, product)
}

func (handler Handler) archiveProduct(ctx *gin.Context) {
	id, err := ids.ParseProductID(ctx.Param("product_id"))
	if err != nil {
		handler.notFound(ctx)
		return
	}
	var body versionRequest
	if !decode(ctx, &body) {
		return
	}
	resolved, _ := tenant.FromContext(ctx.Request.Context())
	product, err := handler.service.ArchiveProduct(ctx.Request.Context(), resolved.StoreID(), id, body.ExpectedVersion)
	if err != nil {
		handler.writeError(ctx, err)
		return
	}
	writeJSON(ctx, http.StatusOK, product)
}

func productDraft(ctx *gin.Context, body productRequest) (domain.ProductDraft, bool) {
	draft := domain.ProductDraft{Name: body.Name, Slug: body.Slug, Description: body.Description}
	for _, raw := range body.CategoryIDs {
		id, err := ids.ParseCategoryID(raw)
		if err != nil {
			writeProblem(ctx, http.StatusBadRequest, "invalid_catalog", "Catalog input is invalid.")
			return domain.ProductDraft{}, false
		}
		draft.CategoryIDs = append(draft.CategoryIDs, id)
	}
	for _, raw := range body.Variants {
		var id ids.VariantID
		if raw.VariantID != "" {
			parsed, err := ids.ParseVariantID(raw.VariantID)
			if err != nil {
				writeProblem(ctx, http.StatusBadRequest, "invalid_catalog", "Catalog input is invalid.")
				return domain.ProductDraft{}, false
			}
			id = parsed
		}
		draft.Variants = append(draft.Variants, domain.VariantDraft{ID: id, SKU: raw.SKU, Barcode: raw.Barcode, OptionValues: map[string]string(raw.OptionValues), WeightGrams: raw.WeightGrams, Dimensions: domain.Dimensions{LengthMM: raw.Dimensions.LengthMM, WidthMM: raw.Dimensions.WidthMM, HeightMM: raw.Dimensions.HeightMM}})
	}
	for _, raw := range body.Media {
		var id ids.MediaID
		if raw.MediaID != "" {
			parsed, err := ids.ParseMediaID(raw.MediaID)
			if err != nil {
				writeProblem(ctx, http.StatusBadRequest, "invalid_catalog", "Catalog input is invalid.")
				return domain.ProductDraft{}, false
			}
			id = parsed
		}
		draft.Media = append(draft.Media, domain.MediaDraft{ID: id, ObjectKey: raw.ObjectKey, AltText: raw.AltText, Position: raw.Position})
	}
	return draft, true
}

func parseOptionalCategory(ctx *gin.Context, raw *string) (*ids.CategoryID, bool) {
	if raw == nil {
		return nil, true
	}
	id, err := ids.ParseCategoryID(*raw)
	if err != nil {
		writeProblem(ctx, http.StatusBadRequest, "invalid_catalog", "Catalog input is invalid.")
		return nil, false
	}
	return &id, true
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
	case errors.Is(err, domain.ErrInvalidCatalog):
		writeProblem(ctx, http.StatusBadRequest, "invalid_catalog", "Catalog input is invalid.")
	case errors.Is(err, application.ErrNotFound):
		handler.notFound(ctx)
	case errors.Is(err, application.ErrConflict):
		writeProblem(ctx, http.StatusConflict, "catalog_conflict", "Catalog state conflicts with this request.")
	default:
		handler.logger.Error("catalog operation failed", "request_id", httpserver.RequestID(ctx.Request.Context()))
		var networkError net.Error
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) || errors.As(err, &networkError) {
			writeProblem(ctx, http.StatusServiceUnavailable, "dependency_unavailable", "A required service is temporarily unavailable.")
			return
		}
		writeProblem(ctx, http.StatusInternalServerError, "internal_error", "An internal error occurred.")
	}
}

func (handler Handler) invalid(ctx *gin.Context) {
	writeProblem(ctx, http.StatusBadRequest, "invalid_request", "Request is invalid.")
}
func (handler Handler) notFound(ctx *gin.Context) {
	writeProblem(ctx, http.StatusNotFound, "catalog_unavailable", "Catalog resource is unavailable.")
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
func nullableCursor(value string) any {
	if value == "" {
		return nil
	}
	return value
}
