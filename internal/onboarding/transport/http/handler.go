package onboardinghttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"time"

	identityapplication "github.com/ah2909/e-commerce/internal/identity/application"
	onboardingapplication "github.com/ah2909/e-commerce/internal/onboarding/application"
	"github.com/ah2909/e-commerce/internal/platform/authn"
	"github.com/ah2909/e-commerce/internal/platform/clientaddr"
	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/platform/problem"
	"github.com/ah2909/e-commerce/internal/platform/tenant"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
)

const maxBody = 16 << 10

type Limiter interface {
	Allow(context.Context, string, string, int64, time.Duration) (bool, time.Duration, error)
}

type Handler struct {
	service onboardingapplication.Service
	limiter Limiter
	logger  *slog.Logger
	timeout time.Duration
	maximum int64
	window  time.Duration
}

func NewHandler(service onboardingapplication.Service, limiter Limiter, logger *slog.Logger, timeout time.Duration, maximum int64, window time.Duration) Handler {
	return Handler{service: service, limiter: limiter, logger: logger, timeout: timeout, maximum: maximum, window: window}
}

func (handler Handler) Register(mux *http.ServeMux, hosts tenant.HostMiddleware, authenticate func(http.Handler) http.Handler) {
	mux.Handle("POST /api/v1/onboarding/stores", hosts.Base(authenticate(handler.withTimeout(http.HandlerFunc(handler.create)))))
}

func (handler Handler) withTimeout(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), handler.timeout)
		defer cancel()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (handler Handler) create(w http.ResponseWriter, r *http.Request) {
	allowed, retry, err := handler.limiter.Allow(r.Context(), "onboarding-peer", clientaddr.FromRequest(r), handler.maximum, handler.window)
	if err != nil {
		problem.Write(w, r, http.StatusServiceUnavailable, "rate_limit_unavailable", "Request protection is unavailable.", map[string]any{"retryable": true})
		return
	}
	if !allowed {
		w.Header().Set("Retry-After", fmt.Sprintf("%d", max(1, int(math.Ceil(retry.Seconds())))))
		problem.Write(w, r, http.StatusTooManyRequests, "rate_limited", "Too many requests.", nil)
		return
	}
	var body struct {
		Subdomain       string `json:"subdomain"`
		StoreName       string `json:"store_name"`
		DefaultCurrency string `json:"default_currency"`
		Locale          string `json:"locale"`
		Timezone        string `json:"timezone"`
		Email           string `json:"email"`
		Password        string `json:"password"`
	}
	if !decode(w, r, &body) {
		return
	}
	principal, ok := authn.FromContext(r.Context())
	if !ok || principal.Kind() != authn.KindPlatformAdmin {
		problem.Write(w, r, http.StatusUnauthorized, "platform_unauthenticated", "Platform authentication is required.", nil)
		return
	}
	result, err := handler.service.Execute(r.Context(), onboardingapplication.Command{Subdomain: body.Subdomain, StoreName: body.StoreName, DefaultCurrency: body.DefaultCurrency, Locale: body.Locale, Timezone: body.Timezone, Email: body.Email, Password: body.Password, RequestID: httpserver.RequestID(r.Context()), PlatformActorID: principal.PlatformID()})
	if err != nil {
		switch {
		case errors.Is(err, onboardingapplication.ErrOnboardingConflict):
			problem.Write(w, r, http.StatusConflict, "onboarding_conflict", "Store onboarding could not be completed.", nil)
		case errors.Is(err, storesdomain.ErrInvalidStore), errors.Is(err, identityapplication.ErrInvalidIdentity):
			problem.Write(w, r, http.StatusBadRequest, "invalid_onboarding", "Onboarding input is invalid.", nil)
		case errors.Is(err, identityapplication.ErrAuthenticationBusy), dependencyUnavailable(err):
			problem.Write(w, r, http.StatusServiceUnavailable, "onboarding_unavailable", "Store onboarding is temporarily unavailable.", map[string]any{"retryable": true})
		default:
			handler.logger.Error("store onboarding failed", "request_id", httpserver.RequestID(r.Context()))
			problem.Write(w, r, http.StatusInternalServerError, "internal_error", "An internal error occurred.", nil)
		}
		return
	}
	response := map[string]any{
		"store_id": result.Store.ID().String(), "subdomain": result.Store.Subdomain().String(), "owner_user_id": result.OwnerUserID.String(),
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(response)
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
