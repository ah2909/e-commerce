package application

import (
	"context"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/stores/domain"
)

type StoreResolverRepository interface {
	ResolveStoreBySubdomain(context.Context, domain.Subdomain) (StoreRecord, bool, error)
}

type TenantRepository interface {
	ResolveTenant(context.Context, ids.StoreID, ids.UserID) (TenantRecord, bool, error)
}

type StoreRecord struct {
	StoreID         ids.StoreID
	Subdomain       domain.Subdomain
	Name            string
	Status          domain.StoreStatus
	DefaultCurrency string
	Locale          string
	Timezone        string
	SettingsVersion int64
}

type TenantRecord struct {
	UserID           ids.UserID
	StoreID          ids.StoreID
	StoreName        string
	Subdomain        domain.Subdomain
	StoreStatus      domain.StoreStatus
	MembershipStatus domain.MembershipStatus
	Role             domain.Role
	DefaultCurrency  string
	Locale           string
	Timezone         string
	SettingsVersion  int64
}
