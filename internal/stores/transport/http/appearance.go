package storeshttp

import (
	"errors"
	"net/http"

	"github.com/ah2909/e-commerce/internal/platform/authz"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/problem"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/stores/application"
	"github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/gin-gonic/gin"
)

func (handler Handler) RegisterAppearance(router gin.IRoutes, hosts tenant.HostMiddleware, service application.AppearanceService) {
	read := func(c *gin.Context) {
		store, _ := tenant.StoreFromContext(c.Request.Context())
		a, err := service.Get(c.Request.Context(), store.StoreID)
		if err != nil {
			handler.appearanceError(c, err)
			return
		}
		c.Header("Cache-Control", "private, no-store")
		writeJSON(c.Writer, http.StatusOK, a)
	}
	registerGet(router, "/api/v1/storefront", hosts.TenantGin(), handler.withTimeout(), read)
	chain := func(final gin.HandlerFunc) []gin.HandlerFunc {
		return []gin.HandlerFunc{hosts.TenantGin(), handler.withTimeout(), handler.authenticate, handler.tenant.Gin(), authz.RequireGin(domain.PermissionStoreManage, handler.logger), final}
	}
	router.GET("/api/v1/admin/storefront", chain(read)...)
	router.PUT("/api/v1/admin/storefront", chain(func(c *gin.Context) {
		var body struct {
			DisplayName     string `json:"display_name"`
			Tagline         string `json:"tagline"`
			LogoURL         string `json:"logo_url"`
			HeroURL         string `json:"hero_url"`
			HeroTitle       string `json:"hero_title"`
			HeroText        string `json:"hero_text"`
			Announcement    string `json:"announcement"`
			Preset          string `json:"preset"`
			ExpectedVersion *int64 `json:"expected_version"`
		}
		if !decode(c.Writer, c.Request, &body) {
			return
		}
		if body.ExpectedVersion == nil {
			handler.appearanceError(c, domain.ErrInvalidAppearance)
			return
		}
		t, _ := tenant.FromContext(c.Request.Context())
		a, err := service.Save(c.Request.Context(), t.StoreID(), t.UserID(), domain.Appearance{DisplayName: body.DisplayName, Tagline: body.Tagline, LogoURL: body.LogoURL, HeroURL: body.HeroURL, HeroTitle: body.HeroTitle, HeroText: body.HeroText, Announcement: body.Announcement, Preset: body.Preset, Version: *body.ExpectedVersion}, httpserver.RequestID(c.Request.Context()))
		if err != nil {
			handler.appearanceError(c, err)
			return
		}
		c.Header("Cache-Control", "private, no-store")
		writeJSON(c.Writer, http.StatusOK, a)
	})...)
}

func (handler Handler) appearanceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domain.ErrInvalidAppearance):
		problem.Write(c.Writer, c.Request, 400, "invalid_appearance", "Storefront appearance is invalid.", nil)
	case errors.Is(err, application.ErrAppearanceConflict):
		problem.Write(c.Writer, c.Request, 409, "appearance_conflict", "Storefront appearance has changed. Reload before saving.", nil)
	case errors.Is(err, application.ErrTenantAccessDenied):
		problem.Write(c.Writer, c.Request, 403, "permission_denied", "Permission is denied.", nil)
	case errors.Is(err, application.ErrStoreUnavailable):
		problem.Write(c.Writer, c.Request, 404, "storefront_unavailable", "Storefront is unavailable.", nil)
	default:
		handler.internalError(c.Writer, c.Request, err, "store appearance failed")
	}
}
