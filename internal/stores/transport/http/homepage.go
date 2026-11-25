package storeshttp

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"unicode/utf8"

	"github.com/ah2909/e-commerce/internal/platform/authz"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/problem"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/stores/application"
	"github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/gin-gonic/gin"
)

func (handler Handler) RegisterHomepage(router gin.IRouter, hosts tenant.HostMiddleware, service application.HomepageService) {
	registerGet(router, "/api/v1/storefront/homepage", hosts.TenantGin(), handler.withTimeout(), func(c *gin.Context) {
		store, _ := tenant.StoreFromContext(c.Request.Context())
		homepage, err := service.Published(c.Request.Context(), store.StoreID)
		handler.homepageResponse(c, homepage, err)
	})

	admin := router.Group("/api/v1/admin/storefront/homepage", hosts.TenantGin(), handler.withTimeout(), handler.authenticate, handler.tenant.Gin(), authz.RequireGin(domain.PermissionStoreManage, handler.logger))
	admin.GET("", func(c *gin.Context) {
		ctx := c.Request.Context()
		t, _ := tenant.FromContext(ctx)
		state, err := service.Get(ctx, t.StoreID())
		handler.homepageResponse(c, state, err)
	})
	admin.PUT("", func(c *gin.Context) {
		var body struct {
			ExpectedVersion *int64          `json:"expected_version"`
			Draft           json.RawMessage `json:"draft"`
		}
		if !decodeHomepageRequest(c, &body, 64<<10) {
			return
		}
		var draft domain.Homepage
		if body.ExpectedVersion == nil || !decodeHomepageDraft(body.Draft, &draft) {
			handler.homepageResponse(c, nil, domain.ErrInvalidHomepage)
			return
		}
		ctx := c.Request.Context()
		t, _ := tenant.FromContext(ctx)
		state, err := service.SaveDraft(ctx, t.StoreID(), t.UserID(), draft, *body.ExpectedVersion, httpserver.RequestID(ctx))
		handler.homepageResponse(c, state, err)
	})
	admin.POST("/publish", func(c *gin.Context) {
		var body struct {
			ExpectedVersion *int64 `json:"expected_version"`
		}
		if !decodeHomepageRequest(c, &body, maxBody) {
			return
		}
		if body.ExpectedVersion == nil {
			handler.homepageResponse(c, nil, domain.ErrInvalidHomepage)
			return
		}
		ctx := c.Request.Context()
		t, _ := tenant.FromContext(ctx)
		state, err := service.Publish(ctx, t.StoreID(), t.UserID(), *body.ExpectedVersion, httpserver.RequestID(ctx))
		handler.homepageResponse(c, state, err)
	})
}

func decodeHomepageRequest(c *gin.Context, target any, limit int64) bool {
	body := &io.LimitedReader{R: c.Request.Body, N: limit + 1}
	if !decodeJSON(body, target) || body.N == 0 {
		problem.Write(c.Writer, c.Request, http.StatusBadRequest, "invalid_request", "Request body is invalid.", nil)
		return false
	}
	return true
}

func decodeHomepageDraft(content json.RawMessage, draft *domain.Homepage) bool {
	if !utf8.Valid(content) || !decodeJSON(bytes.NewReader(content), draft) {
		return false
	}
	// encoding/json can otherwise treat explicit null settings as omitted.
	decoder := json.NewDecoder(bytes.NewReader(content))
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return true
		}
		if err != nil || token == nil {
			return false
		}
	}
}

func (handler Handler) homepageResponse(c *gin.Context, result any, err error) {
	c.Header("Cache-Control", "private, no-store")
	switch {
	case err == nil:
		writeJSON(c.Writer, http.StatusOK, result)
	case errors.Is(err, domain.ErrInvalidHomepage):
		problem.Write(c.Writer, c.Request, 400, "invalid_homepage", "Storefront homepage is invalid.", nil)
	case errors.Is(err, application.ErrHomepageConflict):
		problem.Write(c.Writer, c.Request, 409, "homepage_conflict", "The homepage has changed or has no saved draft.", nil)
	case errors.Is(err, application.ErrTenantAccessDenied):
		problem.Write(c.Writer, c.Request, 403, "permission_denied", "Permission is denied.", nil)
	case errors.Is(err, application.ErrStoreUnavailable):
		problem.Write(c.Writer, c.Request, 404, "storefront_unavailable", "Storefront is unavailable.", nil)
	default:
		handler.internalError(c.Writer, c.Request, err, "store homepage failed")
	}
}
