package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	auditdomain "github.com/ah2909/e-commerce/internal/audit/domain"
	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	"github.com/ah2909/e-commerce/internal/inventory/application"
	"github.com/ah2909/e-commerce/internal/inventory/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CatalogVariants interface {
	LockActiveVariants(context.Context, *gorm.DB, ids.StoreID, []ids.VariantID) (bool, error)
}

type AuditWriter interface {
	Insert(context.Context, *gorm.DB, auditdomain.Record) error
}
type EventWriter interface {
	Append(context.Context, *gorm.DB, eventdomain.Event) error
}
type LedgerWriter interface {
	Insert(context.Context, *gorm.DB, ledgerRow) error
}

type Repository struct {
	db      *gorm.DB
	catalog CatalogVariants
	audit   AuditWriter
	events  EventWriter
	ledger  LedgerWriter
}

func NewRepository(db *gorm.DB, catalog CatalogVariants, audit AuditWriter, events EventWriter) *Repository {
	return &Repository{db: db, catalog: catalog, audit: audit, events: events, ledger: postgresLedgerWriter{}}
}

type postgresLedgerWriter struct{}

func (postgresLedgerWriter) Insert(_ context.Context, tx *gorm.DB, row ledgerRow) error {
	return tx.Create(&row).Error
}

