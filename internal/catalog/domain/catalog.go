package domain

import (
	"errors"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"golang.org/x/text/unicode/norm"
)

var ErrInvalidCatalog = errors.New("invalid catalog data")
var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type ProductStatus string
type ItemStatus string

const (
	ProductStatusDraft     ProductStatus = "draft"
	ProductStatusPublished ProductStatus = "published"
	ProductStatusArchived  ProductStatus = "archived"
	ItemActive             ItemStatus    = "active"
	ItemArchived           ItemStatus    = "archived"
)

type Dimensions struct {
	LengthMM *int64 `json:"length_mm"`
	WidthMM  *int64 `json:"width_mm"`
	HeightMM *int64 `json:"height_mm"`
}

type Variant struct {
	StoreID      ids.StoreID       `json:"store_id"`
	ID           ids.VariantID     `json:"variant_id"`
	ProductID    ids.ProductID     `json:"product_id"`
	SKU          string            `json:"sku"`
	Barcode      *string           `json:"barcode"`
	OptionValues map[string]string `json:"option_values"`
	WeightGrams  int64             `json:"weight_grams"`
	Dimensions   Dimensions        `json:"dimensions"`
	Status       ItemStatus        `json:"status"`
	Version      int64             `json:"version"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	ArchivedAt   *time.Time        `json:"archived_at"`
}

type MediaReference struct {
	StoreID    ids.StoreID   `json:"store_id"`
	ID         ids.MediaID   `json:"media_id"`
	ProductID  ids.ProductID `json:"product_id"`
	ObjectKey  string        `json:"object_key"`
	AltText    string        `json:"alt_text"`
	Position   int           `json:"position"`
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
	ArchivedAt *time.Time    `json:"archived_at"`
}

type Product struct {
	StoreID     ids.StoreID      `json:"store_id"`
	ID          ids.ProductID    `json:"product_id"`
	Name        string           `json:"name"`
	Slug        string           `json:"slug"`
	Description string           `json:"description"`
	Status      ProductStatus    `json:"status"`
	Version     int64            `json:"version"`
	PublishedAt *time.Time       `json:"published_at"`
	ArchivedAt  *time.Time       `json:"archived_at"`
	CreatedAt   time.Time        `json:"created_at"`
	UpdatedAt   time.Time        `json:"updated_at"`
	Variants    []Variant        `json:"variants"`
	CategoryIDs []ids.CategoryID `json:"category_ids"`
	Media       []MediaReference `json:"media"`
}

type Category struct {
	StoreID    ids.StoreID     `json:"store_id"`
	ID         ids.CategoryID  `json:"category_id"`
	ParentID   *ids.CategoryID `json:"parent_id"`
	Name       string          `json:"name"`
	Slug       string          `json:"slug"`
	Status     ItemStatus      `json:"status"`
	Version    int64           `json:"version"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
	ArchivedAt *time.Time      `json:"archived_at"`
}

type VariantDraft struct {
	ID           ids.VariantID
	SKU          string
	Barcode      *string
	OptionValues map[string]string
	WeightGrams  int64
	Dimensions   Dimensions
}

type MediaDraft struct {
	ID        ids.MediaID
	ObjectKey string
	AltText   string
	Position  int
}

type ProductDraft struct {
	Name        string
	Slug        string
	Description string
	Variants    []VariantDraft
	CategoryIDs []ids.CategoryID
	Media       []MediaDraft
}

