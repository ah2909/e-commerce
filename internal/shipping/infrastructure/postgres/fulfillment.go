package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	auditdomain "github.com/ah2909/e-commerce/internal/audit/domain"
	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	ordersapplication "github.com/ah2909/e-commerce/internal/orders/application"
	ordersdomain "github.com/ah2909/e-commerce/internal/orders/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shipping/application"
	"github.com/ah2909/e-commerce/internal/shipping/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type shipmentRow struct {
	StoreID        string     `gorm:"column:store_id"`
	ShipmentID     string     `gorm:"column:shipment_id"`
	OrderID        string     `gorm:"column:order_id"`
	State          string     `gorm:"column:state"`
	Carrier        string     `gorm:"column:carrier"`
	TrackingNumber string     `gorm:"column:tracking_number"`
	Version        int64      `gorm:"column:version"`
	CreatedAt      time.Time  `gorm:"column:created_at"`
	UpdatedAt      time.Time  `gorm:"column:updated_at"`
	ShippedAt      *time.Time `gorm:"column:shipped_at"`
	CompletedAt    *time.Time `gorm:"column:completed_at"`
}

func (shipmentRow) TableName() string { return "shipments" }

type shipmentCommandRow struct {
	StoreID            string     `gorm:"column:store_id"`
	IdempotencyKey     string     `gorm:"column:idempotency_key"`
	Operation          string     `gorm:"column:operation"`
	RequestFingerprint []byte     `gorm:"column:request_fingerprint"`
	ShipmentID         string     `gorm:"column:shipment_id"`
	OrderID            string     `gorm:"column:order_id"`
	ResultState        string     `gorm:"column:result_state"`
	ResultCarrier      string     `gorm:"column:result_carrier"`
	ResultTracking     string     `gorm:"column:result_tracking_number"`
	ResultVersion      int64      `gorm:"column:result_version"`
	ResultCreatedAt    time.Time  `gorm:"column:result_created_at"`
	ResultUpdatedAt    time.Time  `gorm:"column:result_updated_at"`
	ResultShippedAt    *time.Time `gorm:"column:result_shipped_at"`
	ResultCompletedAt  *time.Time `gorm:"column:result_completed_at"`
	CreatedAt          time.Time  `gorm:"column:created_at"`
}

func (shipmentCommandRow) TableName() string { return "shipment_commands" }

