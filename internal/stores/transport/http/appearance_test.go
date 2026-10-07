package storeshttp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/platform/authn"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/stores/application"
	"github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/gin-gonic/gin"
)

type appearanceHost struct{ store ids.StoreID }

func (s appearanceHost) Execute(_ context.Context, subdomain string) (application.StoreRecord, error) {
	if subdomain != "shop" {
		return application.StoreRecord{}, application.ErrStoreUnavailable
	}
	return application.StoreRecord{StoreID: s.store, Name: "Shop", Status: domain.StoreStatusActive}, nil
}

type appearanceTenant struct {
	role   domain.Role
	member bool
}

func (s appearanceTenant) ResolveTenant(_ context.Context, store ids.StoreID, user ids.UserID) (application.TenantRecord, bool, error) {
	return application.TenantRecord{StoreID: store, UserID: user, StoreName: "Shop", StoreStatus: domain.StoreStatusActive, MembershipStatus: domain.MembershipStatusActive, Role: s.role, SettingsVersion: 1}, s.member, nil
}

type appearanceRepo struct {
	calls int
	store ids.StoreID
}

func (s *appearanceRepo) GetAppearance(_ context.Context, store ids.StoreID) (domain.Appearance, error) {
	s.calls++
	s.store = store
	return domain.DefaultAppearance("Shop"), nil
}
func (s *appearanceRepo) SaveAppearance(_ context.Context, store ids.StoreID, _ ids.UserID, a domain.Appearance, _ string) (domain.Appearance, error) {
	s.calls++
	s.store = store
	a.Version++
	return a, nil
}
func TestAppearanceHostAuthorizationAndValidation(t *testing.T) {
	store, _ := ids.NewStoreID()
	other, _ := ids.NewStoreID()
	user, _ := ids.NewUserID()
	content := `{"display_name":"Shop","hero_title":"Hello","preset":"home","expected_version":0}`
	for _, tc := range []struct {
		name, method, path, host, body string
		role                           domain.Role
		principal                      authn.Principal
		member                         bool
		want                           int
	}{
		{"public", "GET", "/api/v1/storefront", "shop.example.test", "", "", authn.Principal{}, false, 200},
		{"unknown host", "GET", "/api/v1/storefront", "unknown.example.test", "", "", authn.Principal{}, false, 404},
		{"owner", "PUT", "/api/v1/admin/storefront", "shop.example.test", content, domain.RoleOwner, authn.NewUserPrincipal(store.String(), user.String(), "s"), true, 200},
		{"admin", "PUT", "/api/v1/admin/storefront", "shop.example.test", content, domain.RoleAdmin, authn.NewUserPrincipal(store.String(), user.String(), "s"), true, 200},
		{"staff", "PUT", "/api/v1/admin/storefront", "shop.example.test", content, domain.RoleStaff, authn.NewUserPrincipal(store.String(), user.String(), "s"), true, 403},
		{"customer", "PUT", "/api/v1/admin/storefront", "shop.example.test", content, domain.RoleOwner, authn.NewUserPrincipal(store.String(), user.String(), "s"), false, 403},
		{"anonymous", "PUT", "/api/v1/admin/storefront", "shop.example.test", content, "", authn.Principal{}, false, 401},
		{"cross store", "PUT", "/api/v1/admin/storefront", "shop.example.test", content, domain.RoleOwner, authn.NewUserPrincipal(other.String(), user.String(), "s"), true, 401},
		{"missing version", "PUT", "/api/v1/admin/storefront", "shop.example.test", `{"display_name":"Shop","hero_title":"Hello","preset":"home"}`, domain.RoleOwner, authn.NewUserPrincipal(store.String(), user.String(), "s"), true, 400},
		{"tenant selector", "PUT", "/api/v1/admin/storefront", "shop.example.test", `{"store_id":"other"}`, domain.RoleOwner, authn.NewUserPrincipal(store.String(), user.String(), "s"), true, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logger := slog.New(slog.DiscardHandler)
			engine := httpserver.NewEngine(logger)
			repo := &appearanceRepo{}
			middleware := tenant.NewMiddleware(application.NewResolveTenant(appearanceTenant{role: tc.role, member: tc.member}), logger, time.Second)
			authenticate := func(c *gin.Context) {
				c.Request = c.Request.WithContext(authn.WithPrincipal(c.Request.Context(), tc.principal))
			}
			h := NewHandler(application.MembershipManager{}, authenticate, middleware, logger, time.Second)
			h.RegisterAppearance(engine, tenant.NewHostMiddleware(appearanceHost{store: store}, "example.test", "auth.example.test", logger, time.Second), application.NewAppearanceService(repo))
			req := httptest.NewRequestWithContext(context.Background(), tc.method, "http://"+tc.host+tc.path, strings.NewReader(tc.body))
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, req)
			if response.Code != tc.want {
				t.Fatalf("status %d want %d: %s", response.Code, tc.want, response.Body.String())
			}
			if tc.want == 200 {
				if repo.store != store {
					t.Fatal("incorrect scope")
				}
				var data map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
					t.Fatal(err)
				}
				if _, ok := data["store_id"]; ok {
					t.Fatal("private field exposed")
				}
			} else if repo.calls != 0 {
				t.Fatal("unauthorized/invalid request reached repository")
			}
		})
	}
}
