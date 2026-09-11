package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/catalog/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shared/money"
)

type repositoryStub struct {
	product   domain.Product
	published []domain.Product
	listCalls int
}

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
func (stub *repositoryStub) GetPublishedBySlug(context.Context, ids.StoreID, string) (domain.Product, error) {
	if len(stub.published) == 0 {
		return domain.Product{}, ErrNotFound
	}
	return stub.published[0], nil
}
func (stub *repositoryStub) ListPublished(context.Context, ids.StoreID, int, *PublishedCursor) ([]domain.Product, error) {
	stub.listCalls++
	return stub.published, nil
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

type priceResolverStub struct{ values map[ids.VariantID]money.Money }

func (stub priceResolverStub) ResolveCurrent(context.Context, ids.StoreID, []ids.VariantID) (map[ids.VariantID]money.Money, error) {
	return stub.values, nil
}

type publisherStub struct{ called bool }

func (stub *publisherStub) Publish(context.Context, ids.StoreID, ids.ProductID, int64, string) (domain.Product, error) {
	stub.called = true
	return domain.Product{Status: domain.ProductStatusPublished}, nil
}

func TestPublicProjectionHidesUnpricedVariantsAndAuthoringFields(t *testing.T) {
	store, _ := ids.ParseStoreID("11111111-1111-4111-8111-111111111111")
	productID, _ := ids.ParseProductID("22222222-2222-4222-8222-222222222222")
	pricedID, _ := ids.ParseVariantID("33333333-3333-4333-8333-333333333333")
	unpricedID, _ := ids.ParseVariantID("44444444-4444-4444-8444-444444444444")
	now := time.Now().UTC()
	barcode := "secret"
	product := domain.Product{StoreID: store, ID: productID, Name: "Product", Slug: "product", Status: domain.ProductStatusPublished, Version: 2, PublishedAt: &now, Variants: []domain.Variant{{StoreID: store, ID: pricedID, Status: domain.ItemActive, Barcode: &barcode}, {StoreID: store, ID: unpricedID, Status: domain.ItemActive}}}
	repository := &repositoryStub{published: []domain.Product{product}}
	price, _ := money.New(120000, "VND")
	service := NewService(repository, IDGenerator{}, time.Now).WithPublication(nil, priceResolverStub{values: map[ids.VariantID]money.Money{pricedID: price}})
	page, err := service.ListPublicProducts(context.Background(), store, 25, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Products) != 1 || len(page.Products[0].Variants) != 1 || page.Products[0].Variants[0].VariantID != pricedID || !page.Products[0].Variants[0].Price.VATInclusive {
		t.Fatalf("page = %#v", page)
	}
}

func TestPublishDelegatesToAtomicPublisher(t *testing.T) {
	store, _ := ids.ParseStoreID("11111111-1111-4111-8111-111111111111")
	productID, _ := ids.ParseProductID("22222222-2222-4222-8222-222222222222")
	publisher := &publisherStub{}
	service := NewService(&repositoryStub{}, IDGenerator{}, time.Now).WithPublication(publisher, priceResolverStub{})
	if _, err := service.PublishProduct(context.Background(), store, productID, 1, "request-id"); err != nil {
		t.Fatal(err)
	}
	if !publisher.called {
		t.Fatal("publisher was not called")
	}
}

func TestPublicListingStopsAtBoundedScanBudget(t *testing.T) {
	store, _ := ids.ParseStoreID("11111111-1111-4111-8111-111111111111")
	now := time.Now().UTC()
	products := make([]domain.Product, publicScanBatchSize+1)
	for index := range products {
		productID, _ := ids.NewProductID()
		variantID, _ := ids.NewVariantID()
		products[index] = domain.Product{StoreID: store, ID: productID, Status: domain.ProductStatusPublished, PublishedAt: &now, Variants: []domain.Variant{{ID: variantID, Status: domain.ItemActive}}}
	}
	repository := &repositoryStub{published: products}
	service := NewService(repository, IDGenerator{}, time.Now).WithPublication(nil, priceResolverStub{values: map[ids.VariantID]money.Money{}})
	page, err := service.ListPublicProducts(context.Background(), store, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if repository.listCalls != publicMaxScanBatches || len(page.Products) != 0 || page.NextCursor == "" {
		t.Fatalf("calls = %d, page = %#v", repository.listCalls, page)
	}
}
