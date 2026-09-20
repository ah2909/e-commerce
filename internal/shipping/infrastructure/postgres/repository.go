package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	auditdomain "github.com/ah2909/e-commerce/internal/audit/domain"
	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	ordersdomain "github.com/ah2909/e-commerce/internal/orders/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shared/money"
	"github.com/ah2909/e-commerce/internal/shipping/application"
	"github.com/ah2909/e-commerce/internal/shipping/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type FulfillmentOrders interface {
	LockFulfillmentOrderInTransaction(context.Context, *gorm.DB, ids.StoreID, ids.OrderID) (application.OrderLock, error)
	TransitionFulfillmentInTransaction(context.Context, *gorm.DB, ids.StoreID, ids.OrderID, int64, ordersdomain.State, ordersdomain.State, time.Time, string) (int64, error)
	GetFulfillmentOrder(context.Context, ids.StoreID, ids.OrderID) (ordersdomain.Order, error)
	ListFulfillmentOrders(context.Context, ids.StoreID, ordersdomain.State, int, string) ([]ordersdomain.Order, string, error)
}

type FulfillmentAuthorizer interface {
	HasPermissionInTransaction(context.Context, *gorm.DB, ids.StoreID, ids.UserID, string) (bool, error)
}

type FulfillmentAudit interface {
	Insert(context.Context, *gorm.DB, auditdomain.Record) error
}

type FulfillmentEvents interface {
	Append(context.Context, *gorm.DB, eventdomain.Event) error
}

type Repository struct {
	db               *gorm.DB
	orders           FulfillmentOrders
	authorize        FulfillmentAuthorizer
	audit            FulfillmentAudit
	events           FulfillmentEvents
	shipmentsEnabled bool
}

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

func (repository *Repository) WithFulfillment(orders FulfillmentOrders, authorize FulfillmentAuthorizer, audit FulfillmentAudit, events FulfillmentEvents) *Repository {
	repository.orders, repository.authorize, repository.audit, repository.events, repository.shipmentsEnabled = orders, authorize, audit, events, true
	return repository
}

type ruleRow struct {
	StoreID        string     `gorm:"column:store_id"`
	ShippingRuleID string     `gorm:"column:shipping_rule_id"`
	Mode           string     `gorm:"column:mode"`
	AmountMinor    int64      `gorm:"column:flat_amount_minor"`
	Currency       string     `gorm:"column:currency"`
	Status         string     `gorm:"column:status"`
	Version        int64      `gorm:"column:version"`
	CreatedAt      time.Time  `gorm:"column:created_at"`
	UpdatedAt      time.Time  `gorm:"column:updated_at"`
	ArchivedAt     *time.Time `gorm:"column:archived_at"`
}

func (ruleRow) TableName() string { return "shipping_rules" }

func (repository *Repository) Get(ctx context.Context, storeID ids.StoreID) (domain.Rule, error) {
	return load(repository.db.WithContext(ctx), storeID, false)
}

func (repository *Repository) QuoteInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID) (domain.Rule, error) {
	return load(tx.WithContext(ctx), storeID, true)
}

func (repository *Repository) GetShipmentByOrderInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID) (domain.Shipment, bool, error) {
	if !repository.shipmentsEnabled {
		return domain.Shipment{}, false, nil
	}
	var row shipmentRow
	if err := tx.WithContext(ctx).Where("store_id=? AND order_id=?", storeID.String(), orderID.String()).Take(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Shipment{}, false, nil
	} else if err != nil {
		return domain.Shipment{}, false, err
	}
	return mapShipment(row), true, nil
}

func (repository *Repository) Replace(ctx context.Context, storeID ids.StoreID, id ids.ShippingRuleID, expected int64, draft domain.Draft) (domain.Rule, error) {
	var result domain.Rule
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current ruleRow
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id = ? AND status = 'active'", storeID).First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if expected != 0 {
				return application.ErrNotFound
			}
		} else if err != nil {
			return err
		} else {
			if current.Version != expected {
				return application.ErrConflict
			}
		}
		var now time.Time
		if err := tx.Raw("SELECT clock_timestamp()").Scan(&now).Error; err != nil {
			return err
		}
		version := int64(1)
		if current.ShippingRuleID != "" {
			version = current.Version + 1
			if err := tx.Model(&ruleRow{}).Where("store_id=? AND shipping_rule_id=? AND status='active'", storeID, current.ShippingRuleID).Updates(map[string]any{"status": "archived", "archived_at": now, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		row := ruleRow{StoreID: storeID.String(), ShippingRuleID: id.String(), Mode: draft.Mode, AmountMinor: draft.AmountMinor, Currency: "VND", Status: "active", Version: version, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
		if err := tx.Create(&row).Error; err != nil {
			return classify(err)
		}
		result, err = mapRule(row)
		return err
	})
	return result, err
}

func load(db *gorm.DB, storeID ids.StoreID, lock bool) (domain.Rule, error) {
	query := db.Where("store_id = ? AND status = 'active'", storeID)
	if lock {
		query = query.Clauses(clause.Locking{Strength: "SHARE"})
	}
	var row ruleRow
	if err := query.First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Rule{}, application.ErrNotFound
	} else if err != nil {
		return domain.Rule{}, err
	}
	return mapRule(row)
}

func mapRule(row ruleRow) (domain.Rule, error) {
	price, err := money.New(row.AmountMinor, row.Currency)
	if err != nil {
		return domain.Rule{}, err
	}
	return domain.Rule{StoreID: ids.StoreID(row.StoreID), ID: ids.ShippingRuleID(row.ShippingRuleID), Mode: row.Mode, Price: price, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, nil
}

type stateError interface{ SQLState() string }

func classify(err error) error {
	var state stateError
	if errors.As(err, &state) && (state.SQLState() == "23505" || state.SQLState() == "23514") {
		return application.ErrConflict
	}
	return fmt.Errorf("persist shipping rule: %w", err)
}
