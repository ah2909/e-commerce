package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/stores/domain"
)

const (
	storeIDText = "018f0f7d-7b3d-7d2e-8f4f-3b86f31f2741"
	userIDText  = "028f0f7d-7b3d-7d2e-8f4f-3b86f31f2742"
)

type repositoryStub struct {
	createdStore      domain.Store
	createdMembership domain.Membership
	createErr         error
	resolvedRecord    TenantRecord
	found             bool
	resolveErr        error
}

func (r *repositoryStub) CreateWithMembership(_ context.Context, store domain.Store, membership domain.Membership) error {
	r.createdStore = store
	r.createdMembership = membership
	return r.createErr
}

func (r *repositoryStub) ResolveTenant(context.Context, domain.StoreID, domain.UserID) (TenantRecord, bool, error) {
	return r.resolvedRecord, r.found, r.resolveErr
}

func TestCreateStoreAppliesVietnamDefaults(t *testing.T) {
	storeID, _ := domain.ParseStoreID(storeIDText)
	userID, _ := domain.ParseUserID(userIDText)
	now := time.Date(2026, 9, 7, 3, 0, 0, 0, time.UTC)
	repository := &repositoryStub{}
	useCase := NewCreateStore(repository, func() (domain.StoreID, error) { return storeID, nil }, func() time.Time { return now })

	store, err := useCase.Execute(context.Background(), CreateStoreCommand{CreatorID: userID, Name: "Cửa hàng Ánh Dương"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if store.DefaultCurrency() != domain.DefaultCurrency || store.Locale() != domain.DefaultLocale || store.Timezone() != domain.DefaultTimezone {
		t.Fatalf("defaults = %s %s %s", store.DefaultCurrency(), store.Locale(), store.Timezone())
	}
	if repository.createdStore.ID() != storeID || repository.createdMembership.StoreID() != storeID || repository.createdMembership.UserID() != userID {
		t.Fatal("store and creator membership were not persisted together")
	}
}

func TestCreateStoreDoesNotPersistInvalidInput(t *testing.T) {
	storeID, _ := domain.ParseStoreID(storeIDText)
	userID, _ := domain.ParseUserID(userIDText)
	repository := &repositoryStub{}
	useCase := NewCreateStore(repository, func() (domain.StoreID, error) { return storeID, nil }, time.Now)
	_, err := useCase.Execute(context.Background(), CreateStoreCommand{CreatorID: userID, Name: " "})
	if !errors.Is(err, domain.ErrInvalidStore) || repository.createdStore.ID() != "" {
		t.Fatalf("Execute() error = %v, persisted ID = %q", err, repository.createdStore.ID())
	}
}

func TestCreateStoreWrapsPersistenceFailure(t *testing.T) {
	storeID, _ := domain.ParseStoreID(storeIDText)
	userID, _ := domain.ParseUserID(userIDText)
	persistenceErr := errors.New("database unavailable")
	repository := &repositoryStub{createErr: persistenceErr}
	useCase := NewCreateStore(repository, func() (domain.StoreID, error) { return storeID, nil }, time.Now)
	_, err := useCase.Execute(context.Background(), CreateStoreCommand{CreatorID: userID, Name: "Cửa hàng"})
	if !errors.Is(err, persistenceErr) {
		t.Fatalf("Execute() error = %v", err)
	}
}

func TestResolveTenant(t *testing.T) {
	storeID, _ := domain.ParseStoreID(storeIDText)
	userID, _ := domain.ParseUserID(userIDText)
	repository := &repositoryStub{resolvedRecord: TenantRecord{StoreID: storeID, UserID: userID, StoreName: "Cửa hàng", StoreStatus: domain.StoreStatusSuspended, MembershipStatus: domain.MembershipStatusActive, DefaultCurrency: "VND", Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh", SettingsVersion: 3}, found: true}

	resolved, err := NewResolveTenant(repository).Execute(context.Background(), storeID, userID)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if resolved.StoreID() != storeID || resolved.UserID() != userID || resolved.StoreStatus() != domain.StoreStatusSuspended || resolved.SettingsVersion() != 3 {
		t.Fatalf("resolved context = %#v", resolved)
	}
}

func TestResolveTenantNormalizesDeniedOutcomes(t *testing.T) {
	storeID, _ := domain.ParseStoreID(storeIDText)
	userID, _ := domain.ParseUserID(userIDText)
	tests := []repositoryStub{{found: false}, {resolvedRecord: TenantRecord{StoreID: storeID, UserID: userID, StoreName: "Cửa hàng", StoreStatus: domain.StoreStatusActive, MembershipStatus: domain.MembershipStatusInactive, DefaultCurrency: "VND", Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh", SettingsVersion: 1}, found: true}}
	for index := range tests {
		_, err := NewResolveTenant(&tests[index]).Execute(context.Background(), storeID, userID)
		if !errors.Is(err, ErrTenantAccessDenied) {
			t.Fatalf("case %d error = %v", index, err)
		}
	}
}

func TestResolveTenantRejectsMismatchedRepositoryScope(t *testing.T) {
	storeID, _ := domain.ParseStoreID(storeIDText)
	otherStoreID, _ := domain.ParseStoreID("118f0f7d-7b3d-7d2e-8f4f-3b86f31f2741")
	userID, _ := domain.ParseUserID(userIDText)
	_, err := NewResolveTenant(&repositoryStub{resolvedRecord: TenantRecord{StoreID: otherStoreID, UserID: userID, StoreName: "Other", StoreStatus: domain.StoreStatusActive, MembershipStatus: domain.MembershipStatusActive, DefaultCurrency: "VND", Locale: "vi-VN", Timezone: "Asia/Ho_Chi_Minh", SettingsVersion: 1}, found: true}).Execute(context.Background(), storeID, userID)
	if err == nil || errors.Is(err, ErrTenantAccessDenied) {
		t.Fatalf("Execute() error = %v", err)
	}
}
