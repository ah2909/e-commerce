package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ah2909/e-commerce/internal/catalog/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shared/money"
)

var (
	ErrNotFound = errors.New("catalog resource not found")
	ErrConflict = errors.New("catalog conflict")
)

const MaxPageSize = 100
const publicScanBatchSize = 50
const publicMaxScanBatches = 3

type Cursor struct {
	UpdatedAt time.Time     `json:"updated_at"`
	ProductID ids.ProductID `json:"product_id"`
}

type Page struct {
	Products   []domain.Product
	NextCursor string
}

type PublishedCursor struct {
	PublishedAt time.Time     `json:"published_at"`
	ProductID   ids.ProductID `json:"product_id"`
}

type PublicPrice struct {
	AmountMinor  int64  `json:"amount_minor"`
	Currency     string `json:"currency"`
	VATInclusive bool   `json:"vat_inclusive"`
}

type PublicVariant struct {
	VariantID    ids.VariantID     `json:"variant_id"`
	SKU          string            `json:"sku"`
	OptionValues map[string]string `json:"option_values"`
	WeightGrams  int64             `json:"weight_grams"`
	Dimensions   domain.Dimensions `json:"dimensions"`
	Price        PublicPrice       `json:"price"`
	InStock      bool              `json:"in_stock"`
}

type PublicMedia struct {
	ObjectKey string `json:"object_key"`
	AltText   string `json:"alt_text"`
	Position  int    `json:"position"`
}

type PublicProduct struct {
	ProductID   ids.ProductID   `json:"product_id"`
	Name        string          `json:"name"`
	Slug        string          `json:"slug"`
	Description string          `json:"description"`
	PublishedAt time.Time       `json:"published_at"`
	Variants    []PublicVariant `json:"variants"`
	Media       []PublicMedia   `json:"media"`
}

type PublicPage struct {
	Products   []PublicProduct
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
	GetPublishedBySlug(context.Context, ids.StoreID, string) (domain.Product, error)
	ListPublished(context.Context, ids.StoreID, int, *PublishedCursor) ([]domain.Product, error)
}

type Publisher interface {
	Publish(context.Context, ids.StoreID, ids.ProductID, int64, string) (domain.Product, error)
}

type PriceResolver interface {
	ResolveCurrent(context.Context, ids.StoreID, []ids.VariantID) (map[ids.VariantID]money.Money, error)
}

type AvailabilityResolver interface {
	ResolveAvailability(context.Context, ids.StoreID, []ids.VariantID) (map[ids.VariantID]bool, error)
}

type IDGenerator struct {
	Product  func() (ids.ProductID, error)
	Variant  func() (ids.VariantID, error)
	Category func() (ids.CategoryID, error)
	Media    func() (ids.MediaID, error)
}

type Service struct {
	repository   Repository
	ids          IDGenerator
	now          func() time.Time
	publisher    Publisher
	prices       PriceResolver
	availability AvailabilityResolver
}

func NewService(repository Repository, generator IDGenerator, now func() time.Time) Service {
	return Service{repository: repository, ids: generator, now: now}
}

func (service Service) WithPublication(publisher Publisher, prices PriceResolver) Service {
	service.publisher, service.prices = publisher, prices
	return service
}

