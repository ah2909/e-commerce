package application

import (
	"context"

	ordersapplication "github.com/ah2909/e-commerce/internal/orders/application"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

type Resolver interface {
	ResolveNotificationContact(context.Context, ids.StoreID, ids.OrderID) (ordersapplication.NotificationContact, error)
}