func NewProduct(storeID ids.StoreID, productID ids.ProductID, draft ProductDraft, now time.Time) (Product, error) {
	product := Product{StoreID: storeID, ID: productID, Name: draft.Name, Slug: draft.Slug, Description: draft.Description, Status: ProductStatusDraft, Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	if err := product.replace(draft, now.UTC(), false); err != nil {
		return Product{}, err
	}
	product.Version = 1
	return product, nil
}

func (product Product) Replaced(draft ProductDraft, now time.Time) (Product, error) {
	if product.Status != ProductStatusDraft {
		return Product{}, ErrInvalidCatalog
	}
	if err := product.replace(draft, now.UTC(), true); err != nil {
		return Product{}, err
	}
	return product, nil
}

func (product *Product) replace(draft ProductDraft, now time.Time, increment bool) error {
	name := normalizeText(draft.Name)
	slug := normalizeSlug(draft.Slug)
	description := norm.NFC.String(strings.TrimSpace(draft.Description))
	if product.StoreID == "" || product.ID == "" || !validText(name, 1, 200) || !validSlug(slug, 160) || utf8.RuneCountInString(description) > 10000 || len(draft.Variants) == 0 || len(draft.Variants) > 200 || len(draft.CategoryIDs) > 100 || len(draft.Media) > 100 {
		return ErrInvalidCatalog
	}
	variants := make([]Variant, 0, len(draft.Variants))
	seenVariants := map[ids.VariantID]bool{}
	seenSKUs := map[string]bool{}
	seenBarcodes := map[string]bool{}
	for _, input := range draft.Variants {
		variant, err := newVariant(product.StoreID, product.ID, input, now)
		if err != nil || seenVariants[variant.ID] || seenSKUs[variant.SKU] || (variant.Barcode != nil && seenBarcodes[*variant.Barcode]) {
			return ErrInvalidCatalog
		}
		seenVariants[variant.ID], seenSKUs[variant.SKU] = true, true
		if variant.Barcode != nil {
			seenBarcodes[*variant.Barcode] = true
		}
		variants = append(variants, variant)
	}
	categories := append([]ids.CategoryID(nil), draft.CategoryIDs...)
	sort.Slice(categories, func(i, j int) bool { return categories[i] < categories[j] })
	for index, id := range categories {
		if id == "" || (index > 0 && categories[index-1] == id) {
			return ErrInvalidCatalog
		}
	}
	media := make([]MediaReference, 0, len(draft.Media))
	seenMedia := map[ids.MediaID]bool{}
	seenKeys := map[string]bool{}
	for _, input := range draft.Media {
		item, err := newMedia(product.StoreID, product.ID, input, now)
		if err != nil || seenMedia[item.ID] || seenKeys[item.ObjectKey] {
			return ErrInvalidCatalog
		}
		seenMedia[item.ID], seenKeys[item.ObjectKey] = true, true
		media = append(media, item)
	}
	sort.Slice(media, func(i, j int) bool { return media[i].Position < media[j].Position })
	for index := range media {
		if media[index].Position != index {
			return ErrInvalidCatalog
		}
	}
	product.Name, product.Slug, product.Description = name, slug, description
	product.Variants, product.CategoryIDs, product.Media, product.UpdatedAt = variants, categories, media, now
	if increment {
		product.Version++
	}
	return nil
}

func (product Product) Archived(now time.Time) (Product, error) {
	if product.Status != ProductStatusDraft {
		return Product{}, ErrInvalidCatalog
	}
	archived := now.UTC()
	product.Status, product.ArchivedAt, product.UpdatedAt, product.Version = ProductStatusArchived, &archived, archived, product.Version+1
	for index := range product.Variants {
		product.Variants[index].Status = ItemArchived
		product.Variants[index].ArchivedAt = &archived
		product.Variants[index].UpdatedAt = archived
		product.Variants[index].Version++
	}
	return product, nil
}

func (product Product) Published(now time.Time) (Product, error) {
	if product.Status != ProductStatusDraft {
		return Product{}, ErrInvalidCatalog
	}
	published := now.UTC()
	product.Status, product.PublishedAt, product.UpdatedAt, product.Version = ProductStatusPublished, &published, published, product.Version+1
	return product, nil
}

func NewCategory(storeID ids.StoreID, id ids.CategoryID, parentID *ids.CategoryID, name, slug string, now time.Time) (Category, error) {
	category := Category{StoreID: storeID, ID: id, ParentID: parentID, Name: normalizeText(name), Slug: normalizeSlug(slug), Status: ItemActive, Version: 1, CreatedAt: now.UTC(), UpdatedAt: now.UTC()}
	if err := category.validate(); err != nil {
		return Category{}, err
	}
	return category, nil
}

func (category Category) Replaced(parentID *ids.CategoryID, name, slug string, now time.Time) (Category, error) {
	if category.Status != ItemActive {
		return Category{}, ErrInvalidCatalog
	}
	category.ParentID, category.Name, category.Slug = parentID, normalizeText(name), normalizeSlug(slug)
	category.Version++
	category.UpdatedAt = now.UTC()
	if err := category.validate(); err != nil {
		return Category{}, err
	}
	return category, nil
}

func (category Category) Archived(now time.Time) (Category, error) {
	if category.Status != ItemActive {
		return Category{}, ErrInvalidCatalog
	}
	archived := now.UTC()
	category.Status, category.ArchivedAt, category.UpdatedAt, category.Version = ItemArchived, &archived, archived, category.Version+1
	return category, nil
}

func (category Category) validate() error {
	if category.StoreID == "" || category.ID == "" || !validText(category.Name, 1, 120) || !validSlug(category.Slug, 120) || (category.ParentID != nil && *category.ParentID == category.ID) {
		return ErrInvalidCatalog
	}
	return nil
}

func newVariant(storeID ids.StoreID, productID ids.ProductID, input VariantDraft, now time.Time) (Variant, error) {
	sku := strings.ToUpper(strings.TrimSpace(input.SKU))
	var barcode *string
	if input.Barcode != nil {
		value := strings.TrimSpace(*input.Barcode)
		if value != "" {
			barcode = &value
		}
	}
	if input.ID == "" || len(sku) < 1 || len(sku) > 100 || !printable(sku) || input.WeightGrams < 0 || input.WeightGrams > 1_000_000_000 || !validDimensions(input.Dimensions) || len(input.OptionValues) > 20 {
		return Variant{}, ErrInvalidCatalog
	}
	if barcode != nil && (len(*barcode) > 100 || !printable(*barcode)) {
		return Variant{}, ErrInvalidCatalog
	}
	options := make(map[string]string, len(input.OptionValues))
	for rawKey, rawValue := range input.OptionValues {
		key, value := normalizeText(rawKey), normalizeText(rawValue)
		if !validText(key, 1, 50) || !validText(value, 1, 100) {
			return Variant{}, ErrInvalidCatalog
		}
		canonical := strings.ToLower(key)
		if _, exists := options[canonical]; exists {
			return Variant{}, ErrInvalidCatalog
		}
		options[canonical] = value
	}
	return Variant{StoreID: storeID, ID: input.ID, ProductID: productID, SKU: sku, Barcode: barcode, OptionValues: options, WeightGrams: input.WeightGrams, Dimensions: input.Dimensions, Status: ItemActive, Version: 1, CreatedAt: now, UpdatedAt: now}, nil
}

func newMedia(storeID ids.StoreID, productID ids.ProductID, input MediaDraft, now time.Time) (MediaReference, error) {
	key, alt := strings.TrimSpace(input.ObjectKey), normalizeText(input.AltText)
	clean := path.Clean(key)
	if input.ID == "" || input.Position < 0 || len(key) > 512 || key == "" || clean != key || strings.HasPrefix(key, "/") || strings.Contains(key, "://") || strings.ContainsAny(key, "?#\\") || strings.Contains(key, "..") || !printable(key) || !validText(alt, 1, 500) {
		return MediaReference{}, ErrInvalidCatalog
	}
	return MediaReference{StoreID: storeID, ID: input.ID, ProductID: productID, ObjectKey: key, AltText: alt, Position: input.Position, CreatedAt: now, UpdatedAt: now}, nil
}

func validDimensions(value Dimensions) bool {
	allNil := value.LengthMM == nil && value.WidthMM == nil && value.HeightMM == nil
	allSet := value.LengthMM != nil && value.WidthMM != nil && value.HeightMM != nil
	if !allNil && !allSet {
		return false
	}
	if allSet {
		return *value.LengthMM >= 0 && *value.WidthMM >= 0 && *value.HeightMM >= 0 && *value.LengthMM <= 10_000_000 && *value.WidthMM <= 10_000_000 && *value.HeightMM <= 10_000_000
	}
	return true
}

func normalizeText(value string) string { return norm.NFC.String(strings.TrimSpace(value)) }
func normalizeSlug(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
func validSlug(value string, maximum int) bool {
	return len(value) <= maximum && slugPattern.MatchString(value)
}
func validText(value string, minimum, maximum int) bool {
	count := utf8.RuneCountInString(value)
	return count >= minimum && count <= maximum && printable(value)
}
func printable(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
