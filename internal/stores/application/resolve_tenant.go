package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/stores/domain"
)

var ErrTenantAccessDenied = errors.New("tenant access denied")

type TenantContext struct {
	userID          ids.UserID
	storeID         ids.StoreID
	storeName       string
	storeStatus     domain.StoreStatus
	defaultCurrency string
	locale          string
	timezone        string
	settingsVersion int64
	role            domain.Role
}

type ResolveTenant struct {
	repository TenantRepository
}

func NewResolveTenant(repository TenantRepository) ResolveTenant {
	return ResolveTenant{repository: repository}
}

func (useCase ResolveTenant) Execute(ctx context.Context, storeID ids.StoreID, userID ids.UserID) (TenantContext, error) {
	record, found, err := useCase.repository.ResolveTenant(ctx, storeID, userID)
	if err != nil {
		return TenantContext{}, fmt.Errorf("resolve tenant persistence: %w", err)
	}
	if !found || record.MembershipStatus != domain.MembershipStatusActive {
		return TenantContext{}, ErrTenantAccessDenied
	}
	if _, err := domain.ParseRole(string(record.Role)); err != nil {
		return TenantContext{}, fmt.Errorf("resolve tenant: repository returned invalid role")
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
		role:            record.Role,
	}, nil
}

func (c TenantContext) UserID() ids.UserID              { return c.userID }
func (c TenantContext) StoreID() ids.StoreID            { return c.storeID }
func (c TenantContext) StoreName() string               { return c.storeName }
func (c TenantContext) StoreStatus() domain.StoreStatus { return c.storeStatus }
func (c TenantContext) DefaultCurrency() string         { return c.defaultCurrency }
func (c TenantContext) Locale() string                  { return c.locale }
func (c TenantContext) Timezone() string                { return c.timezone }
func (c TenantContext) SettingsVersion() int64          { return c.settingsVersion }
func (c TenantContext) Role() domain.Role               { return c.role }

func (c TenantContext) Location() (*time.Location, error) {
	return time.LoadLocation(c.timezone)
}
