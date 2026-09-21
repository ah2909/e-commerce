package identityhttp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ah2909/e-commerce/internal/identity/application"
	identitydomain "github.com/ah2909/e-commerce/internal/identity/domain"
	"github.com/ah2909/e-commerce/internal/platform/authn"
	"github.com/ah2909/e-commerce/internal/platform/clientaddr"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/problem"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/gin-gonic/gin"
)

const maxBody = 16 << 10

type userKey struct{}
type guestKey struct{}

type Limiter interface {
	Allow(context.Context, string, string, int64, time.Duration) (bool, time.Duration, error)
}

type RatePolicy struct {
	Maximum int64
	Window  time.Duration
}

type CredentialRatePolicy struct {
	PeerMaximum     int64
	IdentityMaximum int64
	Window          time.Duration
}

type Settings struct {
	OperationTimeout time.Duration
	TenantBaseDomain string
	SingleStoreHost  string
	AuthHost         string
	Registration     CredentialRatePolicy
	Login            CredentialRatePolicy
	Token            CredentialRatePolicy
	Social           CredentialRatePolicy
	GuestCreation    RatePolicy
}

func DefaultSettings() Settings {
	return Settings{
		OperationTimeout: 2 * time.Second,
		Registration:     CredentialRatePolicy{PeerMaximum: 10, IdentityMaximum: 5, Window: time.Hour},
		Login:            CredentialRatePolicy{PeerMaximum: 20, IdentityMaximum: 5, Window: 15 * time.Minute},
		Token:            CredentialRatePolicy{PeerMaximum: 30, IdentityMaximum: 10, Window: time.Minute},
		Social:           CredentialRatePolicy{PeerMaximum: 20, IdentityMaximum: 10, Window: time.Hour},
		GuestCreation:    RatePolicy{Maximum: 20, Window: time.Hour},
	}
}

type Handler struct {
	service  application.Service
	social   application.SocialService
	limiter  Limiter
	logger   *slog.Logger
	settings Settings
}

func NewHandler(service application.Service, social application.SocialService, limiter Limiter, logger *slog.Logger, settings Settings) Handler {
	return Handler{service: service, social: social, limiter: limiter, logger: logger, settings: settings}
}

func (handler Handler) Register(router gin.IRoutes, hosts tenant.HostMiddleware) {
	tenantMiddleware := hosts.TenantGin()
	timeout := handler.withTimeout()
	userAuth := handler.authenticateUser()
	guestAuth := handler.authenticateGuest()
	router.POST("/api/v1/auth/registrations", tenantMiddleware, timeout, handler.register)
	router.POST("/api/v1/auth/sessions", tenantMiddleware, timeout, handler.login)
	router.POST("/api/v1/auth/sessions/refresh", tenantMiddleware, timeout, handler.refreshUser)
	registerGet(router, "/api/v1/auth/me", tenantMiddleware, timeout, userAuth, handler.me)
	router.DELETE("/api/v1/auth/sessions/current", tenantMiddleware, timeout, handler.logoutUser)
	router.POST("/api/v1/auth/social/:provider/sessions", tenantMiddleware, timeout, handler.startSocial)
	router.POST("/api/v1/auth/social/:provider/links", tenantMiddleware, timeout, userAuth, handler.startSocialLink)
	registerGet(router, "/api/v1/auth/social/continue", hosts.AuthGin(), timeout, handler.continueSocial)
	registerGet(router, "/api/v1/auth/social/:provider/callback", hosts.AuthGin(), timeout, handler.socialCallback)
	router.POST("/api/v1/auth/social/exchanges", tenantMiddleware, timeout, handler.socialExchange)
	router.POST("/api/v1/guest-sessions", tenantMiddleware, timeout, handler.createGuest)
	router.POST("/api/v1/guest-sessions/refresh", tenantMiddleware, timeout, handler.refreshGuest)
	registerGet(router, "/api/v1/guest-sessions/current", tenantMiddleware, timeout, guestAuth, handler.currentGuest)
	router.DELETE("/api/v1/guest-sessions/current", tenantMiddleware, timeout, handler.logoutGuest)
}

func registerGet(router gin.IRoutes, path string, handlers ...gin.HandlerFunc) {
	router.GET(path, handlers...)
	router.HEAD(path, handlers...)
}

func (handler Handler) withTimeout() gin.HandlerFunc {
	return func(request *gin.Context) {
		ctx, cancel := context.WithTimeout(request.Request.Context(), handler.settings.OperationTimeout)
		defer cancel()
		request.Request = request.Request.WithContext(ctx)
		request.Next()
	}
}

