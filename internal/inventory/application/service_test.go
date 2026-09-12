package application

import (
	"context"
	"errors"
	"testing"

	"github.com/ah2909/e-commerce/internal/inventory/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

type repositoryStub struct {
	adjustment domain.Adjustment
}

func (*repositoryStub) Get(context.Context, ids.StoreID, ids.VariantID) (domain.StockItem, error) {
	return domain.StockItem{}, nil
}
func (*repositoryStub) List(context.Context, ids.StoreID, int, *Cursor) ([]domain.StockItem, error) {
	return nil, nil
}
func (stub *repositoryStub) Adjust(_ context.Context, _ ids.StoreID, value domain.Adjustment) (domain.StockItem, error) {
	stub.adjustment = value
	return domain.StockItem{}, nil
}
func (*repositoryStub) ResolveAvailability(context.Context, ids.StoreID, []ids.VariantID) (map[ids.VariantID]bool, error) {
	return map[ids.VariantID]bool{}, nil
}
func (*repositoryStub) Reserve(context.Context, ids.StoreID, ids.OrderID, []domain.Allocation, string) ([]domain.Reservation, error) {
	return nil, nil
}
func (*repositoryStub) Transition(context.Context, ids.StoreID, ids.OrderID, domain.ReservationStatus, string, string) ([]domain.Reservation, error) {
	return nil, nil
}
func (*repositoryStub) ExpireDue(context.Context, int, string) (int, error) { return 0, nil }

func TestAdjustValidatesBeforeRepository(t *testing.T) {
	store, _ := ids.ParseStoreID("11111111-1111-4111-8111-111111111111")
	variant, _ := ids.ParseVariantID("22222222-2222-4222-8222-222222222222")
	user, _ := ids.ParseUserID("33333333-3333-4333-8333-333333333333")
	repository := &repositoryStub{}
	service := NewService(repository)
	_, err := service.Adjust(context.Background(), store, domain.Adjustment{VariantID: variant, QuantityDelta: 2, Reason: " receipt ", IdempotencyKey: " key ", ActorID: user, RequestID: "request", ExpectedVersion: 0})
	if err != nil || repository.adjustment.Reason != "receipt" || repository.adjustment.IdempotencyKey != "key" {
		t.Fatalf("adjustment = %#v, error = %v", repository.adjustment, err)
	}
	_, err = service.Adjust(context.Background(), store, domain.Adjustment{VariantID: variant})
	if !errors.Is(err, domain.ErrInvalidInventory) {
		t.Fatalf("error = %v", err)
	}
}
