package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/ah2909/e-commerce/internal/payments/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shared/money"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Participant struct{ events PaymentEvents }

func NewParticipant(events ...PaymentEvents) Participant {
	value := Participant{}
	if len(events) > 0 {
		value.events = events[0]
	}
	return value
}

type paymentRow struct {
	StoreID     string     `gorm:"column:store_id"`
	PaymentID   string     `gorm:"column:payment_id"`
	OrderID     string     `gorm:"column:order_id"`
	Provider    string     `gorm:"column:provider"`
	AmountMinor int64      `gorm:"column:amount_minor"`
	Currency    string     `gorm:"column:currency"`
	State       string     `gorm:"column:state"`
	Version     int64      `gorm:"column:version"`
	CreatedAt   time.Time  `gorm:"column:created_at"`
	UpdatedAt   time.Time  `gorm:"column:updated_at"`
	ClosedAt    *time.Time `gorm:"column:closed_at"`
}

func (paymentRow) TableName() string { return "payments" }

func (Participant) CreatePendingInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, paymentID ids.PaymentID, orderID ids.OrderID, provider string, amount money.Money, now time.Time) (domain.Payment, error) {
	if storeID == "" || paymentID == "" || orderID == "" || (provider != "vietqr" && provider != "momo") || !amount.Valid() || amount.Currency != "VND" {
		return domain.Payment{}, domain.ErrInvalidPayment
	}
	row := paymentRow{StoreID: storeID.String(), PaymentID: paymentID.String(), OrderID: orderID.String(), Provider: provider, AmountMinor: amount.AmountMinor, Currency: amount.Currency, State: "pending", Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
		return domain.Payment{}, err
	}
	return mapPayment(row)
}

func (Participant) ExpireInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID, now time.Time) (domain.Payment, error) {
	var row paymentRow
	err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND order_id=?", storeID, orderID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Payment{}, domain.ErrInvalidPayment
	}
	if err != nil {
		return domain.Payment{}, err
	}
	if row.State == "reconciliation_required" {
		return mapPayment(row)
	}
	if row.State != "pending" {
		return domain.Payment{}, domain.ErrInvalidPayment
	}
	result := tx.WithContext(ctx).Model(&paymentRow{}).Where("store_id=? AND payment_id=? AND state='pending' AND version=?", storeID, row.PaymentID, row.Version).Updates(map[string]any{"state": "expired", "version": gorm.Expr("version + 1"), "updated_at": now.UTC(), "closed_at": now.UTC()})
	if result.Error != nil || result.RowsAffected != 1 {
		if result.Error != nil {
			return domain.Payment{}, result.Error
		}
		return domain.Payment{}, domain.ErrInvalidPayment
	}
	row.State, row.Version, row.UpdatedAt, row.ClosedAt = "expired", row.Version+1, now.UTC(), ptr(now.UTC())
	return mapPayment(row)
}

func (Participant) LoadInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID) (domain.Payment, error) {
	var row paymentRow
	if err := tx.WithContext(ctx).Where("store_id=? AND order_id=?", storeID, orderID).First(&row).Error; err != nil {
		return domain.Payment{}, err
	}
	return mapPayment(row)
}

func mapPayment(row paymentRow) (domain.Payment, error) {
	amount, err := money.New(row.AmountMinor, row.Currency)
	if err != nil {
		return domain.Payment{}, err
	}
	return domain.Payment{StoreID: ids.StoreID(row.StoreID), ID: ids.PaymentID(row.PaymentID), OrderID: ids.OrderID(row.OrderID), Provider: row.Provider, Amount: amount, State: row.State, Version: row.Version}, nil
}

func ptr(value time.Time) *time.Time { return &value }