func (repository *Repository) Mutate(ctx context.Context, command application.Command, fingerprint [32]byte) (domain.Shipment, bool, error) {
	if repository.orders == nil || repository.authorize == nil || repository.audit == nil || repository.events == nil {
		return domain.Shipment{}, false, errors.New("fulfillment dependencies unavailable")
	}
	var result domain.Shipment
	created := false
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", command.StoreID.String()+":shipment:"+command.IdempotencyKey).Error; err != nil {
			return err
		}
		allowed, err := repository.authorize.HasPermissionInTransaction(ctx, tx, command.StoreID, command.ActorID, "order.fulfill")
		if err != nil {
			return err
		}
		if !allowed {
			return application.ErrForbidden
		}
		var replay shipmentCommandRow
		if err := tx.Where("store_id=? AND idempotency_key=?", command.StoreID.String(), command.IdempotencyKey).Take(&replay).Error; err == nil {
			if replay.Operation != string(command.Mutation) || !bytes.Equal(replay.RequestFingerprint, fingerprint[:]) {
				return application.ErrConflict
			}
			result = commandShipment(replay)
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		now, err := fulfillmentDatabaseTime(tx)
		if err != nil {
			return err
		}
		var row shipmentRow
		switch command.Mutation {
		case application.MutationCreate:
			locked, err := repository.orders.LockFulfillmentOrderInTransaction(ctx, tx, command.StoreID, command.OrderID)
			if err != nil {
				return mapFulfillmentError(err)
			}
			if locked.State != ordersdomain.StatePaid || locked.PaymentState != "succeeded" {
				return application.ErrUnprocessable
			}
			shipmentID, err := ids.NewShipmentID()
			if err != nil {
				return err
			}
			row = shipmentRow{StoreID: command.StoreID.String(), ShipmentID: shipmentID.String(), OrderID: command.OrderID.String(), State: string(domain.ShipmentProcessing), Carrier: command.Details.Carrier, TrackingNumber: command.Details.TrackingNumber, Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&row).Error; err != nil {
				return classifyFulfillment(err)
			}
			orderVersion, err := repository.orders.TransitionFulfillmentInTransaction(ctx, tx, command.StoreID, command.OrderID, locked.Version, ordersdomain.StatePaid, ordersdomain.StateProcessing, now, command.RequestID)
			if err != nil {
				return mapFulfillmentError(err)
			}
			if err := repository.appendShipmentEffects(ctx, tx, command, row, orderVersion, "shipment.prepared", "shipment.prepared.v1", now); err != nil {
				return err
			}
		case application.MutationUpdate, application.MutationDispatch, application.MutationComplete:
			var reference shipmentRow
			if err := tx.Select("store_id", "shipment_id", "order_id").Where("store_id=? AND shipment_id=?", command.StoreID.String(), command.ShipmentID.String()).Take(&reference).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return application.ErrNotFound
			} else if err != nil {
				return err
			}
			orderID := ids.OrderID(reference.OrderID)
			locked, err := repository.orders.LockFulfillmentOrderInTransaction(ctx, tx, command.StoreID, orderID)
			if err != nil {
				return mapFulfillmentError(err)
			}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND shipment_id=?", command.StoreID.String(), command.ShipmentID.String()).Take(&row).Error; err != nil {
				return err
			}
			if row.Version != command.ExpectedVersion {
				return application.ErrConflict
			}
			if command.Mutation == application.MutationUpdate {
				if locked.State != ordersdomain.StateProcessing || row.State != string(domain.ShipmentProcessing) {
					return application.ErrUnprocessable
				}
				row.Carrier, row.TrackingNumber, row.Version, row.UpdatedAt = command.Details.Carrier, command.Details.TrackingNumber, row.Version+1, now
				if err := tx.Model(&shipmentRow{}).Where("store_id=? AND shipment_id=? AND version=?", row.StoreID, row.ShipmentID, command.ExpectedVersion).Updates(map[string]any{"carrier": row.Carrier, "tracking_number": row.TrackingNumber, "version": row.Version, "updated_at": now}).Error; err != nil {
					return err
				}
				if err := repository.appendShipmentEffects(ctx, tx, command, row, locked.Version, "shipment.updated", "shipment.updated.v1", now); err != nil {
					return err
				}
			} else {
				fromShipment, fromOrder, toShipment, toOrder, action, eventType := domain.ShipmentProcessing, ordersdomain.StateProcessing, domain.ShipmentShipped, ordersdomain.StateShipped, "shipment.shipped", "order.shipped.v1"
				if command.Mutation == application.MutationComplete {
					fromShipment, fromOrder, toShipment, toOrder, action, eventType = domain.ShipmentShipped, ordersdomain.StateShipped, domain.ShipmentCompleted, ordersdomain.StateCompleted, "shipment.completed", "order.completed.v1"
				}
				if locked.State != fromOrder || row.State != string(fromShipment) || !domain.CanTransition(fromShipment, toShipment) {
					return application.ErrUnprocessable
				}
				orderVersion, err := repository.orders.TransitionFulfillmentInTransaction(ctx, tx, command.StoreID, orderID, locked.Version, fromOrder, toOrder, now, command.RequestID)
				if err != nil {
					return mapFulfillmentError(err)
				}
				row.State, row.Version, row.UpdatedAt = string(toShipment), row.Version+1, now
				updates := map[string]any{"state": row.State, "version": row.Version, "updated_at": now}
				if toShipment == domain.ShipmentShipped {
					row.ShippedAt, updates["shipped_at"] = &now, now
				} else {
					row.CompletedAt, updates["completed_at"] = &now, now
				}
				update := tx.Model(&shipmentRow{}).Where("store_id=? AND shipment_id=? AND version=?", row.StoreID, row.ShipmentID, command.ExpectedVersion).Updates(updates)
				if update.Error != nil || update.RowsAffected != 1 {
					return application.ErrConflict
				}
				if err := repository.appendShipmentEffects(ctx, tx, command, row, orderVersion, action, eventType, now); err != nil {
					return err
				}
			}
		}
		commandRow := snapshotCommand(command, fingerprint, row, now)
		if err := tx.Create(&commandRow).Error; err != nil {
			return classifyFulfillment(err)
		}
		result, created = mapShipment(row), true
		return nil
	})
	return result, created, err
}

