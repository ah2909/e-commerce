package storeshttp

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/platform/httpserver"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/stores/application"
	"github.com/ah2909/e-commerce/internal/stores/domain"
)

type membershipRepositorySpy struct{ createCalls int }

func (spy *membershipRepositorySpy) ListMemberships(context.Context, ids.StoreID) ([]domain.Membership, error) {
	return nil, nil
}

func (spy *membershipRepositorySpy) CreateMembership(context.Context, domain.Membership, ids.UserID, string) error {
	spy.createCalls++
	return nil
}

func (spy *membershipRepositorySpy) UpdateMembership(context.Context, ids.StoreID, ids.UserID, domain.Role, domain.MembershipStatus, ids.UserID, string, time.Time) (domain.Membership, error) {
	return domain.Membership{}, nil
}

type membershipDirectorySpy struct{ calls int }

func (spy *membershipDirectorySpy) IsActiveUser(context.Context, ids.StoreID, ids.UserID) (bool, error) {
	spy.calls++
	return true, nil
}

func TestUpdateMembershipRejectsMalformedUserID(t *testing.T) {
	handler := Handler{}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPatch, "/api/v1/memberships/not-a-uuid", strings.NewReader(`{"role":"staff","status":"active"}`))
	request.SetPathValue("user_id", "not-a-uuid")
	response := httptest.NewRecorder()
	handler.updateMembership(response, request)
	if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"membership_unavailable"`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestAddMembershipRejectsMalformedUserIDWithoutDependencies(t *testing.T) {
	repository := &membershipRepositorySpy{}
	directory := &membershipDirectorySpy{}
	manager := application.NewMembershipManager(repository, directory, time.Now)
	handler := Handler{memberships: manager, logger: slog.New(slog.DiscardHandler)}
	wrapped := httpserver.Middleware(handler.logger, http.HandlerFunc(handler.addMembership))
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/memberships", strings.NewReader(`{"user_id":"not-a-uuid","role":"staff"}`))
	response := httptest.NewRecorder()
	wrapped.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"membership_unavailable"`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if directory.calls != 0 || repository.createCalls != 0 {
		t.Fatalf("dependency calls = directory %d, repository %d", directory.calls, repository.createCalls)
	}
}
