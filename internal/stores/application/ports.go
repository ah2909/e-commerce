package application

import (
	"context"

	"github.com/ah2909/e-commerce/internal/stores/domain"
)

type Repository interface {
	CreateWithMembership(context.Context, domain.Store, domain.Membership) error
	ResolveTenant(context.Context, domain.StoreID, domain.UserID) (TenantRecord, bool, error)
}

type StoreIDGenerator func() (domain.StoreID, error)

type TenantRecord struct {
	UserID           domain.UserID
	StoreID          domain.StoreID
	StoreName        string
	StoreStatus      domain.StoreStatus
	MembershipStatus domain.MembershipStatus
	DefaultCurrency  string
	Locale           string
	Timezone         string
	SettingsVersion  int64
}
