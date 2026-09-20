package postgres

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/ah2909/e-commerce/internal/orders/application"
	"github.com/ah2909/e-commerce/internal/orders/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	shippingapplication "github.com/ah2909/e-commerce/internal/shipping/application"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (repository *Repository) LockFulfillmentOrderInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID) (shippingapplication.OrderLock, error) {
	var row orderRow
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND order_id=?", storeID.String(), orderID.String()).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return shippingapplication.OrderLock{}, application.ErrNotFound
	} else if err != nil {
		return shippingapplication.OrderLock{}, err
	}
	var payment struct{ State string }
	if err := tx.WithContext(ctx).Table("payments").Select("state").Where("store_id=? AND order_id=?", storeID.String(), orderID.String()).Take(&payment).Error; err != nil {
		return shippingapplication.OrderLock{}, err
	}
	return shippingapplication.OrderLock{State: domain.State(row.State), Version: row.Version, PaymentState: payment.State}, nil
}

func (repository *Repository) TransitionFulfillmentInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID, expectedVersion int64, from, to domain.State, now time.Time, _ string) (int64, error) {
	if !domain.CanTransition(from, to) {
		return 0, application.ErrUnprocessable
	}
	updates := map[string]any{"state": to, "version": gorm.Expr("version + 1"), "updated_at": now.UTC()}
	if to == domain.StateCompleted {
		updates["closed_at"] = now.UTC()
	}
	result := tx.WithContext(ctx).Model(&orderRow{}).Where("store_id=? AND order_id=? AND state=? AND version=?", storeID.String(), orderID.String(), from, expectedVersion).Updates(updates)
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected != 1 {
		return 0, application.ErrConflict
	}
	if err := repository.insertHistory(ctx, tx, storeID, orderID, from, to, fulfillmentReason(to), now); err != nil {
		return 0, err
	}
	version := expectedVersion + 1
	return version, nil
}

func fulfillmentReason(state domain.State) string {
	switch state {
	case domain.StateProcessing:
		return "shipment_prepared"
	case domain.StateShipped:
		return "shipment_dispatched"
	default:
		return "shipment_completed"
	}
}

func (repository *Repository) GetFulfillmentOrder(ctx context.Context, storeID ids.StoreID, orderID ids.OrderID) (domain.Order, error) {
	var result domain.Order
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		result, err = repository.loadMatching(tx, storeID, orderID, fulfillmentStates(""))
		return err
	})
	return result, err
}

func (repository *Repository) ListFulfillmentOrders(ctx context.Context, storeID ids.StoreID, state domain.State, limit int, cursor string) ([]domain.Order, string, error) {
	var cursorTime time.Time
	var cursorID ids.OrderID
	if cursor != "" {
		var err error
		cursorTime, cursorID, err = decodeFulfillmentCursor(cursor)
		if err != nil {
			return nil, "", domain.ErrInvalidOrder
		}
	}
	var orders []domain.Order
	var next string
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rows, err := repository.lockFulfillmentOrderIDs(tx, storeID, state, limit, cursorTime, cursorID)
		if err != nil {
			return err
		}
		hasMore := len(rows) > limit
		if hasMore {
			rows = rows[:limit]
		}
		orders = make([]domain.Order, 0, len(rows))
		for _, row := range rows {
			value, err := repository.load(tx, storeID, ids.OrderID(row.OrderID))
			if err != nil {
				return err
			}
			orders = append(orders, value)
		}
		if hasMore && len(orders) > 0 {
			last := orders[len(orders)-1]
			next = base64.RawURLEncoding.EncodeToString([]byte(last.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + last.ID.String()))
		}
		return nil
	})
	return orders, next, err
}

type fulfillmentOrderIDRow struct{ OrderID string }

func (repository *Repository) lockFulfillmentOrderIDs(tx *gorm.DB, storeID ids.StoreID, state domain.State, limit int, cursorTime time.Time, cursorID ids.OrderID) ([]fulfillmentOrderIDRow, error) {
	query := tx.Model(&orderRow{}).Select("order_id").Where("store_id=? AND state IN ?", storeID.String(), fulfillmentStates(state))
	if cursorID != "" {
		query = query.Where("(created_at,order_id) < (?,?::uuid)", cursorTime, cursorID.String())
	}
	var rows []fulfillmentOrderIDRow
	if err := query.Clauses(clause.Locking{Strength: "SHARE"}).Order("created_at DESC,order_id DESC").Limit(limit + 1).Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func fulfillmentStates(state domain.State) []string {
	if state == domain.StatePaid || state == domain.StateProcessing || state == domain.StateShipped {
		return []string{string(state)}
	}
	return []string{string(domain.StatePaid), string(domain.StateProcessing), string(domain.StateShipped)}
}

func decodeFulfillmentCursor(value string) (time.Time, ids.OrderID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) > 256 {
		return time.Time{}, "", domain.ErrInvalidOrder
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 2 {
		return time.Time{}, "", domain.ErrInvalidOrder
	}
	createdAt, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return time.Time{}, "", domain.ErrInvalidOrder
	}
	orderID, err := ids.ParseOrderID(parts[1])
	return createdAt, orderID, err
}
