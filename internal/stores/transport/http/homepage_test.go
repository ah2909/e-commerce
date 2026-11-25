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

type homepageHosts map[string]ids.StoreID

func (h homepageHosts) Execute(_ context.Context, subdomain string) (application.StoreRecord, error) {
	store, ok := h[subdomain]
	if !ok {
		return application.StoreRecord{}, application.ErrStoreUnavailable
	}
	return application.StoreRecord{StoreID: store, Status: domain.StoreStatusActive}, nil
}

type homepageRepository struct {
	states map[ids.StoreID]domain.HomepageState
	err    error
	calls  int
}

func (r *homepageRepository) GetPublishedHomepage(_ context.Context, store ids.StoreID) (domain.Homepage, error) {
	r.calls++
	return r.states[store].Published, r.err
}

func (r *homepageRepository) GetHomepage(_ context.Context, store ids.StoreID) (domain.HomepageState, error) {
	r.calls++
	return r.states[store], r.err
}

func (r *homepageRepository) SaveHomepageDraft(_ context.Context, store ids.StoreID, _ ids.UserID, draft domain.Homepage, expected int64, _ string) (domain.HomepageState, error) {
	r.calls++
	state := r.states[store]
	if state.Version != expected {
		return domain.HomepageState{}, application.ErrHomepageConflict
	}
	state.Draft = draft
	state.Version++
	return state, r.err
}

func (r *homepageRepository) PublishHomepage(_ context.Context, store ids.StoreID, _ ids.UserID, expected int64, _ string) (domain.HomepageState, error) {
	r.calls++
	state := r.states[store]
	if state.Version != expected {
		return domain.HomepageState{}, application.ErrHomepageConflict
	}
	state.Published = state.Draft
	state.Version++
	return state, r.err
}

func TestHomepageHTTPPermissionScopeAndProjection(t *testing.T) {
	store, _ := ids.NewStoreID()
	other, _ := ids.NewStoreID()
	user, _ := ids.NewUserID()
	owner := authn.NewUserPrincipal(store.String(), user.String(), "session")
	cross := authn.NewUserPrincipal(other.String(), user.String(), "session")
	private := domain.Homepage{SchemaVersion: 1, Mode: "custom", Sections: []domain.HomepageSection{{ID: "private-hero", Type: "hero", Layout: new("split")}}}
	otherLayout := domain.Homepage{SchemaVersion: 1, Mode: "custom", Sections: []domain.HomepageSection{{ID: "other-hero", Type: "hero", Layout: new("stacked")}}}
	initial := domain.DefaultHomepage()
	const admin = "/api/v1/admin/storefront/homepage"
	const public = "/api/v1/storefront/homepage"
	const save = `{"expected_version":1,"draft":{"schema_version":1,"mode":"default"}}`
	const publish = `{"expected_version":1}`
	type testCase struct {
		name, method, path, host, body string
		role                           domain.Role
		principal                      authn.Principal
		member                         bool
		want                           int
	}
	cases := []testCase{
		{"public", "GET", public, "shop", "", "", authn.Principal{}, false, 200},
		{"other public", "GET", public, "other", "", "", authn.Principal{}, false, 200},
		{"owner read", "GET", admin, "shop", "", domain.RoleOwner, owner, true, 200},
		{"owner save", "PUT", admin, "shop", save, domain.RoleOwner, owner, true, 200},
		{"admin publish", "POST", admin + "/publish", "shop", publish, domain.RoleAdmin, owner, true, 200},
		{"missing version", "POST", admin + "/publish", "shop", `{}`, domain.RoleOwner, owner, true, 400},
		{"stale publish", "POST", admin + "/publish", "shop", `{"expected_version":0}`, domain.RoleOwner, owner, true, 409},
		{"stale draft", "PUT", admin, "shop", strings.Replace(save, ":1", ":0", 1), domain.RoleOwner, owner, true, 409},
		{"tenant selector", "PUT", admin, "shop", `{"expected_version":1,"store_id":"other"}`, domain.RoleOwner, owner, true, 400},
		{"unknown section option", "PUT", admin, "shop", `{"expected_version":1,"draft":{"schema_version":1,"mode":"custom","sections":[{"type":"hero","id":"hero","layout":"split","html":"x"}]}}`, domain.RoleOwner, owner, true, 400},
		{"null section option", "PUT", admin, "shop", `{"expected_version":1,"draft":{"schema_version":1,"mode":"custom","sections":[{"type":"hero","id":"hero","layout":"split","image_url":null}]}}`, domain.RoleOwner, owner, true, 400},
		{"overweight request", "PUT", admin, "shop", save + strings.Repeat(" ", 64<<10), domain.RoleOwner, owner, true, 400},
	}
	for _, endpoint := range []struct{ method, path, body string }{{"GET", admin, ""}, {"PUT", admin, save}, {"POST", admin + "/publish", publish}} {
		for _, actor := range []struct {
			name      string
			role      domain.Role
			principal authn.Principal
			member    bool
			want      int
		}{
			{"cross-store", domain.RoleOwner, cross, true, 401},
			{"staff", domain.RoleStaff, owner, true, 403},
			{"shopper", "", owner, false, 403},
			{"anonymous", "", authn.Principal{}, false, 401},
		} {
			cases = append(cases, testCase{actor.name + " " + endpoint.method, endpoint.method, endpoint.path, "shop", endpoint.body, actor.role, actor.principal, actor.member, actor.want})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			logger := slog.New(slog.DiscardHandler)
			engine := httpserver.NewEngine(logger)
			repo := &homepageRepository{states: map[ids.StoreID]domain.HomepageState{
				store: {Draft: private, Published: initial, Version: 1},
				other: {Draft: otherLayout, Published: otherLayout, Version: 2},
			}}
			auth := func(c *gin.Context) {
				c.Request = c.Request.WithContext(authn.WithPrincipal(c.Request.Context(), tc.principal))
			}
			middleware := tenant.NewMiddleware(application.NewResolveTenant(appearanceTenant{role: tc.role, member: tc.member}), logger, time.Second)
			handler := NewHandler(application.MembershipManager{}, auth, middleware, logger, time.Second)
			handler.RegisterHomepage(engine, tenant.NewHostMiddleware(homepageHosts{"shop": store, "other": other}, "example.test", "auth.example.test", logger, time.Second), application.NewHomepageService(repo))
			req := httptest.NewRequestWithContext(t.Context(), tc.method, "http://"+tc.host+".example.test"+tc.path, strings.NewReader(tc.body))
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, req)
			if response.Code != tc.want {
				t.Fatalf("got %d want %d: %s", response.Code, tc.want, response.Body.String())
			}
			if tc.want != 200 && tc.want != 409 && repo.calls != 0 {
				t.Fatal("denied request reached repository")
			}
			if tc.path != public {
				return
			}
			var data map[string]json.RawMessage
			if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			if data["draft"] != nil || data["version"] != nil || strings.Contains(response.Body.String(), "private-hero") {
				t.Fatalf("private state leaked: %s", response.Body.String())
			}
			if (tc.host == "other") != strings.Contains(response.Body.String(), "other-hero") {
				t.Fatalf("wrong tenant layout: %s", response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal("missing cache control")
			}
		})
	}
}
