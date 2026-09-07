package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ah2909/e-commerce/internal/stores/domain"
)

var ErrTenantAccessDenied = errors.New("tenant access denied")

type TenantContext struct {
	userID          domain.UserID
	storeID         domain.StoreID
	storeName       string
	storeStatus     domain.StoreStatus
	defaultCurrency string
	locale          string
	timezone        string
	settingsVersion int64
}

type ResolveTenant struct {
	repository Repository
}

func NewResolveTenant(repository Repository) ResolveTenant {
	return ResolveTenant{repository: repository}
}

func (useCase ResolveTenant) Execute(ctx context.Context, storeID domain.StoreID, userID domain.UserID) (TenantContext, error) {
	record, found, err := useCase.repository.ResolveTenant(ctx, storeID, userID)
	if err != nil {
		return TenantContext{}, fmt.Errorf("resolve tenant persistence: %w", err)
	}
	if !found || record.MembershipStatus != domain.MembershipStatusActive {
		return TenantContext{}, ErrTenantAccessDenied
	}
	if record.StoreID != storeID || record.UserID != userID {
		return TenantContext{}, fmt.Errorf("resolve tenant: repository returned mismatched scope")
	}
	if (record.StoreStatus != domain.StoreStatusActive && record.StoreStatus != domain.StoreStatusSuspended) || record.SettingsVersion < 1 {
		return TenantContext{}, fmt.Errorf("resolve tenant: repository returned invalid store settings")
	}
	return TenantContext{
		userID:          userID,
		storeID:         record.StoreID,
		storeName:       record.StoreName,
		storeStatus:     record.StoreStatus,
		defaultCurrency: record.DefaultCurrency,
		locale:          record.Locale,
		timezone:        record.Timezone,
		settingsVersion: record.SettingsVersion,
	}, nil
}

func (c TenantContext) UserID() domain.UserID           { return c.userID }
func (c TenantContext) StoreID() domain.StoreID         { return c.storeID }
func (c TenantContext) StoreName() string               { return c.storeName }
func (c TenantContext) StoreStatus() domain.StoreStatus { return c.storeStatus }
func (c TenantContext) DefaultCurrency() string         { return c.defaultCurrency }
func (c TenantContext) Locale() string                  { return c.locale }
func (c TenantContext) Timezone() string                { return c.timezone }
func (c TenantContext) SettingsVersion() int64          { return c.settingsVersion }

func (c TenantContext) Location() (*time.Location, error) {
	return time.LoadLocation(c.timezone)
}
