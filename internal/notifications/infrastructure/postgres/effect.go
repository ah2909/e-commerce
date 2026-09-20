package postgres

import (
	"context"
	"fmt"
	"strings"

	eventapplication "github.com/ah2909/e-commerce/internal/eventing/application"
	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	notificationsapplication "github.com/ah2909/e-commerce/internal/notifications/application"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
)

type Effect struct {
	resolver notificationsapplication.Resolver
}

func NewEffect(resolver notificationsapplication.Resolver) *Effect {
	return &Effect{resolver: resolver}
}

var supported = map[string]struct{}{
	"order.created.v1": {}, "order.paid.v1": {}, "order.expired.v1": {}, "order.cancelled.v1": {}, "order.shipped.v1": {}, "order.completed.v1": {},
}

func (effect *Effect) Apply(ctx context.Context, tx *gorm.DB, event eventdomain.Event) (bool, error) {
	if _, ok := supported[event.Type]; !ok {
		return false, nil
	}
	orderText, ok := event.Payload["order_id"].(string)
	if !ok {
		return false, eventapplication.ErrPermanent
	}
	orderID, err := ids.ParseOrderID(orderText)
	if err != nil {
		return false, eventapplication.ErrPermanent
	}
	contact, err := effect.resolver.ResolveNotificationContact(ctx, event.StoreID, orderID)
	if err != nil {
		return false, fmt.Errorf("resolve notification contact: %w", err)
	}
	if err := tx.WithContext(ctx).Exec(`
INSERT INTO notification_email_intents (store_id,event_id,order_id,event_type,recipient_email,recipient_name,status,created_at)
VALUES (?,?,?,?,?,?,'pending',clock_timestamp())
ON CONFLICT (store_id,event_id) DO NOTHING`, event.StoreID.String(), event.ID.String(), contact.OrderID.String(), event.Type, strings.ToLower(strings.TrimSpace(contact.Email)), strings.TrimSpace(contact.Name)).Error; err != nil {
		return false, fmt.Errorf("insert email intent: %w", err)
	}
	return true, nil
}