func (service Service) WithAvailability(resolver AvailabilityResolver) Service {
	service.availability = resolver
	return service
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

func (service Service) PublishProduct(ctx context.Context, storeID ids.StoreID, productID ids.ProductID, expectedVersion int64, correlationID string) (domain.Product, error) {
	if service.publisher == nil || expectedVersion < 1 || strings.TrimSpace(correlationID) == "" {
		return domain.Product{}, domain.ErrInvalidCatalog
	}
	product, err := service.publisher.Publish(ctx, storeID, productID, expectedVersion, correlationID)
	if err != nil {
		return domain.Product{}, fmt.Errorf("publish product: %w", err)
	}
	return product, nil
}

func (service Service) GetPublicProduct(ctx context.Context, storeID ids.StoreID, slug string) (PublicProduct, error) {
	if service.prices == nil {
		return PublicProduct{}, ErrNotFound
	}
	product, err := service.repository.GetPublishedBySlug(ctx, storeID, strings.ToLower(strings.TrimSpace(slug)))
	if err != nil {
		return PublicProduct{}, err
	}
	prices, err := service.prices.ResolveCurrent(ctx, storeID, activeVariantIDs([]domain.Product{product}))
	if err != nil {
		return PublicProduct{}, fmt.Errorf("resolve public prices: %w", err)
	}
	availability, err := service.resolveAvailability(ctx, storeID, []domain.Product{product})
	if err != nil {
		return PublicProduct{}, err
	}
	projected, ok := publicProduct(product, prices, availability)
	if !ok {
		return PublicProduct{}, ErrNotFound
	}
	return projected, nil
}

func (service Service) ListPublicProducts(ctx context.Context, storeID ids.StoreID, limit int, encoded string) (PublicPage, error) {
	if service.prices == nil {
		return PublicPage{}, ErrNotFound
	}
	if limit == 0 {
		limit = 25
	}
	if limit < 1 || limit > MaxPageSize {
		return PublicPage{}, domain.ErrInvalidCatalog
	}
	cursor, err := decodePublishedCursor(encoded)
	if err != nil {
		return PublicPage{}, domain.ErrInvalidCatalog
	}
	page := PublicPage{Products: make([]PublicProduct, 0, limit)}
	for batch := 0; batch < publicMaxScanBatches && len(page.Products) < limit; batch++ {
		values, err := service.repository.ListPublished(ctx, storeID, publicScanBatchSize+1, cursor)
		if err != nil {
			return PublicPage{}, fmt.Errorf("list published products: %w", err)
		}
		if len(values) == 0 {
			break
		}
		hasMore := len(values) > publicScanBatchSize
		if hasMore {
			values = values[:publicScanBatchSize]
		}
		prices, err := service.prices.ResolveCurrent(ctx, storeID, activeVariantIDs(values))
		if err != nil {
			return PublicPage{}, fmt.Errorf("resolve public prices: %w", err)
		}
		availability, err := service.resolveAvailability(ctx, storeID, values)
		if err != nil {
			return PublicPage{}, err
		}
		for index, product := range values {
			cursor = &PublishedCursor{PublishedAt: *product.PublishedAt, ProductID: product.ID}
			if projected, ok := publicProduct(product, prices, availability); ok {
				page.Products = append(page.Products, projected)
			}
			if len(page.Products) == limit {
				if hasMore || index < len(values)-1 {
					page.NextCursor, err = encodePublishedCursor(*cursor)
				}
				return page, err
			}
		}
		if !hasMore {
			break
		}
		if batch == publicMaxScanBatches-1 {
			page.NextCursor, err = encodePublishedCursor(*cursor)
			return page, err
		}
	}
	return page, nil
}

func activeVariantIDs(products []domain.Product) []ids.VariantID {
	result := make([]ids.VariantID, 0)
	for _, product := range products {
		for _, variant := range product.Variants {
			if variant.Status == domain.ItemActive {
				result = append(result, variant.ID)
			}
		}
	}
	return result
}

func publicProduct(product domain.Product, prices map[ids.VariantID]money.Money, availability map[ids.VariantID]bool) (PublicProduct, bool) {
	if product.Status != domain.ProductStatusPublished || product.PublishedAt == nil {
		return PublicProduct{}, false
	}
	result := PublicProduct{ProductID: product.ID, Name: product.Name, Slug: product.Slug, Description: product.Description, PublishedAt: *product.PublishedAt, Variants: make([]PublicVariant, 0), Media: make([]PublicMedia, 0, len(product.Media))}
	for _, variant := range product.Variants {
		price, ok := prices[variant.ID]
		if variant.Status != domain.ItemActive || !ok {
			continue
		}
		projected := PublicVariant{VariantID: variant.ID, SKU: variant.SKU, OptionValues: variant.OptionValues, WeightGrams: variant.WeightGrams, Dimensions: variant.Dimensions, Price: PublicPrice{AmountMinor: price.AmountMinor, Currency: price.Currency, VATInclusive: true}, InStock: availability[variant.ID]}
		result.Variants = append(result.Variants, projected)
	}
	for _, media := range product.Media {
		if media.ArchivedAt == nil {
			result.Media = append(result.Media, PublicMedia{ObjectKey: media.ObjectKey, AltText: media.AltText, Position: media.Position})
		}
	}
	return result, len(result.Variants) > 0
}

func (service Service) resolveAvailability(ctx context.Context, storeID ids.StoreID, products []domain.Product) (map[ids.VariantID]bool, error) {
	if service.availability == nil {
		return nil, nil
	}
	values, err := service.availability.ResolveAvailability(ctx, storeID, activeVariantIDs(products))
	if err != nil {
		return nil, fmt.Errorf("resolve public availability: %w", err)
	}
	return values, nil
}

func encodePublishedCursor(cursor PublishedCursor) (string, error) {
	if cursor.PublishedAt.IsZero() || cursor.ProductID == "" {
		return "", domain.ErrInvalidCatalog
	}
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodePublishedCursor(encoded string) (*PublishedCursor, error) {
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
	var cursor PublishedCursor
	if err := json.Unmarshal(raw, &cursor); err != nil || cursor.PublishedAt.IsZero() || cursor.ProductID == "" {
		return nil, domain.ErrInvalidCatalog
	}
	if _, err := ids.ParseProductID(cursor.ProductID.String()); err != nil {
		return nil, domain.ErrInvalidCatalog
	}
	return &cursor, nil
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
