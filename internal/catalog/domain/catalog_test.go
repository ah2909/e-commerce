package domain

import (
	"errors"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/shared/ids"
)

func TestProductNormalizesAndArchives(t *testing.T) {
	store, _ := ids.ParseStoreID("11111111-1111-4111-8111-111111111111")
	productID, _ := ids.ParseProductID("22222222-2222-4222-8222-222222222222")
	variantID, _ := ids.ParseVariantID("33333333-3333-4333-8333-333333333333")
	now := time.Date(2026, 9, 10, 1, 2, 3, 0, time.FixedZone("test", 3600))
	product, err := NewProduct(store, productID, ProductDraft{Name: "  Shirt ", Slug: "SHIRT", Variants: []VariantDraft{{ID: variantID, SKU: " sku-1 ", OptionValues: map[string]string{" Size ": " M "}}}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if product.Name != "Shirt" || product.Slug != "shirt" || product.Variants[0].SKU != "SKU-1" || product.CreatedAt.Location() != time.UTC {
		t.Fatalf("product = %#v", product)
	}
	archived, err := product.Archived(now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if archived.Status != ProductStatusArchived || archived.Variants[0].Status != ItemArchived || archived.Version != 2 {
		t.Fatalf("archived = %#v", archived)
	}
	if _, err := archived.Replaced(ProductDraft{}, now); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("replace archived error = %v", err)
	}
}

func TestProductRejectsInvalidPhysicalAndMediaData(t *testing.T) {
	store, _ := ids.ParseStoreID("11111111-1111-4111-8111-111111111111")
	productID, _ := ids.ParseProductID("22222222-2222-4222-8222-222222222222")
	variantID, _ := ids.ParseVariantID("33333333-3333-4333-8333-333333333333")
	mediaID, _ := ids.ParseMediaID("44444444-4444-4444-8444-444444444444")
	length := int64(1)
	tests := []ProductDraft{
		{Name: "Product", Slug: "product", Variants: nil},
		{Name: "Product", Slug: "product", Variants: []VariantDraft{{ID: variantID, SKU: "SKU", WeightGrams: -1}}},
		{Name: "Product", Slug: "product", Variants: []VariantDraft{{ID: variantID, SKU: "SKU", Dimensions: Dimensions{LengthMM: &length}}}},
		{Name: "Product", Slug: "product", Variants: []VariantDraft{{ID: variantID, SKU: "SKU"}}, Media: []MediaDraft{{ID: mediaID, ObjectKey: "../secret", AltText: "image"}}},
	}
	for _, draft := range tests {
		if _, err := NewProduct(store, productID, draft, time.Now()); !errors.Is(err, ErrInvalidCatalog) {
			t.Fatalf("draft %#v error = %v", draft, err)
		}
	}
}

func TestCategoryRejectsSelfParent(t *testing.T) {
	store, _ := ids.ParseStoreID("11111111-1111-4111-8111-111111111111")
	id, _ := ids.ParseCategoryID("22222222-2222-4222-8222-222222222222")
	if _, err := NewCategory(store, id, &id, "Name", "name", time.Now()); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("error = %v", err)
	}
}

func TestProductPublishesOnlyOnceFromDraft(t *testing.T) {
	store, _ := ids.ParseStoreID("11111111-1111-4111-8111-111111111111")
	productID, _ := ids.ParseProductID("22222222-2222-4222-8222-222222222222")
	variantID, _ := ids.ParseVariantID("33333333-3333-4333-8333-333333333333")
	now := time.Now().UTC()
	product, err := NewProduct(store, productID, ProductDraft{Name: "Product", Slug: "product", Variants: []VariantDraft{{ID: variantID, SKU: "SKU"}}}, now)
	if err != nil {
		t.Fatal(err)
	}
	published, err := product.Published(now.Add(time.Minute))
	if err != nil || published.Status != ProductStatusPublished || published.PublishedAt == nil || published.Version != 2 {
		t.Fatalf("published = %#v, error = %v", published, err)
	}
	if _, err := published.Published(now); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("republish error = %v", err)
	}
	if _, err := published.Replaced(ProductDraft{}, now); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("edit published error = %v", err)
	}
}
