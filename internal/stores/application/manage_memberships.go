package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/stores/domain"
)

var (
	ErrMembershipTarget = errors.New("membership target is unavailable")
	ErrLastOwner        = errors.New("store must retain an active owner")
)

type UserDirectory interface {
	IsActiveUser(context.Context, ids.StoreID, ids.UserID) (bool, error)
}

type MembershipRepository interface {
	ListMemberships(context.Context, ids.StoreID) ([]domain.Membership, error)
	CreateMembership(context.Context, domain.Membership, ids.UserID, string) error
	UpdateMembership(context.Context, ids.StoreID, ids.UserID, domain.Role, domain.MembershipStatus, ids.UserID, string, time.Time) (domain.Membership, error)
}

type MembershipManager struct {
	repository MembershipRepository
	users      UserDirectory
	now        func() time.Time
}

type AddMembershipCommand struct {
	StoreID   ids.StoreID
	ActorID   ids.UserID
	UserID    string
	Role      string
	RequestID string
}

type UpdateMembershipCommand struct {
	StoreID   ids.StoreID
	ActorID   ids.UserID
	UserID    ids.UserID
	Role      string
	Status    string
	RequestID string
}

func NewMembershipManager(repository MembershipRepository, users UserDirectory, now func() time.Time) MembershipManager {
	return MembershipManager{repository: repository, users: users, now: now}
}

func (manager MembershipManager) List(ctx context.Context, storeID ids.StoreID) ([]domain.Membership, error) {
	return manager.repository.ListMemberships(ctx, storeID)
}

func (manager MembershipManager) Add(ctx context.Context, command AddMembershipCommand) (domain.Membership, error) {
	if command.UserID == "" || command.RequestID == "" {
		return domain.Membership{}, ErrMembershipTarget
	}
	role, err := domain.ParseRole(command.Role)
	if err != nil {
		return domain.Membership{}, fmt.Errorf("add membership: %w", domain.ErrInvalidMembership)
	}
	targetID, err := ids.ParseUserID(command.UserID)
	if err != nil {
		return domain.Membership{}, ErrMembershipTarget
	}
	found, err := manager.users.IsActiveUser(ctx, command.StoreID, targetID)
	if err != nil {
		return domain.Membership{}, fmt.Errorf("resolve membership target: %w", err)
	}
	if !found {
		return domain.Membership{}, ErrMembershipTarget
	}
	membership, err := domain.NewMembershipWithRole(command.StoreID, targetID, role, manager.now().UTC())
	if err != nil {
		return domain.Membership{}, err
	}
	if err := manager.repository.CreateMembership(ctx, membership, command.ActorID, command.RequestID); err != nil {
		return domain.Membership{}, err
	}
	return membership, nil
}

func (manager MembershipManager) Update(ctx context.Context, command UpdateMembershipCommand) (domain.Membership, error) {
	role, roleErr := domain.ParseRole(command.Role)
	status := domain.MembershipStatus(command.Status)
	if roleErr != nil || (status != domain.MembershipStatusActive && status != domain.MembershipStatusInactive) || command.RequestID == "" {
		return domain.Membership{}, fmt.Errorf("update membership: %w", domain.ErrInvalidMembership)
	}
	return manager.repository.UpdateMembership(ctx, command.StoreID, command.UserID, role, status, command.ActorID, command.RequestID, manager.now().UTC())
}