func (repository *Repository) GetOrder(ctx context.Context, storeID ids.StoreID, orderID ids.OrderID) (ordersdomain.Order, error) {
	value, err := repository.orders.GetFulfillmentOrder(ctx, storeID, orderID)
	return value, mapFulfillmentError(err)
}

func (repository *Repository) ListOrders(ctx context.Context, storeID ids.StoreID, state ordersdomain.State, limit int, cursor string) ([]ordersdomain.Order, string, error) {
	values, next, err := repository.orders.ListFulfillmentOrders(ctx, storeID, state, limit, cursor)
	return values, next, mapFulfillmentError(err)
}

func (repository *Repository) appendShipmentEffects(ctx context.Context, tx *gorm.DB, command application.Command, row shipmentRow, orderVersion int64, action, eventType string, now time.Time) error {
	record, err := auditdomain.NewRecord(command.StoreID, auditdomain.ActorUser, command.ActorID.String(), action, "shipment", row.ShipmentID, command.RequestID, now)
	if err != nil {
		return err
	}
	if err := repository.audit.Insert(ctx, tx, record); err != nil {
		return err
	}
	eventID, err := ids.NewEventID()
	if err != nil {
		return err
	}
	aggregateType, aggregateID, aggregateVersion := "shipment", row.ShipmentID, row.Version
	if eventType == "order.shipped.v1" || eventType == "order.completed.v1" {
		aggregateType, aggregateID, aggregateVersion = "order", row.OrderID, orderVersion
	}
	event, err := eventdomain.New(command.StoreID, eventID, eventType, aggregateType, aggregateID, aggregateVersion, now, command.RequestID, map[string]any{"store_id": command.StoreID.String(), "order_id": row.OrderID, "shipment_id": row.ShipmentID, "state": row.State})
	if err != nil {
		return err
	}
	return repository.events.Append(ctx, tx, event)
}

func snapshotCommand(command application.Command, fingerprint [32]byte, row shipmentRow, now time.Time) shipmentCommandRow {
	return shipmentCommandRow{StoreID: command.StoreID.String(), IdempotencyKey: command.IdempotencyKey, Operation: string(command.Mutation), RequestFingerprint: fingerprint[:], ShipmentID: row.ShipmentID, OrderID: row.OrderID, ResultState: row.State, ResultCarrier: row.Carrier, ResultTracking: row.TrackingNumber, ResultVersion: row.Version, ResultCreatedAt: row.CreatedAt, ResultUpdatedAt: row.UpdatedAt, ResultShippedAt: row.ShippedAt, ResultCompletedAt: row.CompletedAt, CreatedAt: now}
}

func commandShipment(row shipmentCommandRow) domain.Shipment {
	return domain.Shipment{StoreID: ids.StoreID(row.StoreID), ID: ids.ShipmentID(row.ShipmentID), OrderID: ids.OrderID(row.OrderID), State: domain.ShipmentState(row.ResultState), Carrier: row.ResultCarrier, TrackingNumber: row.ResultTracking, Version: row.ResultVersion, CreatedAt: row.ResultCreatedAt, UpdatedAt: row.ResultUpdatedAt, ShippedAt: row.ResultShippedAt, CompletedAt: row.ResultCompletedAt}
}

func mapShipment(row shipmentRow) domain.Shipment {
	return domain.Shipment{StoreID: ids.StoreID(row.StoreID), ID: ids.ShipmentID(row.ShipmentID), OrderID: ids.OrderID(row.OrderID), State: domain.ShipmentState(row.State), Carrier: row.Carrier, TrackingNumber: row.TrackingNumber, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ShippedAt: row.ShippedAt, CompletedAt: row.CompletedAt}
}

func fulfillmentDatabaseTime(tx *gorm.DB) (time.Time, error) {
	var value time.Time
	err := tx.Raw("SELECT clock_timestamp()").Scan(&value).Error
	return value.UTC(), err
}

func mapFulfillmentError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ordersapplication.ErrNotFound):
		return application.ErrNotFound
	case errors.Is(err, ordersapplication.ErrConflict):
		return application.ErrConflict
	case errors.Is(err, ordersapplication.ErrUnprocessable):
		return application.ErrUnprocessable
	default:
		return err
	}
}

func classifyFulfillment(err error) error {
	var state stateError
	if errors.As(err, &state) && (state.SQLState() == "23505" || state.SQLState() == "23514" || state.SQLState() == "23503") {
		return application.ErrConflict
	}
	return fmt.Errorf("persist fulfillment: %w", err)
}
