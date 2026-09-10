package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ah2909/e-commerce/internal/catalog/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

var (
	ErrNotFound = errors.New("catalog resource not found")
	ErrConflict = errors.New("catalog conflict")
)

const MaxPageSize = 100

type Cursor struct {
	UpdatedAt time.Time     `json:"updated_at"`
	ProductID ids.ProductID `json:"product_id"`
}

type Page struct {
	Products   []domain.Product
	NextCursor string
}

type Repository interface {
	CreateProduct(context.Context, ids.StoreID, domain.Product) (domain.Product, error)
	ReplaceProduct(context.Context, ids.StoreID, ids.ProductID, int64, domain.Product) (domain.Product, error)
	GetProduct(context.Context, ids.StoreID, ids.ProductID) (domain.Product, error)
	ListProducts(context.Context, ids.StoreID, int, *Cursor) ([]domain.Product, error)
	ArchiveProduct(context.Context, ids.StoreID, ids.ProductID, int64, time.Time) (domain.Product, error)
	CreateCategory(context.Context, ids.StoreID, domain.Category) (domain.Category, error)
	ReplaceCategory(context.Context, ids.StoreID, ids.CategoryID, int64, domain.Category) (domain.Category, error)
	ListCategories(context.Context, ids.StoreID) ([]domain.Category, error)
	ArchiveCategory(context.Context, ids.StoreID, ids.CategoryID, int64, time.Time) (domain.Category, error)
}

type IDGenerator struct {
	Product  func() (ids.ProductID, error)
	Variant  func() (ids.VariantID, error)
	Category func() (ids.CategoryID, error)
	Media    func() (ids.MediaID, error)
}

type Service struct {
	repository Repository
	ids        IDGenerator
	now        func() time.Time
}

func NewService(repository Repository, generator IDGenerator, now func() time.Time) Service {
	return Service{repository: repository, ids: generator, now: now}
}

func DefaultIDGenerator() IDGenerator {
	return IDGenerator{Product: ids.NewProductID, Variant: ids.NewVariantID, Category: ids.NewCategoryID, Media: ids.NewMediaID}
}

func (service Service) CreateProduct(ctx context.Context, storeID ids.StoreID, draft domain.ProductDraft) (domain.Product, error) {
	productID, err := service.ids.Product()
	if err != nil {
		return domain.Product{}, fmt.Errorf("generate product ID: %w", err)
	}
	draft, err = service.completeDraftIDs(draft, false)
	if err != nil {
		return domain.Product{}, err
	}
	product, err := domain.NewProduct(storeID, productID, draft, service.now())
	if err != nil {
		return domain.Product{}, err
	}
	created, err := service.repository.CreateProduct(ctx, storeID, product)
	if err != nil {
		return domain.Product{}, fmt.Errorf("create product: %w", err)
	}
	return created, nil
}

func (service Service) ReplaceProduct(ctx context.Context, storeID ids.StoreID, productID ids.ProductID, expectedVersion int64, draft domain.ProductDraft) (domain.Product, error) {
	if expectedVersion < 1 {
		return domain.Product{}, domain.ErrInvalidCatalog
	}
	current, err := service.repository.GetProduct(ctx, storeID, productID)
	if err != nil {
		return domain.Product{}, fmt.Errorf("load product: %w", err)
	}
	if current.Version != expectedVersion {
		return domain.Product{}, ErrConflict
	}
	knownVariants := make(map[ids.VariantID]bool, len(current.Variants))
	for _, variant := range current.Variants {
		if variant.Status == domain.ItemActive {
			knownVariants[variant.ID] = true
		}
	}
	for _, variant := range draft.Variants {
		if variant.ID != "" && !knownVariants[variant.ID] {
			return domain.Product{}, ErrNotFound
		}
	}
	knownMedia := make(map[ids.MediaID]bool, len(current.Media))
	for _, media := range current.Media {
		knownMedia[media.ID] = true
	}
	for _, media := range draft.Media {
		if media.ID != "" && !knownMedia[media.ID] {
			return domain.Product{}, ErrNotFound
		}
	}
	draft, err = service.completeDraftIDs(draft, true)
	if err != nil {
		return domain.Product{}, err
	}
	replacement, err := current.Replaced(draft, service.now())
	if err != nil {
		return domain.Product{}, err
	}
	updated, err := service.repository.ReplaceProduct(ctx, storeID, productID, expectedVersion, replacement)
	if err != nil {
		return domain.Product{}, fmt.Errorf("replace product: %w", err)
	}
	return updated, nil
}

func (service Service) GetProduct(ctx context.Context, storeID ids.StoreID, productID ids.ProductID) (domain.Product, error) {
	product, err := service.repository.GetProduct(ctx, storeID, productID)
	if err != nil {
		return domain.Product{}, fmt.Errorf("get product: %w", err)
	}
	return product, nil
}