func (handler Handler) AuthenticateUser() gin.HandlerFunc    { return handler.authenticateUser() }
func (handler Handler) AuthenticateShopper() gin.HandlerFunc { return handler.authenticateShopper() }

func (handler Handler) authenticateShopper() gin.HandlerFunc {
	return func(request *gin.Context) {
		w, r := request.Writer, request.Request
		store, hasStore := tenant.StoreFromContext(r.Context())
		value, ok := bearer(r)
		if !hasStore || !ok {
			handler.authError(w, r, application.ErrUnauthenticated)
			request.Abort()
			return
		}
		user, sessionID, err := handler.service.AuthenticateUser(r.Context(), store.StoreID, value)
		if err == nil {
			request.Request = r.WithContext(authn.WithPrincipal(r.Context(), authn.NewUserPrincipal(store.StoreID.String(), user.ID.String(), sessionID)))
			request.Next()
			return
		}
		if !errors.Is(err, application.ErrUnauthenticated) {
			handler.authError(w, r, err)
			request.Abort()
			return
		}
		guest, err := handler.service.AuthenticateGuest(r.Context(), store.StoreID, value)
		if err != nil {
			handler.guestError(w, r, err)
			request.Abort()
			return
		}
		request.Request = r.WithContext(authn.WithPrincipal(r.Context(), authn.NewGuestPrincipal(store.StoreID.String(), guest.GuestID.String(), guest.Token.SessionID)))
		request.Next()
	}
}

func (handler Handler) register(ctx *gin.Context) {
	w, r := ctx.Writer, ctx.Request
	store, _ := tenant.StoreFromContext(r.Context())
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	policy := handler.settings.Registration
	if !handler.limitCredential(w, r, "register", store.StoreID.String()+"|"+rateEmail(body.Email), policy.PeerMaximum, policy.IdentityMaximum, policy.Window) {
		return
	}
	result, err := handler.service.Register(r.Context(), store.StoreID, body.Email, body.Password)
	if err != nil {
		if errors.Is(err, application.ErrIdentityExists) {
			problem.Write(w, r, http.StatusConflict, "identity_exists", "Identity already exists.", nil)
			return
		}
		if errors.Is(err, application.ErrInvalidIdentity) {
			problem.Write(w, r, http.StatusBadRequest, "invalid_identity", "Identity input is invalid.", nil)
			return
		}
		if handler.authenticationBusy(w, r, err) {
			return
		}
		handler.internalError(w, r, "registration failed", err)
		return
	}
	writeJSON(w, http.StatusCreated, sessionResponse(result))
}

func (handler Handler) login(ctx *gin.Context) {
	w, r := ctx.Writer, ctx.Request
	store, _ := tenant.StoreFromContext(r.Context())
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	policy := handler.settings.Login
	if !handler.limitCredential(w, r, "login", store.StoreID.String()+"|"+rateEmail(body.Email), policy.PeerMaximum, policy.IdentityMaximum, policy.Window) {
		return
	}
	result, err := handler.service.Login(r.Context(), store.StoreID, body.Email, body.Password)
	if err != nil {
		handler.authError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse(result))
}

func (handler Handler) refreshUser(ctx *gin.Context) {
	w, r := ctx.Writer, ctx.Request
	store, _ := tenant.StoreFromContext(r.Context())
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if !decode(w, r, &body) {
		return
	}
	policy := handler.settings.Token
	if !handler.limitCredential(w, r, "refresh", store.StoreID.String()+"|"+rateSecret(body.RefreshToken), policy.PeerMaximum, policy.IdentityMaximum, policy.Window) {
		return
	}
	pair, err := handler.service.RefreshUser(r.Context(), store.StoreID, body.RefreshToken)
	if err != nil {
		handler.authError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse(pair))
}

