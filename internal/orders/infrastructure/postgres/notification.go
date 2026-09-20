package postgres

import (
	"context"
	"errors"
	"fmt"

	ordersapplication "github.com/ah2909/e-commerce/internal/orders/application"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
)

func (repository *Repository) ResolveNotificationContact(ctx context.Context, storeID ids.StoreID, orderID ids.OrderID) (ordersapplication.NotificationContact, error) {
	var row struct {
		OrderID      string
		ContactName  string
		ContactEmail string
	}
	err := repository.db.WithContext(ctx).Raw(`SELECT order_id,contact_name,contact_email FROM orders WHERE store_id=? AND order_id=?`, storeID.String(), orderID.String()).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ordersapplication.NotificationContact{}, ordersapplication.ErrNotFound
	}
	if err != nil {
		return ordersapplication.NotificationContact{}, fmt.Errorf("resolve order notification contact: %w", err)
	}
	return ordersapplication.NotificationContact{OrderID: ids.OrderID(row.OrderID), Name: row.ContactName, Email: row.ContactEmail}, nil
}
