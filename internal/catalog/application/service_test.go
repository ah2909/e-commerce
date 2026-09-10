package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/catalog/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

type repositoryStub struct{ product domain.Product }

func (stub *repositoryStub) CreateProduct(_ context.Context, _ ids.StoreID, product domain.Product) (domain.Product, error) {
	stub.product = product
	return product, nil
}
func (stub *repositoryStub) ReplaceProduct(_ context.Context, _ ids.StoreID, _ ids.ProductID, _ int64, product domain.Product) (domain.Product, error) {
	stub.product = product
	return product, nil
}
func (stub *repositoryStub) GetProduct(_ context.Context, _ ids.StoreID, _ ids.ProductID) (domain.Product, error) {
	if stub.product.ID == "" {
		return domain.Product{}, ErrNotFound
	}
	return stub.product, nil
}
func (*repositoryStub) ListProducts(context.Context, ids.StoreID, int, *Cursor) ([]domain.Product, error) {
	return nil, nil
}
func (*repositoryStub) ArchiveProduct(context.Context, ids.StoreID, ids.ProductID, int64, time.Time) (domain.Product, error) {
	return domain.Product{}, nil
}
func (*repositoryStub) CreateCategory(context.Context, ids.StoreID, domain.Category) (domain.Category, error) {
	return domain.Category{}, nil
}
func (*repositoryStub) ReplaceCategory(context.Context, ids.StoreID, ids.CategoryID, int64, domain.Category) (domain.Category, error) {
	return domain.Category{}, nil
}
func (*repositoryStub) ListCategories(context.Context, ids.StoreID) ([]domain.Category, error) {
	return nil, nil
}
func (*repositoryStub) ArchiveCategory(context.Context, ids.StoreID, ids.CategoryID, int64, time.Time) (domain.Category, error) {
	return domain.Category{}, nil
}

func TestCreateProductGeneratesChildIDs(t *testing.T) {
	store, _ := ids.ParseStoreID("11111111-1111-4111-8111-111111111111")
	productID, _ := ids.ParseProductID("22222222-2222-4222-8222-222222222222")
	variantID, _ := ids.ParseVariantID("33333333-3333-4333-8333-333333333333")
	repository := &repositoryStub{}
	service := NewService(repository, IDGenerator{Product: func() (ids.ProductID, error) { return productID, nil }, Variant: func() (ids.VariantID, error) { return variantID, nil }, Media: ids.NewMediaID}, time.Now)
	product, err := service.CreateProduct(context.Background(), store, domain.ProductDraft{Name: "Product", Slug: "product", Variants: []domain.VariantDraft{{SKU: "SKU"}}})
	if err != nil {
		t.Fatal(err)
	}
	if product.ID != productID || product.Variants[0].ID != variantID {
		t.Fatalf("product = %#v", product)
	}
}

func TestCursorRejectsMalformedInput(t *testing.T) {
	if _, err := DecodeCursor("not-valid"); !errors.Is(err, domain.ErrInvalidCatalog) {
		t.Fatalf("error = %v", err)
	}
}