func (service Service) ListProducts(ctx context.Context, storeID ids.StoreID, limit int, encoded string) (Page, error) {
	if limit == 0 {
		limit = 25
	}
	if limit < 1 || limit > MaxPageSize {
		return Page{}, domain.ErrInvalidCatalog
	}
	cursor, err := DecodeCursor(encoded)
	if err != nil {
		return Page{}, domain.ErrInvalidCatalog
	}
	products, err := service.repository.ListProducts(ctx, storeID, limit+1, cursor)
	if err != nil {
		return Page{}, fmt.Errorf("list products: %w", err)
	}
	page := Page{Products: products}
	if len(products) > limit {
		page.Products = products[:limit]
		last := page.Products[len(page.Products)-1]
		page.NextCursor, err = EncodeCursor(Cursor{UpdatedAt: last.UpdatedAt, ProductID: last.ID})
		if err != nil {
			return Page{}, fmt.Errorf("encode product cursor: %w", err)
		}
	}
	return page, nil
}

func (service Service) ArchiveProduct(ctx context.Context, storeID ids.StoreID, productID ids.ProductID, expectedVersion int64) (domain.Product, error) {
	if expectedVersion < 1 {
		return domain.Product{}, domain.ErrInvalidCatalog
	}
	product, err := service.repository.ArchiveProduct(ctx, storeID, productID, expectedVersion, service.now().UTC())
	if err != nil {
		return domain.Product{}, fmt.Errorf("archive product: %w", err)
	}
	return product, nil
}

func (service Service) CreateCategory(ctx context.Context, storeID ids.StoreID, parentID *ids.CategoryID, name, slug string) (domain.Category, error) {
	id, err := service.ids.Category()
	if err != nil {
		return domain.Category{}, fmt.Errorf("generate category ID: %w", err)
	}
	category, err := domain.NewCategory(storeID, id, parentID, name, slug, service.now())
	if err != nil {
		return domain.Category{}, err
	}
	created, err := service.repository.CreateCategory(ctx, storeID, category)
	if err != nil {
		return domain.Category{}, fmt.Errorf("create category: %w", err)
	}
	return created, nil
}

func (service Service) ReplaceCategory(ctx context.Context, storeID ids.StoreID, categoryID ids.CategoryID, expectedVersion int64, parentID *ids.CategoryID, name, slug string) (domain.Category, error) {
	if expectedVersion < 1 {
		return domain.Category{}, domain.ErrInvalidCatalog
	}
	categories, err := service.repository.ListCategories(ctx, storeID)
	if err != nil {
		return domain.Category{}, fmt.Errorf("load categories: %w", err)
	}
	var current *domain.Category
	for index := range categories {
		if categories[index].ID == categoryID {
			current = &categories[index]
			break
		}
	}
	if current == nil {
		return domain.Category{}, ErrNotFound
	}
	replacement, err := current.Replaced(parentID, name, slug, service.now())
	if err != nil {
		return domain.Category{}, err
	}
	updated, err := service.repository.ReplaceCategory(ctx, storeID, categoryID, expectedVersion, replacement)
	if err != nil {
		return domain.Category{}, fmt.Errorf("replace category: %w", err)
	}
	return updated, nil
}

func (service Service) ListCategories(ctx context.Context, storeID ids.StoreID) ([]domain.Category, error) {
	categories, err := service.repository.ListCategories(ctx, storeID)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	return categories, nil
}

func (service Service) ArchiveCategory(ctx context.Context, storeID ids.StoreID, categoryID ids.CategoryID, expectedVersion int64) (domain.Category, error) {
	if expectedVersion < 1 {
		return domain.Category{}, domain.ErrInvalidCatalog
	}
	category, err := service.repository.ArchiveCategory(ctx, storeID, categoryID, expectedVersion, service.now().UTC())
	if err != nil {
		return domain.Category{}, fmt.Errorf("archive category: %w", err)
	}
	return category, nil
}

func (service Service) completeDraftIDs(draft domain.ProductDraft, allowExisting bool) (domain.ProductDraft, error) {
	for index := range draft.Variants {
		if draft.Variants[index].ID != "" && allowExisting {
			continue
		}
		if draft.Variants[index].ID != "" {
			return domain.ProductDraft{}, domain.ErrInvalidCatalog
		}
		id, err := service.ids.Variant()
		if err != nil {
			return domain.ProductDraft{}, fmt.Errorf("generate variant ID: %w", err)
		}
		draft.Variants[index].ID = id
	}
	for index := range draft.Media {
		if draft.Media[index].ID != "" && allowExisting {
			continue
		}
		if draft.Media[index].ID != "" {
			return domain.ProductDraft{}, domain.ErrInvalidCatalog
		}
		id, err := service.ids.Media()
		if err != nil {
			return domain.ProductDraft{}, fmt.Errorf("generate media ID: %w", err)
		}
		draft.Media[index].ID = id
	}
	return draft, nil
}

func EncodeCursor(cursor Cursor) (string, error) {
	if cursor.UpdatedAt.IsZero() || cursor.ProductID == "" {
		return "", domain.ErrInvalidCatalog
	}
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func DecodeCursor(encoded string) (*Cursor, error) {
	if encoded == "" {
		return nil, nil
	}
	if len(encoded) > 512 {
		return nil, domain.ErrInvalidCatalog
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, domain.ErrInvalidCatalog
	}
	var cursor Cursor
	if err := json.Unmarshal(raw, &cursor); err != nil || cursor.UpdatedAt.IsZero() || cursor.ProductID == "" {
		return nil, domain.ErrInvalidCatalog
	}
	if _, err := ids.ParseProductID(cursor.ProductID.String()); err != nil {
		return nil, domain.ErrInvalidCatalog
	}
	return &cursor, nil
}
