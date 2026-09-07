package domain

import (
	"errors"
	"fmt"
	"time"
)

var ErrInvalidMembership = errors.New("invalid membership")

type MembershipStatus string

const (
	MembershipStatusActive   MembershipStatus = "active"
	MembershipStatusInactive MembershipStatus = "inactive"
)

type Membership struct {
	storeID   StoreID
	userID    UserID
	status    MembershipStatus
	createdAt time.Time
	updatedAt time.Time
}

func NewMembership(storeID StoreID, userID UserID, now time.Time) (Membership, error) {
	return RestoreMembership(storeID, userID, MembershipStatusActive, now, now)
}

func RestoreMembership(storeID StoreID, userID UserID, status MembershipStatus, createdAt, updatedAt time.Time) (Membership, error) {
	if _, err := ParseStoreID(storeID.String()); err != nil {
		return Membership{}, fmt.Errorf("%w: store ID", ErrInvalidMembership)
	}
	if _, err := ParseUserID(userID.String()); err != nil {
		return Membership{}, fmt.Errorf("%w: user ID", ErrInvalidMembership)
	}
	if status != MembershipStatusActive && status != MembershipStatusInactive {
		return Membership{}, fmt.Errorf("%w: status", ErrInvalidMembership)
	}
	createdAt = createdAt.UTC()
	updatedAt = updatedAt.UTC()
	if createdAt.IsZero() || updatedAt.IsZero() || updatedAt.Before(createdAt) {
		return Membership{}, fmt.Errorf("%w: timestamps", ErrInvalidMembership)
	}
	return Membership{storeID: storeID, userID: userID, status: status, createdAt: createdAt, updatedAt: updatedAt}, nil
}

func (m Membership) StoreID() StoreID         { return m.storeID }
func (m Membership) UserID() UserID           { return m.userID }
func (m Membership) Status() MembershipStatus { return m.status }
func (m Membership) CreatedAt() time.Time     { return m.createdAt }
func (m Membership) UpdatedAt() time.Time     { return m.updatedAt }
