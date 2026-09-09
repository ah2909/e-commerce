package application

import (
	"context"
	"errors"
	"testing"
	"time"

	identityapplication "github.com/ah2909/e-commerce/internal/identity/application"
	identitydomain "github.com/ah2909/e-commerce/internal/identity/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	storesdomain "github.com/ah2909/e-commerce/internal/stores/domain"
)

type repositoryStub struct {
	store      storesdomain.Store
	membership storesdomain.Membership
	account    identityapplication.PreparedAccount
	actorID    string
	err        error
}

func (stub *repositoryStub) Create(_ context.Context, store storesdomain.Store, membership storesdomain.Membership, account identityapplication.PreparedAccount, actorID, _ string) error {
	stub.store, stub.membership, stub.account, stub.actorID = store, membership, account, actorID
	return stub.err
}

type identityStub struct {
	prepared identityapplication.PreparedAccount
	err      error
}

func (stub identityStub) PrepareAccount(context.Context, ids.StoreID, string, string) (identityapplication.PreparedAccount, error) {
	return stub.prepared, stub.err
}

func TestServicePreparesAndPersistsOneScopedOwner(t *testing.T) {
	storeID, _ := ids.ParseStoreID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	userID, _ := ids.ParseUserID("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	now := time.Date(2026, 9, 8, 4, 0, 0, 0, time.UTC)
	user := identityapplication.UserRecord{StoreID: storeID, ID: userID, Status: identitydomain.UserStatusActive, CreatedAt: now, UpdatedAt: now}
	prepared := identityapplication.PreparedAccount{User: user}
	repository := &repositoryStub{}
	service := NewService(repository, identityStub{prepared: prepared}, func() (ids.StoreID, error) { return storeID, nil }, func() time.Time { return now })
	result, err := service.Execute(context.Background(), Command{Subdomain: "book-shop", StoreName: "Book Store", Email: "owner@example.com", Password: "correct-horse-battery-staple", PlatformActorID: "deployment", RequestID: "request"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Store.ID() != storeID || result.OwnerUserID != userID || repository.membership.StoreID() != storeID || repository.membership.UserID() != userID || repository.membership.Role() != storesdomain.RoleOwner || repository.account.User.StoreID != storeID || repository.actorID != "deployment" {
		t.Fatalf("onboarding result = %#v, membership = %#v", result, repository.membership)
	}
}

func TestServiceRejectsInvalidStoreBeforePreparingIdentity(t *testing.T) {
	storeID, _ := ids.ParseStoreID("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	service := NewService(&repositoryStub{}, identityStub{err: errors.New("identity should not be called")}, func() (ids.StoreID, error) { return storeID, nil }, time.Now)
	if _, err := service.Execute(context.Background(), Command{Subdomain: "www", StoreName: "Store", PlatformActorID: "deployment", RequestID: "request"}); !errors.Is(err, storesdomain.ErrInvalidStore) {
		t.Fatalf("Execute() error = %v", err)
	}
}
