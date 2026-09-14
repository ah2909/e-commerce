package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ah2909/e-commerce/internal/inventory/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

var (
	ErrNotFound               = errors.New("inventory resource not found")
	ErrConflict               = errors.New("inventory conflict")
	ErrReservationUnavailable = errors.New("inventory reservation unavailable")
)

const MaxPageSize = 100

type Cursor struct {
	UpdatedAt time.Time     `json:"updated_at"`
	VariantID ids.VariantID `json:"variant_id"`
}

type Page struct {
	StockItems []domain.StockItem `json:"stock_items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}

type Repository interface {
	Get(context.Context, ids.StoreID, ids.VariantID) (domain.StockItem, error)
	List(context.Context, ids.StoreID, int, *Cursor) ([]domain.StockItem, error)
	Adjust(context.Context, ids.StoreID, domain.Adjustment) (domain.StockItem, error)
	ResolveAvailability(context.Context, ids.StoreID, []ids.VariantID) (map[ids.VariantID]bool, error)
	Reserve(context.Context, ids.StoreID, ids.OrderID, []domain.Allocation, string) ([]domain.Reservation, error)
	Transition(context.Context, ids.StoreID, ids.OrderID, domain.ReservationStatus, string, string) ([]domain.Reservation, error)
	ExpireDue(context.Context, int, string) (int, error)
}

type Service struct{ repository Repository }

type quantityResolver interface {
	ResolveAvailableQuantities(context.Context, ids.StoreID, []ids.VariantID) (map[ids.VariantID]int64, error)
}

func NewService(repository Repository) Service { return Service{repository: repository} }

func (service Service) Get(ctx context.Context, storeID ids.StoreID, variantID ids.VariantID) (domain.StockItem, error) {
	if storeID == "" || variantID == "" {
		return domain.StockItem{}, domain.ErrInvalidInventory
	}
	return service.repository.Get(ctx, storeID, variantID)
}

func (service Service) List(ctx context.Context, storeID ids.StoreID, limit int, encoded string) (Page, error) {
	if limit == 0 {
		limit = 25
	}
	if storeID == "" || limit < 1 || limit > MaxPageSize {
		return Page{}, domain.ErrInvalidInventory
	}
	cursor, err := DecodeCursor(encoded)
	if err != nil {
		return Page{}, domain.ErrInvalidInventory
	}
	values, err := service.repository.List(ctx, storeID, limit+1, cursor)
	if err != nil {
		return Page{}, fmt.Errorf("list stock: %w", err)
	}
	page := Page{StockItems: values}
	if len(values) > limit {
		page.StockItems = values[:limit]
		last := page.StockItems[len(page.StockItems)-1]
		page.NextCursor, err = EncodeCursor(Cursor{UpdatedAt: last.UpdatedAt, VariantID: last.VariantID})
	}
	return page, err
}

func (service Service) Adjust(ctx context.Context, storeID ids.StoreID, value domain.Adjustment) (domain.StockItem, error) {
	if storeID == "" {
		return domain.StockItem{}, domain.ErrInvalidInventory
	}
	validated, err := domain.ValidateAdjustment(value)
	if err != nil {
		return domain.StockItem{}, err
	}
	result, err := service.repository.Adjust(ctx, storeID, validated)
	if err != nil {
		return domain.StockItem{}, fmt.Errorf("adjust stock: %w", err)
	}
	return result, nil
}

func (service Service) ResolveAvailability(ctx context.Context, storeID ids.StoreID, variants []ids.VariantID) (map[ids.VariantID]bool, error) {
	if storeID == "" {
		return nil, domain.ErrInvalidInventory
	}
	return service.repository.ResolveAvailability(ctx, storeID, variants)
}

func (service Service) ResolveAvailableQuantities(ctx context.Context, storeID ids.StoreID, variants []ids.VariantID) (map[ids.VariantID]int64, error) {
	resolver, ok := service.repository.(quantityResolver)
	if !ok || storeID == "" || len(variants) == 0 || len(variants) > 100 {
		return nil, domain.ErrInvalidInventory
	}
	return resolver.ResolveAvailableQuantities(ctx, storeID, variants)
}

func (service Service) Reserve(ctx context.Context, storeID ids.StoreID, orderID ids.OrderID, allocations []domain.Allocation, correlationID string) ([]domain.Reservation, error) {
	correlationID = strings.TrimSpace(correlationID)
	validated, err := domain.ValidateAllocations(allocations)
	if storeID == "" || orderID == "" || correlationID == "" || len(correlationID) > 200 || err != nil {
		return nil, domain.ErrInvalidInventory
	}
	return service.repository.Reserve(ctx, storeID, orderID, validated, correlationID)
}

func (service Service) Confirm(ctx context.Context, storeID ids.StoreID, orderID ids.OrderID, correlationID string) ([]domain.Reservation, error) {
	return service.transition(ctx, storeID, orderID, domain.ReservationConfirmed, "payment_confirmed", correlationID)
}

func (service Service) Release(ctx context.Context, storeID ids.StoreID, orderID ids.OrderID, reason, correlationID string) ([]domain.Reservation, error) {
	return service.transition(ctx, storeID, orderID, domain.ReservationReleased, reason, correlationID)
}

func (service Service) transition(ctx context.Context, storeID ids.StoreID, orderID ids.OrderID, target domain.ReservationStatus, reason, correlationID string) ([]domain.Reservation, error) {
	reason, correlationID = strings.TrimSpace(reason), strings.TrimSpace(correlationID)
	if storeID == "" || orderID == "" || len(reason) < 1 || len(reason) > 120 || len(correlationID) < 1 || len(correlationID) > 200 {
		return nil, domain.ErrInvalidInventory
	}
	return service.repository.Transition(ctx, storeID, orderID, target, reason, correlationID)
}

func (service Service) ExpireDue(ctx context.Context, limit int, correlationID string) (int, error) {
	correlationID = strings.TrimSpace(correlationID)
	if limit < 1 || limit > 100 || correlationID == "" || len(correlationID) > 160 {
		return 0, domain.ErrInvalidInventory
	}
	return service.repository.ExpireDue(ctx, limit, correlationID)
}

func EncodeCursor(cursor Cursor) (string, error) {
	if cursor.UpdatedAt.IsZero() || cursor.VariantID == "" {
		return "", domain.ErrInvalidInventory
	}
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func DecodeCursor(encoded string) (*Cursor, error) {
	if encoded == "" {
		return nil, nil
	}
	if len(encoded) > 512 {
		return nil, domain.ErrInvalidInventory
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, domain.ErrInvalidInventory
	}
	var cursor Cursor
	if err := json.Unmarshal(raw, &cursor); err != nil || cursor.UpdatedAt.IsZero() || cursor.VariantID == "" {
		return nil, domain.ErrInvalidInventory
	}
	if _, err := ids.ParseVariantID(cursor.VariantID.String()); err != nil {
		return nil, domain.ErrInvalidInventory
	}
	return &cursor, nil
}