type stockRow struct {
	StoreID   string    `gorm:"column:store_id"`
	VariantID string    `gorm:"column:variant_id"`
	OnHand    int64     `gorm:"column:on_hand"`
	Version   int64     `gorm:"column:version"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (stockRow) TableName() string { return "inventory_stock_items" }

type reservationRow struct {
	StoreID       string     `gorm:"column:store_id"`
	ReservationID string     `gorm:"column:reservation_id"`
	OrderID       string     `gorm:"column:order_id"`
	VariantID     string     `gorm:"column:variant_id"`
	Quantity      int64      `gorm:"column:quantity"`
	Status        string     `gorm:"column:status"`
	ExpiresAt     time.Time  `gorm:"column:expires_at"`
	Version       int64      `gorm:"column:version"`
	CreatedAt     time.Time  `gorm:"column:created_at"`
	UpdatedAt     time.Time  `gorm:"column:updated_at"`
	ClosedAt      *time.Time `gorm:"column:closed_at"`
	CloseReason   *string    `gorm:"column:close_reason"`
}

func (reservationRow) TableName() string { return "inventory_reservations" }

type ledgerRow struct {
	StoreID           string    `gorm:"column:store_id"`
	LedgerEntryID     string    `gorm:"column:ledger_entry_id"`
	VariantID         string    `gorm:"column:variant_id"`
	ReservationID     *string   `gorm:"column:reservation_id"`
	ActorID           *string   `gorm:"column:actor_id"`
	EntryType         string    `gorm:"column:entry_type"`
	QuantityDelta     int64     `gorm:"column:quantity_delta"`
	ResultingOnHand   int64     `gorm:"column:resulting_on_hand"`
	ResultingReserved int64     `gorm:"column:resulting_reserved"`
	Reason            string    `gorm:"column:reason"`
	BusinessReference string    `gorm:"column:business_reference"`
	IdempotencyKey    *string   `gorm:"column:idempotency_key"`
	ExpectedVersion   *int64    `gorm:"column:expected_version"`
	CreatedAt         time.Time `gorm:"column:created_at"`
}

func (ledgerRow) TableName() string { return "inventory_ledger_entries" }

func (repository *Repository) Get(ctx context.Context, storeID ids.StoreID, variantID ids.VariantID) (domain.StockItem, error) {
	var result domain.StockItem
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		valid, err := repository.catalog.LockActiveVariants(ctx, tx, storeID, []ids.VariantID{variantID})
		if err != nil {
			return err
		}
		if !valid {
			return application.ErrNotFound
		}
		result, err = loadStock(tx, storeID, variantID)
		if errors.Is(err, application.ErrNotFound) {
			result, err = domain.NewImplicitStock(storeID, variantID)
		}
		return err
	})
	return result, err
}

func (repository *Repository) List(ctx context.Context, storeID ids.StoreID, limit int, cursor *application.Cursor) ([]domain.StockItem, error) {
	query := repository.db.WithContext(ctx).Table("inventory_stock_items s").Select(`s.*,COALESCE((SELECT sum(r.quantity) FROM inventory_reservations r WHERE r.store_id=s.store_id AND r.variant_id=s.variant_id AND r.status='active'),0) AS reserved`).Where("s.store_id = ?", storeID.String())
	if cursor != nil {
		var anchors int64
		if err := repository.db.WithContext(ctx).Model(&stockRow{}).Where("store_id=? AND variant_id=? AND updated_at=?", storeID.String(), cursor.VariantID.String(), cursor.UpdatedAt).Count(&anchors).Error; err != nil {
			return nil, fmt.Errorf("validate stock cursor: %w", err)
		}
		if anchors != 1 {
			return nil, application.ErrNotFound
		}
		query = query.Where("(s.updated_at,s.variant_id) < (?,?::uuid)", cursor.UpdatedAt, cursor.VariantID.String())
	}
	type listed struct {
		StoreID   string    `gorm:"column:store_id"`
		VariantID string    `gorm:"column:variant_id"`
		OnHand    int64     `gorm:"column:on_hand"`
		Version   int64     `gorm:"column:version"`
		CreatedAt time.Time `gorm:"column:created_at"`
		UpdatedAt time.Time `gorm:"column:updated_at"`
		Reserved  int64     `gorm:"column:reserved"`
	}
	var rows []listed
	if err := query.Order("s.updated_at DESC,s.variant_id DESC").Limit(limit).Scan(&rows).Error; err != nil {
		return nil, fmt.Errorf("list stock: %w", err)
	}
	result := make([]domain.StockItem, 0, len(rows))
	for _, row := range rows {
		item, err := mapStock(stockRow{StoreID: row.StoreID, VariantID: row.VariantID, OnHand: row.OnHand, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, row.Reserved)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, nil
}

func (repository *Repository) ResolveAvailability(ctx context.Context, storeID ids.StoreID, variants []ids.VariantID) (map[ids.VariantID]bool, error) {
	quantities, err := repository.ResolveAvailableQuantities(ctx, storeID, variants)
	if err != nil {
		return nil, err
	}
	result := make(map[ids.VariantID]bool, len(variants))
	for _, value := range variants {
		result[value] = quantities[value] > 0
	}
	return result, nil
}

func (repository *Repository) ResolveAvailableQuantities(ctx context.Context, storeID ids.StoreID, variants []ids.VariantID) (map[ids.VariantID]int64, error) {
	result := make(map[ids.VariantID]int64, len(variants))
	if len(variants) == 0 {
		return result, nil
	}
	values := variantStrings(variants)
	type availableRow struct {
		VariantID string `gorm:"column:variant_id"`
		Available int64  `gorm:"column:available"`
	}
	var rows []availableRow
	err := repository.db.WithContext(ctx).Raw(`SELECT s.variant_id,s.on_hand-COALESCE(sum(r.quantity) FILTER (WHERE r.status='active'),0) AS available FROM inventory_stock_items s LEFT JOIN inventory_reservations r ON r.store_id=s.store_id AND r.variant_id=s.variant_id WHERE s.store_id=? AND s.variant_id IN ? GROUP BY s.store_id,s.variant_id,s.on_hand`, storeID.String(), values).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("resolve inventory availability: %w", err)
	}
	for _, value := range variants {
		result[value] = 0
	}
	for _, row := range rows {
		id, err := ids.ParseVariantID(row.VariantID)
		if err != nil {
			return nil, err
		}
		result[id] = row.Available
	}
	return result, nil
}

func (repository *Repository) Adjust(ctx context.Context, storeID ids.StoreID, value domain.Adjustment) (domain.StockItem, error) {
	var result domain.StockItem
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockKey(tx, storeID.String()+":adjustment:"+value.IdempotencyKey); err != nil {
			return err
		}
		var replay ledgerRow
		err := tx.Where("store_id=? AND idempotency_key=?", storeID.String(), value.IdempotencyKey).First(&replay).Error
		if err == nil {
			if replay.VariantID != value.VariantID.String() || replay.QuantityDelta != value.QuantityDelta || replay.Reason != value.Reason || replay.ExpectedVersion == nil || *replay.ExpectedVersion != value.ExpectedVersion {
				return application.ErrConflict
			}
			var stock stockRow
			if err := tx.Where("store_id=? AND variant_id=?", storeID.String(), value.VariantID.String()).First(&stock).Error; err != nil {
				return fmt.Errorf("load replay stock: %w", err)
			}
			available, err := domain.Availability(replay.ResultingOnHand, replay.ResultingReserved)
			if err != nil {
				return err
			}
			result = domain.StockItem{StoreID: storeID, VariantID: value.VariantID, OnHand: replay.ResultingOnHand, Reserved: replay.ResultingReserved, Available: available, Version: value.ExpectedVersion + 1, CreatedAt: stock.CreatedAt, UpdatedAt: replay.CreatedAt}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("load adjustment replay: %w", err)
		}
		if err := lockKey(tx, storeID.String()+":stock:"+value.VariantID.String()); err != nil {
			return err
		}
		valid, err := repository.catalog.LockActiveVariants(ctx, tx, storeID, []ids.VariantID{value.VariantID})
		if err != nil {
			return err
		}
		if !valid {
			return application.ErrNotFound
		}
		now, err := databaseTime(tx)
		if err != nil {
			return err
		}
		var row stockRow
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND variant_id=?", storeID.String(), value.VariantID.String()).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if value.ExpectedVersion != 0 || value.QuantityDelta < 1 {
				return application.ErrConflict
			}
			row = stockRow{StoreID: storeID.String(), VariantID: value.VariantID.String(), OnHand: value.QuantityDelta, Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&row).Error; err != nil {
				return fmt.Errorf("create stock: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("lock stock: %w", err)
		} else {
			if row.Version != value.ExpectedVersion {
				return application.ErrConflict
			}
			reserved, err := activeReserved(tx, storeID, value.VariantID)
			if err != nil {
				return err
			}
			if value.QuantityDelta > 0 && row.OnHand > domain.MaxQuantity-value.QuantityDelta {
				return domain.ErrInvalidInventory
			}
			updated := row.OnHand + value.QuantityDelta
			if updated < reserved || updated < 0 {
				return domain.ErrInsufficientStock
			}
			row.OnHand, row.Version, row.UpdatedAt = updated, row.Version+1, now
			if err := tx.Model(&stockRow{}).Where("store_id=? AND variant_id=? AND version=?", storeID.String(), value.VariantID.String(), value.ExpectedVersion).Updates(map[string]any{"on_hand": row.OnHand, "version": row.Version, "updated_at": now}).Error; err != nil {
				return fmt.Errorf("update stock: %w", err)
			}
		}
		ledgerID, err := ids.NewLedgerEntryID()
		if err != nil {
			return err
		}
		actor := value.ActorID.String()
		key := value.IdempotencyKey
		expected := value.ExpectedVersion
		reserved, err := activeReserved(tx, storeID, value.VariantID)
		if err != nil {
			return err
		}
		ledger := ledgerRow{StoreID: storeID.String(), LedgerEntryID: ledgerID.String(), VariantID: value.VariantID.String(), ActorID: &actor, EntryType: "manual_adjustment", QuantityDelta: value.QuantityDelta, ResultingOnHand: row.OnHand, ResultingReserved: reserved, Reason: value.Reason, BusinessReference: "manual_adjustment", IdempotencyKey: &key, ExpectedVersion: &expected, CreatedAt: now}
		if err := repository.ledger.Insert(ctx, tx, ledger); err != nil {
			return fmt.Errorf("append inventory ledger: %w", err)
		}
		audit, err := auditdomain.NewRecord(storeID, auditdomain.ActorUser, value.ActorID.String(), "inventory.adjusted", "inventory_stock_item", value.VariantID.String(), value.RequestID, now)
		if err != nil {
			return err
		}
		if err := repository.audit.Insert(ctx, tx, audit); err != nil {
			return err
		}
		if err := repository.appendEvent(ctx, tx, storeID, "inventory.changed.v1", "inventory_stock_item", value.VariantID.String(), row.Version, now, value.RequestID, map[string]any{"store_id": storeID.String(), "variant_id": value.VariantID.String(), "ledger_entry_id": ledgerID.String(), "reason": "manual_adjustment"}); err != nil {
			return err
		}
		result, err = mapStock(row, reserved)
		return err
	})
	return result, err
}

func (repository *Repository) Reserve(ctx context.Context, storeID ids.StoreID, orderID ids.OrderID, allocations []domain.Allocation, correlationID string) ([]domain.Reservation, error) {
	ordered := append([]domain.Allocation(nil), allocations...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].VariantID.String() < ordered[j].VariantID.String() })
	var result []domain.Reservation
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockKey(tx, storeID.String()+":order:"+orderID.String()); err != nil {
			return err
		}
		existing, err := loadReservations(tx, storeID, orderID, false)
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			if !sameAllocations(existing, ordered) {
				return application.ErrConflict
			}
			result = existing
			return nil
		}
		variantIDs := make([]ids.VariantID, len(ordered))
		for i, v := range ordered {
			variantIDs[i] = v.VariantID
		}
		valid, err := repository.catalog.LockActiveVariants(ctx, tx, storeID, variantIDs)
		if err != nil {
			return err
		}
		if !valid {
			return application.ErrNotFound
		}
		now, err := databaseTime(tx)
		if err != nil {
			return err
		}
		expires := now.Add(domain.ReservationTTL)
		for _, allocation := range ordered {
			var row stockRow
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND variant_id=?", storeID.String(), allocation.VariantID.String()).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return domain.ErrInsufficientStock
			} else if err != nil {
				return fmt.Errorf("lock stock for reservation: %w", err)
			}
			reserved, err := activeReserved(tx, storeID, allocation.VariantID)
			if err != nil {
				return err
			}
			if row.OnHand-reserved < allocation.Quantity {
				return domain.ErrInsufficientStock
			}
		}
		result = make([]domain.Reservation, 0, len(ordered))
		for _, allocation := range ordered {
			id, err := ids.NewReservationID()
			if err != nil {
				return err
			}
			row := reservationRow{StoreID: storeID.String(), ReservationID: id.String(), OrderID: orderID.String(), VariantID: allocation.VariantID.String(), Quantity: allocation.Quantity, Status: string(domain.ReservationActive), ExpiresAt: expires, Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&row).Error; err != nil {
				return fmt.Errorf("create reservation: %w", err)
			}
			mapped, err := mapReservation(row)
			if err != nil {
				return err
			}
			result = append(result, mapped)
			if err := repository.appendEvent(ctx, tx, storeID, "inventory.reserved.v1", "inventory_reservation", id.String(), 1, now, correlationID, map[string]any{"store_id": storeID.String(), "reservation_id": id.String(), "order_id": orderID.String(), "variant_id": allocation.VariantID.String(), "expires_at": expires}); err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}

func (repository *Repository) Transition(ctx context.Context, storeID ids.StoreID, orderID ids.OrderID, target domain.ReservationStatus, reason, correlationID string) ([]domain.Reservation, error) {
	result, _, err := repository.transition(ctx, storeID, orderID, target, reason, correlationID)
	return result, err
}

func (repository *Repository) transition(ctx context.Context, storeID ids.StoreID, orderID ids.OrderID, target domain.ReservationStatus, reason, correlationID string) ([]domain.Reservation, bool, error) {
	var result []domain.Reservation
	changed := false
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockKey(tx, storeID.String()+":order:"+orderID.String()); err != nil {
			return err
		}
		rows, err := loadReservationRows(tx, storeID, orderID, true)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return application.ErrNotFound
		}
		allTarget := true
		for _, row := range rows {
			if domain.ReservationStatus(row.Status) != target {
				allTarget = false
			}
			if !domain.CanTransition(domain.ReservationStatus(row.Status), target) {
				return application.ErrConflict
			}
		}
		if allTarget {
			result, err = mapReservations(rows)
			return err
		}
		changed = true
		now, err := databaseTime(tx)
		if err != nil {
			return err
		}
		if target == domain.ReservationConfirmed {
			for _, row := range rows {
				if !now.Before(row.ExpiresAt) {
					return application.ErrConflict
				}
			}
		}
		stockRows := map[string]stockRow{}
		if target == domain.ReservationConfirmed {
			variants := append([]reservationRow(nil), rows...)
			sort.Slice(variants, func(i, j int) bool { return variants[i].VariantID < variants[j].VariantID })
			for _, reservation := range variants {
				var stock stockRow
				if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND variant_id=?", storeID.String(), reservation.VariantID).First(&stock).Error; err != nil {
					return fmt.Errorf("lock stock for confirmation: %w", err)
				}
				if stock.OnHand < reservation.Quantity {
					return domain.ErrInsufficientStock
				}
				stock.OnHand -= reservation.Quantity
				stock.Version++
				stock.UpdatedAt = now
				if err := tx.Model(&stockRow{}).Where("store_id=? AND variant_id=?", storeID.String(), reservation.VariantID).Updates(map[string]any{"on_hand": stock.OnHand, "version": stock.Version, "updated_at": now}).Error; err != nil {
					return err
				}
				stockRows[reservation.VariantID] = stock
			}
		}
		for index := range rows {
			rows[index].Status = string(target)
			rows[index].Version++
			rows[index].UpdatedAt = now
			rows[index].ClosedAt = &now
			rows[index].CloseReason = &reason
			if err := tx.Model(&reservationRow{}).Where("store_id=? AND reservation_id=? AND status='active'", storeID.String(), rows[index].ReservationID).Updates(map[string]any{"status": string(target), "version": rows[index].Version, "updated_at": now, "closed_at": now, "close_reason": reason}).Error; err != nil {
				return err
			}
			eventType := "inventory.reservation_released.v1"
			if target == domain.ReservationConfirmed {
				eventType = "inventory.reservation_confirmed.v1"
			}
			if target == domain.ReservationExpired {
				eventType = "inventory.reservation_expired.v1"
			}
			if target == domain.ReservationConfirmed {
				ledgerID, err := ids.NewLedgerEntryID()
				if err != nil {
					return err
				}
				reservationID := rows[index].ReservationID
				stock := stockRows[rows[index].VariantID]
				variantID, err := ids.ParseVariantID(rows[index].VariantID)
				if err != nil {
					return err
				}
				reserved, err := activeReserved(tx, storeID, variantID)
				if err != nil {
					return err
				}
				ledger := ledgerRow{StoreID: storeID.String(), LedgerEntryID: ledgerID.String(), VariantID: rows[index].VariantID, ReservationID: &reservationID, EntryType: "reservation_confirmation", QuantityDelta: -rows[index].Quantity, ResultingOnHand: stock.OnHand, ResultingReserved: reserved, Reason: reason, BusinessReference: orderID.String(), CreatedAt: now}
				if err := repository.ledger.Insert(ctx, tx, ledger); err != nil {
					return fmt.Errorf("append confirmation ledger: %w", err)
				}
				if err := repository.appendEvent(ctx, tx, storeID, "inventory.changed.v1", "inventory_stock_item", rows[index].VariantID, stock.Version, now, correlationID, map[string]any{"store_id": storeID.String(), "variant_id": rows[index].VariantID, "ledger_entry_id": ledgerID.String(), "reason": "reservation_confirmation"}); err != nil {
					return err
				}
			}
			if err := repository.appendEvent(ctx, tx, storeID, eventType, "inventory_reservation", rows[index].ReservationID, rows[index].Version, now, correlationID, map[string]any{"store_id": storeID.String(), "reservation_id": rows[index].ReservationID, "order_id": orderID.String(), "variant_id": rows[index].VariantID, "reason": reason}); err != nil {
				return err
			}
		}
		result, err = mapReservations(rows)
		return err
	})
	return result, changed, err
}

func (repository *Repository) ExpireDue(ctx context.Context, limit int, correlationID string) (int, error) {
	type candidate struct {
		StoreID string `gorm:"column:store_id"`
		OrderID string `gorm:"column:order_id"`
	}
	var values []candidate
	if err := repository.db.WithContext(ctx).Raw(`SELECT DISTINCT store_id,order_id FROM inventory_reservations WHERE status='active' AND expires_at<=clock_timestamp() ORDER BY store_id,order_id LIMIT ?`, limit).Scan(&values).Error; err != nil {
		return 0, fmt.Errorf("discover expired reservations: %w", err)
	}
	processed := 0
	for index, value := range values {
		storeID, err := ids.ParseStoreID(value.StoreID)
		if err != nil {
			return processed, err
		}
		orderID, err := ids.ParseOrderID(value.OrderID)
		if err != nil {
			return processed, err
		}
		_, changed, err := repository.transition(ctx, storeID, orderID, domain.ReservationExpired, "reservation_expired", fmt.Sprintf("%s:%d", correlationID, index))
		if errors.Is(err, application.ErrConflict) || errors.Is(err, application.ErrNotFound) {
			continue
		}
		if err != nil {
			return processed, err
		}
		if changed {
			processed++
		}
	}
	return processed, nil
}

func (repository *Repository) appendEvent(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, eventType, aggregateType, aggregateID string, version int64, at time.Time, correlationID string, payload map[string]any) error {
	id, err := ids.NewEventID()
	if err != nil {
		return err
	}
	event, err := eventdomain.New(storeID, id, eventType, aggregateType, aggregateID, version, at, correlationID, payload)
	if err != nil {
		return err
	}
	return repository.events.Append(ctx, tx, event)
}

func loadStock(tx *gorm.DB, storeID ids.StoreID, variantID ids.VariantID) (domain.StockItem, error) {
	type stockSnapshot struct {
		StoreID   string    `gorm:"column:store_id"`
		VariantID string    `gorm:"column:variant_id"`
		OnHand    int64     `gorm:"column:on_hand"`
		Version   int64     `gorm:"column:version"`
		CreatedAt time.Time `gorm:"column:created_at"`
		UpdatedAt time.Time `gorm:"column:updated_at"`
		Reserved  int64     `gorm:"column:reserved"`
	}
	var snapshot stockSnapshot
	result := tx.Raw(`SELECT s.*,COALESCE((SELECT sum(r.quantity) FROM inventory_reservations r WHERE r.store_id=s.store_id AND r.variant_id=s.variant_id AND r.status='active'),0) AS reserved FROM inventory_stock_items s WHERE s.store_id=? AND s.variant_id=?`, storeID.String(), variantID.String()).Scan(&snapshot)
	if result.Error != nil {
		return domain.StockItem{}, result.Error
	}
	if result.RowsAffected == 0 {
		return domain.StockItem{}, application.ErrNotFound
	}
	return mapStock(stockRow{StoreID: snapshot.StoreID, VariantID: snapshot.VariantID, OnHand: snapshot.OnHand, Version: snapshot.Version, CreatedAt: snapshot.CreatedAt, UpdatedAt: snapshot.UpdatedAt}, snapshot.Reserved)
}
func mapStock(row stockRow, reserved int64) (domain.StockItem, error) {
	id, err := ids.ParseVariantID(row.VariantID)
	if err != nil {
		return domain.StockItem{}, err
	}
	store, err := ids.ParseStoreID(row.StoreID)
	if err != nil {
		return domain.StockItem{}, err
	}
	available, err := domain.Availability(row.OnHand, reserved)
	if err != nil {
		return domain.StockItem{}, err
	}
	return domain.StockItem{StoreID: store, VariantID: id, OnHand: row.OnHand, Reserved: reserved, Available: available, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, nil
}
func activeReserved(tx *gorm.DB, storeID ids.StoreID, variantID ids.VariantID) (int64, error) {
	var total int64
	if err := tx.Model(&reservationRow{}).Select("COALESCE(sum(quantity),0)").Where("store_id=? AND variant_id=? AND status='active'", storeID.String(), variantID.String()).Scan(&total).Error; err != nil {
		return 0, fmt.Errorf("sum active reservations: %w", err)
	}
	return total, nil
}
func databaseTime(tx *gorm.DB) (time.Time, error) {
	var value time.Time
	if err := tx.Raw("SELECT clock_timestamp()").Scan(&value).Error; err != nil {
		return time.Time{}, fmt.Errorf("read database time: %w", err)
	}
	return value.UTC(), nil
}
func lockKey(tx *gorm.DB, value string) error {
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", value).Error; err != nil {
		return fmt.Errorf("lock inventory operation: %w", err)
	}
	return nil
}
func variantStrings(values []ids.VariantID) []string {
	result := make([]string, len(values))
	for i, v := range values {
		result[i] = v.String()
	}
	return result
}
func loadReservationRows(tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID, lock bool) ([]reservationRow, error) {
	query := tx.Where("store_id=? AND order_id=?", storeID.String(), orderID.String()).Order("reservation_id")
	if lock {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var rows []reservationRow
	if err := query.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("load reservations: %w", err)
	}
	return rows, nil
}
func loadReservations(tx *gorm.DB, storeID ids.StoreID, orderID ids.OrderID, lock bool) ([]domain.Reservation, error) {
	rows, err := loadReservationRows(tx, storeID, orderID, lock)
	if err != nil {
		return nil, err
	}
	return mapReservations(rows)
}
func mapReservations(rows []reservationRow) ([]domain.Reservation, error) {
	result := make([]domain.Reservation, 0, len(rows))
	for _, row := range rows {
		value, err := mapReservation(row)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}
func mapReservation(row reservationRow) (domain.Reservation, error) {
	store, err := ids.ParseStoreID(row.StoreID)
	if err != nil {
		return domain.Reservation{}, err
	}
	id, err := ids.ParseReservationID(row.ReservationID)
	if err != nil {
		return domain.Reservation{}, err
	}
	order, err := ids.ParseOrderID(row.OrderID)
	if err != nil {
		return domain.Reservation{}, err
	}
	variant, err := ids.ParseVariantID(row.VariantID)
	if err != nil {
		return domain.Reservation{}, err
	}
	reason := ""
	if row.CloseReason != nil {
		reason = *row.CloseReason
	}
	return domain.Reservation{StoreID: store, ID: id, OrderID: order, VariantID: variant, Quantity: row.Quantity, Status: domain.ReservationStatus(row.Status), ExpiresAt: row.ExpiresAt, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ClosedAt: row.ClosedAt, CloseReason: reason}, nil
}
func sameAllocations(existing []domain.Reservation, requested []domain.Allocation) bool {
	if len(existing) != len(requested) {
		return false
	}
	byVariant := make(map[ids.VariantID]int64, len(existing))
	for _, value := range existing {
		byVariant[value.VariantID] = value.Quantity
	}
	for _, value := range requested {
		if byVariant[value.VariantID] != value.Quantity {
			return false
		}
	}
	return true
}
