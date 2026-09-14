package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/stores/domain"
)

type managementRepositoryStub struct {
	created         domain.Membership
	updated         domain.Membership
	createCalls     int
	permissionCalls int
	permissions     []domain.Permission
}

func (stub *managementRepositoryStub) ListMemberships(context.Context, ids.StoreID) ([]domain.Membership, error) {
	return nil, nil
}
func (stub *managementRepositoryStub) CreateMembership(_ context.Context, membership domain.Membership, _ ids.UserID, _ string) error {
	stub.createCalls++
	stub.created = membership
	return nil
}
func (stub *managementRepositoryStub) UpdateMembership(_ context.Context, storeID ids.StoreID, userID ids.UserID, role domain.Role, status domain.MembershipStatus, _ ids.UserID, _ string, now time.Time) (domain.Membership, error) {
	membership, err := domain.RestoreMembershipWithRole(storeID, userID, status, role, now, now)
	stub.updated = membership
	return membership, err
}
func (stub *managementRepositoryStub) ReplaceStaffPermissions(_ context.Context, _ ids.StoreID, _ ids.UserID, permissions []domain.Permission, expected int64, _ ids.UserID, _ string, _ time.Time) (int64, error) {
	stub.permissionCalls++
	stub.permissions = append([]domain.Permission(nil), permissions...)
	return expected + 1, nil
}

func TestMembershipManagerValidatesExplicitPaymentPermissions(t *testing.T) {
	storeID, _ := ids.ParseStoreID(storeIDText)
	actorID, _ := ids.ParseUserID(userIDText)
	targetID, _ := ids.ParseUserID("128f0f7d-7b3d-7d2e-8f4f-3b86f31f2743")
	repository := &managementRepositoryStub{}
	manager := NewMembershipManager(repository, userDirectoryStub{}, time.Now)
	version, err := manager.ReplacePermissions(context.Background(), ReplacePermissionsCommand{StoreID: storeID, ActorID: actorID, UserID: targetID, Permissions: []string{"payment.confirm", "payment.reconcile.view"}, ExpectedVersion: 3, RequestID: "request"})
	if err != nil || version != 4 || repository.permissionCalls != 1 || len(repository.permissions) != 2 {
		t.Fatalf("version = %d, permissions = %#v, calls = %d, error = %v", version, repository.permissions, repository.permissionCalls, err)
	}
	if _, err := manager.ReplacePermissions(context.Background(), ReplacePermissionsCommand{StoreID: storeID, ActorID: actorID, UserID: targetID, Permissions: []string{"inventory.manage"}, RequestID: "request"}); !errors.Is(err, domain.ErrInvalidMembership) {
		t.Fatalf("invalid permission error = %v", err)
	}
	if repository.permissionCalls != 1 {
		t.Fatalf("repository calls after invalid permission = %d", repository.permissionCalls)
	}
}

type userDirectoryStub struct {
	userID ids.UserID
	active bool
	calls  *int
}

func (stub userDirectoryStub) IsActiveUser(context.Context, ids.StoreID, ids.UserID) (bool, error) {
	if stub.calls != nil {
		(*stub.calls)++
	}
	return stub.active, nil
}

func TestMembershipManagerRequiresTenantLocalUserID(t *testing.T) {
	storeID, _ := ids.ParseStoreID(storeIDText)
	actorID, _ := ids.ParseUserID(userIDText)
	manager := NewMembershipManager(&managementRepositoryStub{}, userDirectoryStub{active: true}, time.Now)
	for _, command := range []AddMembershipCommand{{StoreID: storeID, ActorID: actorID, Role: "staff", RequestID: "request"}} {
		if _, err := manager.Add(context.Background(), command); !errors.Is(err, ErrMembershipTarget) {
			t.Fatalf("Add() error = %v", err)
		}
	}
}

func TestMembershipManagerRejectsMalformedUserIDWithoutDependencies(t *testing.T) {
	storeID, _ := ids.ParseStoreID(storeIDText)
	actorID, _ := ids.ParseUserID(userIDText)
	repository := &managementRepositoryStub{}
	directoryCalls := 0
	manager := NewMembershipManager(repository, userDirectoryStub{active: true, calls: &directoryCalls}, time.Now)
	_, err := manager.Add(context.Background(), AddMembershipCommand{StoreID: storeID, ActorID: actorID, UserID: "not-a-uuid", Role: "staff", RequestID: "request"})
	if !errors.Is(err, ErrMembershipTarget) {
		t.Fatalf("Add() error = %v", err)
	}
	if directoryCalls != 0 || repository.createCalls != 0 {
		t.Fatalf("dependency calls = directory %d, repository %d", directoryCalls, repository.createCalls)
	}
}

func TestMembershipManagerAddsActiveUserInRouteStore(t *testing.T) {
	storeID, _ := ids.ParseStoreID(storeIDText)
	actorID, _ := ids.ParseUserID(userIDText)
	targetID, _ := ids.ParseUserID("128f0f7d-7b3d-7d2e-8f4f-3b86f31f2743")
	repository := &managementRepositoryStub{}
	manager := NewMembershipManager(repository, userDirectoryStub{userID: targetID, active: true}, time.Now)
	membership, err := manager.Add(context.Background(), AddMembershipCommand{StoreID: storeID, ActorID: actorID, UserID: targetID.String(), Role: "staff", RequestID: "request"})
	if err != nil {
		t.Fatal(err)
	}
	if membership.StoreID() != storeID || membership.UserID() != targetID || membership.Role() != domain.RoleStaff || repository.created.StoreID() != storeID {
		t.Fatalf("unexpected membership: %#v", membership)
	}
}
