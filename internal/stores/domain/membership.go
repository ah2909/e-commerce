package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/ah2909/e-commerce/internal/shared/ids"
)

var ErrInvalidMembership = errors.New("invalid membership")

type MembershipStatus string

const (
	MembershipStatusActive   MembershipStatus = "active"
	MembershipStatusInactive MembershipStatus = "inactive"
)

type Membership struct {
	storeID   ids.StoreID
	userID    ids.UserID
	status    MembershipStatus
	role      Role
	createdAt time.Time
	updatedAt time.Time
}

func NewMembership(storeID ids.StoreID, userID ids.UserID, now time.Time) (Membership, error) {
	return RestoreMembershipWithRole(storeID, userID, MembershipStatusActive, RoleOwner, now, now)
}

func RestoreMembership(storeID ids.StoreID, userID ids.UserID, status MembershipStatus, createdAt, updatedAt time.Time) (Membership, error) {
	return RestoreMembershipWithRole(storeID, userID, status, RoleOwner, createdAt, updatedAt)
}

func NewMembershipWithRole(storeID ids.StoreID, userID ids.UserID, role Role, now time.Time) (Membership, error) {
	return RestoreMembershipWithRole(storeID, userID, MembershipStatusActive, role, now, now)
}

func RestoreMembershipWithRole(storeID ids.StoreID, userID ids.UserID, status MembershipStatus, role Role, createdAt, updatedAt time.Time) (Membership, error) {
	if _, err := ids.ParseStoreID(storeID.String()); err != nil {
		return Membership{}, fmt.Errorf("%w: store ID", ErrInvalidMembership)
	}
	if _, err := ids.ParseUserID(userID.String()); err != nil {
		return Membership{}, fmt.Errorf("%w: user ID", ErrInvalidMembership)
	}
	if status != MembershipStatusActive && status != MembershipStatusInactive {
		return Membership{}, fmt.Errorf("%w: status", ErrInvalidMembership)
	}
	if _, err := ParseRole(string(role)); err != nil {
		return Membership{}, fmt.Errorf("%w: role", ErrInvalidMembership)
	}
	createdAt = createdAt.UTC()
	updatedAt = updatedAt.UTC()
	if createdAt.IsZero() || updatedAt.IsZero() || updatedAt.Before(createdAt) {
		return Membership{}, fmt.Errorf("%w: timestamps", ErrInvalidMembership)
	}
	return Membership{storeID: storeID, userID: userID, status: status, role: role, createdAt: createdAt, updatedAt: updatedAt}, nil
}

func (m Membership) StoreID() ids.StoreID     { return m.storeID }
func (m Membership) UserID() ids.UserID       { return m.userID }
func (m Membership) Status() MembershipStatus { return m.status }
func (m Membership) Role() Role               { return m.role }
func (m Membership) CreatedAt() time.Time     { return m.createdAt }
func (m Membership) UpdatedAt() time.Time     { return m.updatedAt }
