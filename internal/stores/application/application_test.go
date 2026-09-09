package application

import (
	"context"
	"errors"
	"testing"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/stores/domain"
)

const (
	storeIDText = "018f0f7d-7b3d-7d2e-8f4f-3b86f31f2741"
	userIDText  = "028f0f7d-7b3d-7d2e-8f4f-3b86f31f2742"
)

type repositoryStub struct {
	resolvedRecord TenantRecord
	found          bool
	resolveErr     error
}

func (r *repositoryStub) ResolveTenant(context.Context, ids.StoreID, ids.UserID) (TenantRecord, bool, error) {
	return r.resolvedRecord, r.found, r.resolveErr
}

func TestResolveTenant(t *testing.T) {
	storeID, _ := ids.ParseStoreID(storeIDText)
	userID, _ := ids.ParseUserID(userIDText)
	repository := &repositoryStub{resolvedRecord: TenantRecord{StoreID: storeID, UserID: userID, StoreName: "Cửa hàng", StoreStatus: domain.StoreStatusSuspended, MembershipStatus: domain.MembershipStatusActive, Role: domain.RoleOwner, DefaultCurrency: "VND", Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh", SettingsVersion: 3}, found: true}

	resolved, err := NewResolveTenant(repository).Execute(context.Background(), storeID, userID)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if resolved.StoreID() != storeID || resolved.UserID() != userID || resolved.StoreStatus() != domain.StoreStatusSuspended || resolved.SettingsVersion() != 3 {
		t.Fatalf("resolved context = %#v", resolved)
	}
}

func TestResolveTenantNormalizesDeniedOutcomes(t *testing.T) {
	storeID, _ := ids.ParseStoreID(storeIDText)
	userID, _ := ids.ParseUserID(userIDText)
	tests := []repositoryStub{{found: false}, {resolvedRecord: TenantRecord{StoreID: storeID, UserID: userID, StoreName: "Cửa hàng", StoreStatus: domain.StoreStatusActive, MembershipStatus: domain.MembershipStatusInactive, DefaultCurrency: "VND", Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh", SettingsVersion: 1}, found: true}}
	for index := range tests {
		_, err := NewResolveTenant(&tests[index]).Execute(context.Background(), storeID, userID)
		if !errors.Is(err, ErrTenantAccessDenied) {
			t.Fatalf("case %d error = %v", index, err)
		}
	}
}

func TestResolveTenantRejectsMismatchedRepositoryScope(t *testing.T) {
	storeID, _ := ids.ParseStoreID(storeIDText)
	otherStoreID, _ := ids.ParseStoreID("118f0f7d-7b3d-7d2e-8f4f-3b86f31f2741")
	userID, _ := ids.ParseUserID(userIDText)
	_, err := NewResolveTenant(&repositoryStub{resolvedRecord: TenantRecord{StoreID: otherStoreID, UserID: userID, StoreName: "Other", StoreStatus: domain.StoreStatusActive, MembershipStatus: domain.MembershipStatusActive, Role: domain.RoleOwner, DefaultCurrency: "VND", Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh", SettingsVersion: 1}, found: true}).Execute(context.Background(), storeID, userID)
	if err == nil || errors.Is(err, ErrTenantAccessDenied) {
		t.Fatalf("Execute() error = %v", err)
	}
}

type storeResolverRepositoryStub struct {
	record StoreRecord
	found  bool
}

func (stub storeResolverRepositoryStub) ResolveStoreBySubdomain(context.Context, domain.Subdomain) (StoreRecord, bool, error) {
	return stub.record, stub.found, nil
}

func TestResolveStoreRequiresActiveMatchingSubdomain(t *testing.T) {
	storeID, _ := ids.ParseStoreID(storeIDText)
	subdomain, _ := domain.ParseSubdomain("shop")
	active := StoreRecord{StoreID: storeID, Subdomain: subdomain, Status: domain.StoreStatusActive}
	resolved, err := NewResolveStore(storeResolverRepositoryStub{record: active, found: true}).Execute(context.Background(), "SHOP")
	if err != nil || resolved.StoreID != storeID {
		t.Fatalf("Execute() = %#v, %v", resolved, err)
	}
	other, _ := domain.ParseSubdomain("other")
	for _, stub := range []storeResolverRepositoryStub{{found: false}, {record: StoreRecord{StoreID: storeID, Subdomain: subdomain, Status: domain.StoreStatusSuspended}, found: true}, {record: StoreRecord{StoreID: storeID, Subdomain: other, Status: domain.StoreStatusActive}, found: true}} {
		if _, err := NewResolveStore(stub).Execute(context.Background(), "shop"); err == nil {
			t.Fatal("invalid store resolution succeeded")
		}
	}
}
