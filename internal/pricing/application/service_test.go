package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/pricing/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shared/money"
)

type repositoryStub struct{ list domain.PriceList }

func (*repositoryStub) Create(context.Context, ids.StoreID, domain.PriceList) (domain.PriceList, error) {
	return domain.PriceList{}, nil
}
func (*repositoryStub) Replace(context.Context, ids.StoreID, ids.PriceListID, int64, domain.PriceList) (domain.PriceList, error) {
	return domain.PriceList{}, nil
}
func (stub *repositoryStub) Get(context.Context, ids.StoreID, ids.PriceListID) (domain.PriceList, error) {
	return stub.list, nil
}
func (*repositoryStub) List(context.Context, ids.StoreID, int, *Cursor) ([]domain.PriceList, error) {
	return nil, nil
}
func (*repositoryStub) Archive(context.Context, ids.StoreID, ids.PriceListID, int64, time.Time) (domain.PriceList, error) {
	return domain.PriceList{}, nil
}
func (*repositoryStub) ResolveCurrent(context.Context, ids.StoreID, []ids.VariantID) (map[ids.VariantID]money.Money, error) {
	return nil, nil
}

func TestReplacingArchivedPriceListIsConflict(t *testing.T) {
	storeID, _ := ids.ParseStoreID("11111111-1111-4111-8111-111111111111")
	listID, _ := ids.ParsePriceListID("22222222-2222-4222-8222-222222222222")
	service := NewService(&repositoryStub{list: domain.PriceList{StoreID: storeID, ID: listID, Status: domain.StatusArchived, Version: 2}}, ids.NewPriceListID, time.Now)
	_, err := service.Replace(context.Background(), storeID, listID, 2, domain.PriceListDraft{})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("error = %v", err)
	}
}