func (handler Handler) logoutUser(ctx *gin.Context) {
	w, r := ctx.Writer, ctx.Request
	store, _ := tenant.StoreFromContext(r.Context())
	value, ok := bearer(r)
	if !ok {
		handler.authError(w, r, application.ErrUnauthenticated)
		return
	}
	if err := handler.service.LogoutUser(r.Context(), store.StoreID, value); err != nil {
		handler.authError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (handler Handler) me(ctx *gin.Context) {
	w, r := ctx.Writer, ctx.Request
	user := r.Context().Value(userKey{}).(application.UserRecord)
	var email *string
	if user.Email != nil {
		value := user.Email.Presented()
		email = &value
	}
	writeJSON(w, http.StatusOK, map[string]any{"store_id": user.StoreID.String(), "user_id": user.ID.String(), "email": email, "status": user.Status, "created_at": user.CreatedAt, "updated_at": user.UpdatedAt})
}

func (handler Handler) startSocial(ctx *gin.Context) {
	handler.startSocialFlow(ctx, "login", "", "")
}
func (handler Handler) startSocialLink(ctx *gin.Context) {
	w, r := ctx.Writer, ctx.Request
	principal, _ := authn.FromContext(r.Context())
	userID, err := ids.ParseUserID(principal.UserID())
	if err != nil {
		handler.authError(w, r, application.ErrUnauthenticated)
		return
	}
	handler.startSocialFlow(ctx, "link", userID, principal.SessionID())
}

func (handler Handler) startSocialFlow(ctx *gin.Context, purpose string, userID ids.UserID, sessionID string) {
	w, r := ctx.Writer, ctx.Request
	store, _ := tenant.StoreFromContext(r.Context())
	var body struct {
		ReturnPath string `json:"return_path"`
	}
	if !decode(w, r, &body) {
		return
	}
	policy := handler.settings.Social
	if !handler.limitCredential(w, r, "social-start", store.StoreID.String()+"|"+ctx.Param("provider"), policy.PeerMaximum, policy.IdentityMaximum, policy.Window) {
		return
	}
	provider, err := identitydomain.ParseProvider(ctx.Param("provider"))
	if err != nil {
		problem.Write(w, r, http.StatusNotFound, "provider_unavailable", "Social provider is unavailable.", nil)
		return
	}
	started, err := handler.social.Start(r.Context(), store.StoreID, store.Subdomain.String(), provider, purpose, userID, sessionID, body.ReturnPath)
	if err != nil {
		if errors.Is(err, application.ErrSocialDenied) {
			problem.Write(w, r, http.StatusBadRequest, "social_login_denied", "Social login could not be started.", nil)
			return
		}
		problem.Write(w, r, http.StatusServiceUnavailable, "social_login_unavailable", "Social login is temporarily unavailable.", map[string]any{"retryable": true})
		return
	}
	continuation := "https://" + handler.settings.AuthHost + "/api/v1/auth/social/continue?code=" + url.QueryEscape(started.ContinuationCode)
	writeJSON(w, http.StatusOK, map[string]string{"authorization_url": continuation})
}

func (handler Handler) continueSocial(ctx *gin.Context) {
	w, r := ctx.Writer, ctx.Request
	if len(r.URL.Query()["code"]) != 1 {
		problem.Write(w, r, http.StatusUnauthorized, "social_state_invalid", "Social login state is invalid.", nil)
		return
	}
	continuation, err := handler.social.Continue(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		problem.Write(w, r, http.StatusUnauthorized, "social_state_invalid", "Social login state is invalid.", nil)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "social_binding", Value: continuation.BrowserToken, Path: "/api/v1/auth/social/", MaxAge: 600, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, continuation.AuthorizationURL, http.StatusSeeOther)
}

func (handler Handler) socialCallback(ctx *gin.Context) {
	w, r := ctx.Writer, ctx.Request
	query := r.URL.Query()
	hasCode := len(query["code"]) == 1 && query.Get("code") != ""
	hasError := len(query["error"]) == 1 && query.Get("error") != ""
	if len(query["state"]) != 1 || len(query["code"]) > 1 || len(query["error"]) > 1 || hasCode == hasError {
		problem.Write(w, r, http.StatusUnauthorized, "social_state_invalid", "Social login state is invalid.", nil)
		return
	}
	provider, err := identitydomain.ParseProvider(ctx.Param("provider"))
	cookie, cookieErr := r.Cookie("social_binding")
	if err != nil || cookieErr != nil {
		problem.Write(w, r, http.StatusUnauthorized, "social_state_invalid", "Social login state is invalid.", nil)
		return
	}
	exchange, returnPath, subdomain, err := handler.social.Callback(r.Context(), provider, query.Get("state"), query.Get("code"), cookie.Value)
	if !errors.Is(err, application.ErrSocialState) {
		http.SetCookie(w, &http.Cookie{Name: "social_binding", Path: "/api/v1/auth/social/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	}
	if err != nil {
		if query.Get("error") != "" {
			problem.Write(w, r, http.StatusUnauthorized, "social_login_denied", "Social login was denied.", nil)
			return
		}
		if errors.Is(err, application.ErrSocialState) || errors.Is(err, application.ErrSocialDenied) {
			problem.Write(w, r, http.StatusUnauthorized, "social_login_denied", "Social login could not be completed.", nil)
			return
		}
		if errors.Is(err, application.ErrSocialProviderRejected) {
			problem.Write(w, r, http.StatusUnauthorized, "social_login_rejected", "Social login could not be completed.", map[string]any{"retryable": false, "fresh_start_required": true})
			return
		}
		if errors.Is(err, application.ErrSocialProviderUnavailable) {
			problem.Write(w, r, http.StatusServiceUnavailable, "social_login_unavailable", "Social login is temporarily unavailable.", map[string]any{"retryable": false, "fresh_start_required": true})
			return
		}
		if errors.Is(err, application.ErrSocialAccountLink) {
			problem.Write(w, r, http.StatusConflict, "social_link_required", "Sign in locally and link this social account.", nil)
			return
		}
		if errors.Is(err, application.ErrIdentityExists) {
			problem.Write(w, r, http.StatusConflict, "social_identity_exists", "Social identity is already linked.", nil)
			return
		}
		problem.Write(w, r, http.StatusServiceUnavailable, "social_login_unavailable", "Social login is temporarily unavailable.", map[string]any{"retryable": true})
		return
	}
	target, err := socialRedirectTargetHost(subdomain, handler.settings.TenantBaseDomain, handler.settings.SingleStoreHost, returnPath, exchange)
	if err != nil {
		problem.Write(w, r, http.StatusInternalServerError, "internal_error", "An internal error occurred.", nil)
		return
	}
	w.Header().Set("Location", target)
	w.WriteHeader(http.StatusSeeOther)
}

func (handler Handler) socialExchange(ctx *gin.Context) {
	w, r := ctx.Writer, ctx.Request
	store, _ := tenant.StoreFromContext(r.Context())
	var body struct {
		ExchangeCode string `json:"exchange_code"`
	}
	if !decode(w, r, &body) {
		return
	}
	policy := handler.settings.Token
	if !handler.limitCredential(w, r, "social-exchange", store.StoreID.String()+"|"+rateSecret(body.ExchangeCode), policy.PeerMaximum, policy.IdentityMaximum, policy.Window) {
		return
	}
	result, err := handler.social.Exchange(r.Context(), store.StoreID, body.ExchangeCode)
	if err != nil {
		handler.authError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sessionResponse(result))
}

func (handler Handler) createGuest(ctx *gin.Context) {
	w, r := ctx.Writer, ctx.Request
	store, _ := tenant.StoreFromContext(r.Context())
	storeID := store.StoreID
	if !handler.limitPeerPolicy(w, r, "guest-create", handler.settings.GuestCreation) {
		return
	}
	result, err := handler.service.CreateGuest(r.Context(), storeID)
	if err != nil {
		if errors.Is(err, application.ErrStoreUnavailable) {
			problem.Write(w, r, http.StatusNotFound, "storefront_unavailable", "Storefront is unavailable.", nil)
			return
		}
		handler.internalError(w, r, "guest session creation failed", err)
		return
	}
	response := tokenResponse(result.Token)
	response["guest_id"] = result.GuestID.String()
	response["store_id"] = storeID.String()
	writeJSON(w, http.StatusCreated, response)
}

func (handler Handler) refreshGuest(ctx *gin.Context) {
	w, r := ctx.Writer, ctx.Request
	store, _ := tenant.StoreFromContext(r.Context())
	storeID := store.StoreID
	var body struct {
		RefreshToken string `json:"refresh_token"`
	}
	if !decode(w, r, &body) {
		return
	}
	policy := handler.settings.Token
	if !handler.limitCredential(w, r, "guest-refresh", storeID.String()+"|"+rateSecret(body.RefreshToken), policy.PeerMaximum, policy.IdentityMaximum, policy.Window) {
		return
	}
	pair, err := handler.service.RefreshGuest(r.Context(), storeID, body.RefreshToken)
	if err != nil {
		handler.guestError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse(pair))
}

func (handler Handler) currentGuest(ctx *gin.Context) {
	w, r := ctx.Writer, ctx.Request
	guest := r.Context().Value(guestKey{}).(application.GuestSession)
	writeJSON(w, http.StatusOK, map[string]any{"guest_id": guest.GuestID.String(), "store_id": guest.StoreID.String(), "session_id": guest.Token.SessionID, "access_expires_at": guest.Token.AccessExpiresAt, "session_expires_at": guest.Token.SessionExpiresAt})
}

func (handler Handler) logoutGuest(ctx *gin.Context) {
	w, r := ctx.Writer, ctx.Request
	store, _ := tenant.StoreFromContext(r.Context())
	storeID := store.StoreID
	value, ok := bearer(r)
	if !ok {
		handler.authError(w, r, application.ErrUnauthenticated)
		return
	}
	if err := handler.service.LogoutGuest(r.Context(), storeID, value); err != nil {
		handler.guestError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (handler Handler) authenticateUser() gin.HandlerFunc {
	return func(request *gin.Context) {
		w, r := request.Writer, request.Request
		value, ok := bearer(r)
		if !ok {
			handler.authError(w, r, application.ErrUnauthenticated)
			request.Abort()
			return
		}
		store, hasStore := tenant.StoreFromContext(r.Context())
		if !hasStore {
			handler.authError(w, r, application.ErrUnauthenticated)
			request.Abort()
			return
		}
		user, sessionID, err := handler.service.AuthenticateUser(r.Context(), store.StoreID, value)
		if err != nil {
			handler.authError(w, r, err)
			request.Abort()
			return
		}
		requestContext := context.WithValue(r.Context(), userKey{}, user)
		requestContext = authn.WithPrincipal(requestContext, authn.NewUserPrincipal(store.StoreID.String(), user.ID.String(), sessionID))
		request.Request = r.WithContext(requestContext)
		request.Next()
	}
}

func (handler Handler) authenticateGuest() gin.HandlerFunc {
	return func(request *gin.Context) {
		w, r := request.Writer, request.Request
		store, hasStore := tenant.StoreFromContext(r.Context())
		storeID := store.StoreID
		value, ok := bearer(r)
		if !hasStore || !ok {
			handler.authError(w, r, application.ErrUnauthenticated)
			request.Abort()
			return
		}
		guest, err := handler.service.AuthenticateGuest(r.Context(), storeID, value)
		if err != nil {
			handler.guestError(w, r, err)
			request.Abort()
			return
		}
		requestContext := context.WithValue(r.Context(), guestKey{}, guest)
		requestContext = authn.WithPrincipal(requestContext, authn.NewGuestPrincipal(storeID.String(), guest.GuestID.String(), guest.Token.SessionID))
		request.Request = r.WithContext(requestContext)
		request.Next()
	}
}

func (handler Handler) guestError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, application.ErrStoreUnavailable) {
		problem.Write(w, r, http.StatusNotFound, "storefront_unavailable", "Storefront is unavailable.", nil)
		return
	}
	handler.authError(w, r, err)
}

func (handler Handler) limitPeerPolicy(w http.ResponseWriter, r *http.Request, name string, policy RatePolicy) bool {
	return handler.applyLimit(w, r, name+"-peer", clientaddr.FromRequest(r), policy.Maximum, policy.Window)
}

func (handler Handler) limitCredential(w http.ResponseWriter, r *http.Request, policy, subject string, peerMaximum, subjectMaximum int64, window time.Duration) bool {
	if !handler.applyLimit(w, r, policy+"-peer", clientaddr.FromRequest(r), peerMaximum, window) {
		return false
	}
	return handler.applyLimit(w, r, policy+"-identity", subject, subjectMaximum, window)
}

func (handler Handler) applyLimit(w http.ResponseWriter, r *http.Request, policy, subject string, maximum int64, window time.Duration) bool {
	allowed, retry, err := handler.limiter.Allow(r.Context(), policy, subject, maximum, window)
	if err != nil {
		problem.Write(w, r, http.StatusServiceUnavailable, "rate_limit_unavailable", "Request protection is unavailable.", map[string]any{"retryable": true})
		return false
	}
	if !allowed {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", max(1, int(math.Ceil(retry.Seconds())))))
		problem.Write(w, r, http.StatusTooManyRequests, "rate_limited", "Too many requests.", nil)
		return false
	}
	return true
}

func (handler Handler) authError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, application.ErrRefreshReplay) {
		handler.logger.Warn("session compromised", "request_id", httpserver.RequestID(r.Context()), "reason", "refresh_replay")
	}
	if errors.Is(err, application.ErrUnauthenticated) || errors.Is(err, application.ErrRefreshReplay) {
		problem.Write(w, r, http.StatusUnauthorized, "unauthenticated", "Authentication is required.", nil)
		return
	}
	if handler.authenticationBusy(w, r, err) {
		return
	}
	handler.internalError(w, r, "authentication operation failed", err)
}

func (handler Handler) authenticationBusy(w http.ResponseWriter, r *http.Request, err error) bool {
	if !errors.Is(err, application.ErrAuthenticationBusy) {
		return false
	}
	problem.Write(w, r, http.StatusServiceUnavailable, "authentication_busy", "Authentication is temporarily unavailable.", map[string]any{"retryable": true})
	return true
}

func (handler Handler) internalError(w http.ResponseWriter, r *http.Request, event string, err error) {
	handler.logger.Error(event, "request_id", httpserver.RequestID(r.Context()))
	if dependencyUnavailable(err) {
		problem.Write(w, r, http.StatusServiceUnavailable, "dependency_unavailable", "A required service is temporarily unavailable.", map[string]any{"retryable": true})
		return
	}
	problem.Write(w, r, http.StatusInternalServerError, "internal_error", "An internal error occurred.", nil)
}

func dependencyUnavailable(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}

func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		problem.Write(w, r, http.StatusBadRequest, "invalid_request", "Request body is invalid.", nil)
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		problem.Write(w, r, http.StatusBadRequest, "invalid_request", "Request body is invalid.", nil)
		return false
	}
	return true
}

func bearer(r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return "", false
	}
	parts := strings.Split(values[0], " ")
	returnValue := len(parts) == 2 && parts[0] == "Bearer" && parts[1] != ""
	if !returnValue {
		return "", false
	}
	return parts[1], true
}
func rateEmail(value string) string {
	email, err := identitydomain.ParseEmail(value)
	if err == nil {
		return email.Canonical()
	}
	return strings.ToLower(strings.TrimSpace(value))
}
func rateSecret(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
func socialRedirectTarget(subdomain, baseDomain, returnPath, exchange string) (string, error) {
	return socialRedirectTargetHost(subdomain, baseDomain, "", returnPath, exchange)
}

func socialRedirectTargetHost(subdomain, baseDomain, exactHost, returnPath, exchange string) (string, error) {
	host, ok := tenantSubdomainTarget(subdomain, baseDomain)
	if exactHost != "" {
		parsed, err := url.Parse("https://" + exactHost)
		if err != nil || parsed.Hostname() != exactHost || parsed.Port() != "" || strings.ContainsAny(exactHost, "/:@?#") {
			return "", errors.New("invalid social redirect host")
		}
		host, ok = exactHost, true
	}
	if !ok {
		return "", errors.New("invalid social redirect host")
	}
	parsed, err := url.Parse(returnPath)
	if err != nil || !strings.HasPrefix(returnPath, "/") || strings.HasPrefix(returnPath, "//") || parsed.IsAbs() || parsed.Host != "" || parsed.Fragment != "" {
		return "", errors.New("invalid social return path")
	}
	target := &url.URL{Scheme: "https", Host: host, Path: parsed.Path, RawPath: parsed.RawPath, RawQuery: parsed.RawQuery}
	query := target.Query()
	if exchange == "" {
		query.Set("linked", "true")
	} else {
		query.Set("exchange_code", exchange)
	}
	target.RawQuery = query.Encode()
	return target.String(), nil
}
func tenantSubdomainTarget(subdomain, baseDomain string) (string, bool) {
	parsedSubdomain, err := storesdomain.ParseSubdomain(subdomain)
	if err != nil || parsedSubdomain.String() != subdomain || baseDomain == "" || strings.ContainsAny(baseDomain, "/:@?#") {
		return "", false
	}
	target := subdomain + "." + baseDomain
	parsed, err := url.Parse("https://" + target)
	return target, err == nil && parsed.Hostname() == target && parsed.Port() == ""
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func tokenResponse(pair application.TokenPair) map[string]any {
	return map[string]any{"access_token": pair.AccessToken, "refresh_token": pair.RefreshToken, "token_type": "Bearer", "access_expires_at": pair.AccessExpiresAt, "session_expires_at": pair.SessionExpiresAt, "session_id": pair.SessionID}
}
func sessionResponse(result application.UserSession) map[string]any {
	response := tokenResponse(result.Token)
	response["store_id"] = result.User.StoreID.String()
	response["user_id"] = result.User.ID.String()
	return response
}
