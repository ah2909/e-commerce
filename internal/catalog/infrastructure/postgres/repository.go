package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/ah2909/e-commerce/internal/catalog/application"
	"github.com/ah2909/e-commerce/internal/catalog/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

type productRow struct {
	StoreID     string     `gorm:"column:store_id"`
	ProductID   string     `gorm:"column:product_id"`
	Name        string     `gorm:"column:name"`
	Slug        string     `gorm:"column:slug"`
	Description string     `gorm:"column:description"`
	Status      string     `gorm:"column:status"`
	Version     int64      `gorm:"column:version"`
	PublishedAt *time.Time `gorm:"column:published_at"`
	ArchivedAt  *time.Time `gorm:"column:archived_at"`
	CreatedAt   time.Time  `gorm:"column:created_at"`
	UpdatedAt   time.Time  `gorm:"column:updated_at"`
}

func (productRow) TableName() string { return "catalog_products" }

type variantRow struct {
	StoreID      string     `gorm:"column:store_id"`
	VariantID    string     `gorm:"column:variant_id"`
	ProductID    string     `gorm:"column:product_id"`
	SKU          string     `gorm:"column:sku"`
	Barcode      *string    `gorm:"column:barcode"`
	OptionValues []byte     `gorm:"column:option_values;type:jsonb"`
	WeightGrams  int64      `gorm:"column:weight_grams"`
	LengthMM     *int64     `gorm:"column:length_mm"`
	WidthMM      *int64     `gorm:"column:width_mm"`
	HeightMM     *int64     `gorm:"column:height_mm"`
	Status       string     `gorm:"column:status"`
	Version      int64      `gorm:"column:version"`
	CreatedAt    time.Time  `gorm:"column:created_at"`
	UpdatedAt    time.Time  `gorm:"column:updated_at"`
	ArchivedAt   *time.Time `gorm:"column:archived_at"`
}

func (variantRow) TableName() string { return "catalog_variants" }

type categoryRow struct {
	StoreID    string     `gorm:"column:store_id"`
	CategoryID string     `gorm:"column:category_id"`
	ParentID   *string    `gorm:"column:parent_id"`
	Name       string     `gorm:"column:name"`
	Slug       string     `gorm:"column:slug"`
	Status     string     `gorm:"column:status"`
	Version    int64      `gorm:"column:version"`
	CreatedAt  time.Time  `gorm:"column:created_at"`
	UpdatedAt  time.Time  `gorm:"column:updated_at"`
	ArchivedAt *time.Time `gorm:"column:archived_at"`
}

func (categoryRow) TableName() string { return "catalog_categories" }

type productCategoryRow struct {
	StoreID    string `gorm:"column:store_id"`
	ProductID  string `gorm:"column:product_id"`
	CategoryID string `gorm:"column:category_id"`
}

func (productCategoryRow) TableName() string { return "catalog_product_categories" }

type mediaRow struct {
	StoreID    string     `gorm:"column:store_id"`
	MediaID    string     `gorm:"column:media_id"`
	ProductID  string     `gorm:"column:product_id"`
	ObjectKey  string     `gorm:"column:object_key"`
	AltText    string     `gorm:"column:alt_text"`
	Position   int        `gorm:"column:position"`
	CreatedAt  time.Time  `gorm:"column:created_at"`
	UpdatedAt  time.Time  `gorm:"column:updated_at"`
	ArchivedAt *time.Time `gorm:"column:archived_at"`
}

func (mediaRow) TableName() string { return "catalog_media" }

func (repository *Repository) CreateProduct(ctx context.Context, storeID ids.StoreID, product domain.Product) (domain.Product, error) {
	var created domain.Product
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if product.StoreID != storeID {
			return application.ErrNotFound
		}
		if err := lockHierarchy(tx, storeID); err != nil {
			return err
		}
		if err := validateCategories(tx, storeID, product.CategoryIDs); err != nil {
			return err
		}
		if err := tx.Create(toProductRow(product)).Error; err != nil {
			return classify(err)
		}
		if err := replaceChildren(tx, storeID, product, false); err != nil {
			return err
		}
		var err error
		created, err = loadProduct(tx, storeID, product.ID)
		return err
	})
	if err != nil {
		return domain.Product{}, classify(err)
	}
	return created, nil
}

func (repository *Repository) ReplaceProduct(ctx context.Context, storeID ids.StoreID, productID ids.ProductID, expectedVersion int64, product domain.Product) (domain.Product, error) {
	var updated domain.Product
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if product.StoreID != storeID || product.ID != productID {
			return application.ErrNotFound
		}
		if err := lockHierarchy(tx, storeID); err != nil {
			return err
		}
		if err := validateCategories(tx, storeID, product.CategoryIDs); err != nil {
			return err
		}
		result := tx.Model(&productRow{}).Where("store_id = ? AND product_id = ? AND version = ? AND status = 'draft'", storeID.String(), productID.String(), expectedVersion).Updates(map[string]any{"name": product.Name, "slug": product.Slug, "description": product.Description, "version": expectedVersion + 1, "updated_at": product.UpdatedAt})
		if result.Error != nil {
			return classify(result.Error)
		}
		if result.RowsAffected != 1 {
			return distinguishProduct(tx, storeID, productID)
		}
		if err := replaceChildren(tx, storeID, product, true); err != nil {
			return err
		}
		var err error
		updated, err = loadProduct(tx, storeID, productID)
		return err
	})
	if err != nil {
		return domain.Product{}, classify(err)
	}
	return updated, nil
}

func (repository *Repository) GetProduct(ctx context.Context, storeID ids.StoreID, productID ids.ProductID) (domain.Product, error) {
	var product domain.Product
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		loaded, err := loadProduct(tx, storeID, productID)
		product = loaded
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return product, err
}

func (repository *Repository) ListProducts(ctx context.Context, storeID ids.StoreID, limit int, cursor *application.Cursor) ([]domain.Product, error) {
	products := make([]domain.Product, 0)
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Where("store_id = ?", storeID.String())
		if cursor != nil {
			var anchors int64
			if err := tx.Model(&productRow{}).Where("store_id = ? AND product_id = ? AND updated_at = ?", storeID.String(), cursor.ProductID.String(), cursor.UpdatedAt).Count(&anchors).Error; err != nil {
				return fmt.Errorf("validate product cursor: %w", err)
			}
			if anchors != 1 {
				return application.ErrNotFound
			}
			query = query.Where("(updated_at, product_id) < (?, ?::uuid)", cursor.UpdatedAt, cursor.ProductID.String())
		}
		var rows []productRow
		if err := query.Order("updated_at DESC, product_id DESC").Limit(limit).Find(&rows).Error; err != nil {
			return fmt.Errorf("list product rows: %w", err)
		}
		var err error
		products, err = loadProducts(tx, storeID, rows)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return products, err
}

func loadProducts(tx *gorm.DB, storeID ids.StoreID, rows []productRow) ([]domain.Product, error) {
	products := make([]domain.Product, len(rows))
	indexes := make(map[string]int, len(rows))
	productIDs := make([]string, len(rows))
	for index, row := range rows {
		id, err := ids.ParseProductID(row.ProductID)
		if err != nil {
			return nil, err
		}
		products[index] = domain.Product{StoreID: storeID, ID: id, Name: row.Name, Slug: row.Slug, Description: row.Description, Status: domain.ProductStatus(row.Status), Version: row.Version, PublishedAt: row.PublishedAt, ArchivedAt: row.ArchivedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Variants: make([]domain.Variant, 0), CategoryIDs: make([]ids.CategoryID, 0), Media: make([]domain.MediaReference, 0)}
		indexes[row.ProductID], productIDs[index] = index, row.ProductID
	}
	if len(productIDs) == 0 {
		return products, nil
	}
	var variants []variantRow
	if err := tx.Where("store_id = ? AND product_id IN ?", storeID.String(), productIDs).Order("product_id, created_at, variant_id").Find(&variants).Error; err != nil {
		return nil, fmt.Errorf("load public variants: %w", err)
	}
	for _, value := range variants {
		id, err := ids.ParseVariantID(value.VariantID)
		if err != nil {
			return nil, err
		}
		options := map[string]string{}
		if err := json.Unmarshal(value.OptionValues, &options); err != nil {
			return nil, err
		}
		productID := products[indexes[value.ProductID]].ID
		products[indexes[value.ProductID]].Variants = append(products[indexes[value.ProductID]].Variants, domain.Variant{StoreID: storeID, ID: id, ProductID: productID, SKU: value.SKU, Barcode: value.Barcode, OptionValues: options, WeightGrams: value.WeightGrams, Dimensions: domain.Dimensions{LengthMM: value.LengthMM, WidthMM: value.WidthMM, HeightMM: value.HeightMM}, Status: domain.ItemStatus(value.Status), Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, ArchivedAt: value.ArchivedAt})
	}
	var assignments []productCategoryRow
	if err := tx.Where("store_id = ? AND product_id IN ?", storeID.String(), productIDs).Order("product_id, category_id").Find(&assignments).Error; err != nil {
		return nil, fmt.Errorf("load public categories: %w", err)
	}
	for _, value := range assignments {
		id, err := ids.ParseCategoryID(value.CategoryID)
		if err != nil {
			return nil, err
		}
		products[indexes[value.ProductID]].CategoryIDs = append(products[indexes[value.ProductID]].CategoryIDs, id)
	}
	var media []mediaRow
	if err := tx.Where("store_id = ? AND product_id IN ? AND archived_at IS NULL", storeID.String(), productIDs).Order("product_id, position").Find(&media).Error; err != nil {
		return nil, fmt.Errorf("load public media: %w", err)
	}
	for _, value := range media {
		id, err := ids.ParseMediaID(value.MediaID)
		if err != nil {
			return nil, err
		}
		productID := products[indexes[value.ProductID]].ID
		products[indexes[value.ProductID]].Media = append(products[indexes[value.ProductID]].Media, domain.MediaReference{StoreID: storeID, ID: id, ProductID: productID, ObjectKey: value.ObjectKey, AltText: value.AltText, Position: value.Position, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, ArchivedAt: value.ArchivedAt})
	}
	return products, nil
}

func (repository *Repository) ArchiveProduct(ctx context.Context, storeID ids.StoreID, productID ids.ProductID, expectedVersion int64, now time.Time) (domain.Product, error) {
	var archived domain.Product
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockHierarchy(tx, storeID); err != nil {
			return err
		}
		result := tx.Model(&productRow{}).Where("store_id = ? AND product_id = ? AND version = ? AND status = 'draft'", storeID.String(), productID.String(), expectedVersion).Updates(map[string]any{"status": "archived", "archived_at": now, "updated_at": now, "version": expectedVersion + 1})
		if result.Error != nil {
			return classify(result.Error)
		}
		if result.RowsAffected != 1 {
			return distinguishProduct(tx, storeID, productID)
		}
		if err := tx.Model(&variantRow{}).Where("store_id = ? AND product_id = ? AND status = 'active'", storeID.String(), productID.String()).Updates(map[string]any{"status": "archived", "archived_at": now, "updated_at": now, "version": gorm.Expr("version + 1")}).Error; err != nil {
			return fmt.Errorf("archive product variants: %w", err)
		}
		if err := tx.Model(&mediaRow{}).Where("store_id = ? AND product_id = ? AND archived_at IS NULL", storeID.String(), productID.String()).Updates(map[string]any{"archived_at": now, "updated_at": now}).Error; err != nil {
			return fmt.Errorf("archive product media: %w", err)
		}
		var err error
		archived, err = loadProduct(tx, storeID, productID)
		return err
	})
	if err != nil {
		return domain.Product{}, err
	}
	return archived, nil
}

func (repository *Repository) GetPublishedBySlug(ctx context.Context, storeID ids.StoreID, slug string) (domain.Product, error) {
	var product domain.Product
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row productRow
		if err := tx.Where("store_id = ? AND slug = ? AND status = 'published'", storeID.String(), slug).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return application.ErrNotFound
		} else if err != nil {
			return err
		}
		var err error
		product, err = loadProduct(tx, storeID, ids.ProductID(row.ProductID))
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return product, err
}

func (repository *Repository) ListPublished(ctx context.Context, storeID ids.StoreID, limit int, cursor *application.PublishedCursor) ([]domain.Product, error) {
	products := make([]domain.Product, 0)
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Where("store_id = ? AND status = 'published'", storeID.String())
		if cursor != nil {
			var anchors int64
			if err := tx.Model(&productRow{}).Where("store_id = ? AND product_id = ? AND published_at = ? AND status = 'published'", storeID.String(), cursor.ProductID.String(), cursor.PublishedAt).Count(&anchors).Error; err != nil {
				return err
			}
			if anchors != 1 {
				return application.ErrNotFound
			}
			query = query.Where("(published_at, product_id) < (?, ?::uuid)", cursor.PublishedAt, cursor.ProductID.String())
		}
		var rows []productRow
		if err := query.Order("published_at DESC, product_id DESC").Limit(limit).Find(&rows).Error; err != nil {
			return fmt.Errorf("list published products: %w", err)
		}
		var err error
		products, err = loadProducts(tx, storeID, rows)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return products, err
}

func (repository *Repository) LockActiveVariants(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, variantIDs []ids.VariantID) (bool, error) {
	values := make([]string, len(variantIDs))
	for index, id := range variantIDs {
		values[index] = id.String()
	}
	sort.Strings(values)
	var rows []variantRow
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where("store_id = ? AND variant_id IN ? AND status = 'active'", storeID.String(), values).Order("variant_id").Find(&rows).Error; err != nil {
		return false, fmt.Errorf("lock active catalog variants: %w", err)
	}
	if len(rows) != len(values) {
		return false, nil
	}
	return true, nil
}

func (repository *Repository) CreateCategory(ctx context.Context, storeID ids.StoreID, category domain.Category) (domain.Category, error) {
	var created domain.Category
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if category.StoreID != storeID {
			return application.ErrNotFound
		}
		if err := lockHierarchy(tx, storeID); err != nil {
			return err
		}
		if err := validateParent(tx, storeID, category.ID, category.ParentID); err != nil {
			return err
		}
		if err := classify(tx.Create(toCategoryRow(category)).Error); err != nil {
			return err
		}
		var err error
		created, err = loadCategory(tx, storeID, category.ID)
		return err
	})
	if err != nil {
		return domain.Category{}, err
	}
	return created, nil
}

func (repository *Repository) ReplaceCategory(ctx context.Context, storeID ids.StoreID, categoryID ids.CategoryID, expectedVersion int64, category domain.Category) (domain.Category, error) {
	var updated domain.Category
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockHierarchy(tx, storeID); err != nil {
			return err
		}
		var locked categoryRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id = ? AND category_id = ?", storeID.String(), categoryID.String()).First(&locked).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return application.ErrNotFound
		} else if err != nil {
			return fmt.Errorf("lock category: %w", err)
		}
		if locked.Version != expectedVersion || locked.Status != "active" {
			return application.ErrConflict
		}
		if err := validateParent(tx, storeID, categoryID, category.ParentID); err != nil {
			return err
		}
		result := tx.Model(&categoryRow{}).Where("store_id = ? AND category_id = ? AND version = ? AND status = 'active'", storeID.String(), categoryID.String(), expectedVersion).Updates(map[string]any{"parent_id": stringPointer(category.ParentID), "name": category.Name, "slug": category.Slug, "updated_at": category.UpdatedAt, "version": expectedVersion + 1})
		if result.Error != nil {
			return classify(result.Error)
		}
		if result.RowsAffected != 1 {
			return application.ErrConflict
		}
		var err error
		updated, err = loadCategory(tx, storeID, categoryID)
		return err
	})
	if err != nil {
		return domain.Category{}, err
	}
	return updated, nil
}

func (repository *Repository) ListCategories(ctx context.Context, storeID ids.StoreID) ([]domain.Category, error) {
	var rows []categoryRow
	if err := repository.db.WithContext(ctx).Where("store_id = ?", storeID.String()).Order("slug, category_id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	result := make([]domain.Category, 0, len(rows))
	for _, row := range rows {
		category, err := restoreCategory(row)
		if err != nil {
			return nil, err
		}
		result = append(result, category)
	}
	return result, nil
}

func (repository *Repository) ArchiveCategory(ctx context.Context, storeID ids.StoreID, categoryID ids.CategoryID, expectedVersion int64, now time.Time) (domain.Category, error) {
	var archived domain.Category
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockHierarchy(tx, storeID); err != nil {
			return err
		}
		var blockers int64
		if err := tx.Raw(`SELECT (SELECT count(*) FROM catalog_categories WHERE store_id=? AND parent_id=? AND status='active') + (SELECT count(*) FROM catalog_product_categories pc JOIN catalog_products p ON p.store_id=pc.store_id AND p.product_id=pc.product_id WHERE pc.store_id=? AND pc.category_id=? AND p.status<>'archived')`, storeID.String(), categoryID.String(), storeID.String(), categoryID.String()).Scan(&blockers).Error; err != nil {
			return fmt.Errorf("check category use: %w", err)
		}
		if blockers > 0 {
			return application.ErrConflict
		}
		result := tx.Model(&categoryRow{}).Where("store_id = ? AND category_id = ? AND version = ? AND status = 'active'", storeID.String(), categoryID.String(), expectedVersion).Updates(map[string]any{"status": "archived", "archived_at": now, "updated_at": now, "version": expectedVersion + 1})
		if result.Error != nil {
			return classify(result.Error)
		}
		if result.RowsAffected != 1 {
			return distinguishCategory(tx, storeID, categoryID)
		}
		var err error
		archived, err = loadCategory(tx, storeID, categoryID)
		return err
	})
	if err != nil {
		return domain.Category{}, err
	}
	return archived, nil
}

func replaceChildren(tx *gorm.DB, storeID ids.StoreID, product domain.Product, replacing bool) error {
	if replacing {
		if err := tx.Exec("SET CONSTRAINTS catalog_variants_store_sku_unique, catalog_variants_store_barcode_unique DEFERRED").Error; err != nil {
			return fmt.Errorf("defer variant uniqueness: %w", err)
		}
		if err := tx.Model(&mediaRow{}).Where("store_id = ? AND product_id = ? AND archived_at IS NULL", storeID.String(), product.ID.String()).Updates(map[string]any{"archived_at": product.UpdatedAt, "updated_at": product.UpdatedAt}).Error; err != nil {
			return fmt.Errorf("prepare media replacement: %w", err)
		}
	}
	variantIDs := make([]string, 0, len(product.Variants))
	for _, variant := range product.Variants {
		encoded, err := json.Marshal(variant.OptionValues)
		if err != nil {
			return fmt.Errorf("encode variant options: %w", err)
		}
		row := variantRow{StoreID: storeID.String(), VariantID: variant.ID.String(), ProductID: product.ID.String(), SKU: variant.SKU, Barcode: variant.Barcode, OptionValues: encoded, WeightGrams: variant.WeightGrams, LengthMM: variant.Dimensions.LengthMM, WidthMM: variant.Dimensions.WidthMM, HeightMM: variant.Dimensions.HeightMM, Status: "active", Version: 1, CreatedAt: variant.CreatedAt, UpdatedAt: product.UpdatedAt}
		result := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "store_id"}, {Name: "variant_id"}},
			DoUpdates: clause.Assignments(map[string]any{"sku": row.SKU, "barcode": row.Barcode, "option_values": row.OptionValues, "weight_grams": row.WeightGrams, "length_mm": row.LengthMM, "width_mm": row.WidthMM, "height_mm": row.HeightMM, "status": "active", "archived_at": nil, "updated_at": row.UpdatedAt, "version": gorm.Expr("catalog_variants.version + 1")}),
			Where:     clause.Where{Exprs: []clause.Expression{clause.Eq{Column: clause.Column{Table: "catalog_variants", Name: "product_id"}, Value: product.ID.String()}}},
		}).Create(&row)
		if result.Error != nil {
			return classify(result.Error)
		}
		if result.RowsAffected != 1 {
			return application.ErrNotFound
		}
		variantIDs = append(variantIDs, variant.ID.String())
	}
	if replacing {
		if err := tx.Model(&variantRow{}).Where("store_id = ? AND product_id = ? AND status = 'active' AND variant_id NOT IN ?", storeID.String(), product.ID.String(), variantIDs).Updates(map[string]any{"status": "archived", "archived_at": product.UpdatedAt, "updated_at": product.UpdatedAt, "version": gorm.Expr("version + 1")}).Error; err != nil {
			return fmt.Errorf("archive omitted variants: %w", err)
		}
	}
	if err := tx.Where("store_id = ? AND product_id = ?", storeID.String(), product.ID.String()).Delete(&productCategoryRow{}).Error; err != nil {
		return fmt.Errorf("replace product categories: %w", err)
	}
	for _, categoryID := range product.CategoryIDs {
		if err := tx.Create(&productCategoryRow{StoreID: storeID.String(), ProductID: product.ID.String(), CategoryID: categoryID.String()}).Error; err != nil {
			return classify(err)
		}
	}
	for _, media := range product.Media {
		row := mediaRow{StoreID: storeID.String(), MediaID: media.ID.String(), ProductID: product.ID.String(), ObjectKey: media.ObjectKey, AltText: media.AltText, Position: media.Position, CreatedAt: media.CreatedAt, UpdatedAt: product.UpdatedAt}
		result := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "store_id"}, {Name: "media_id"}},
			DoUpdates: clause.Assignments(map[string]any{"object_key": row.ObjectKey, "alt_text": row.AltText, "position": row.Position, "archived_at": nil, "updated_at": row.UpdatedAt}),
			Where:     clause.Where{Exprs: []clause.Expression{clause.Eq{Column: clause.Column{Table: "catalog_media", Name: "product_id"}, Value: product.ID.String()}}},
		}).Create(&row)
		if result.Error != nil {
			return classify(result.Error)
		}
		if result.RowsAffected != 1 {
			return application.ErrNotFound
		}
	}
	return nil
}

func lockHierarchy(tx *gorm.DB, storeID ids.StoreID) error {
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", storeID.String()).Error; err != nil {
		return fmt.Errorf("lock category hierarchy: %w", err)
	}
	return nil
}

func loadProduct(tx *gorm.DB, storeID ids.StoreID, productID ids.ProductID) (domain.Product, error) {
	var row productRow
	if err := tx.Where("store_id = ? AND product_id = ?", storeID.String(), productID.String()).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Product{}, application.ErrNotFound
	} else if err != nil {
		return domain.Product{}, fmt.Errorf("load product row: %w", err)
	}
	product := domain.Product{StoreID: storeID, ID: productID, Name: row.Name, Slug: row.Slug, Description: row.Description, Status: domain.ProductStatus(row.Status), Version: row.Version, PublishedAt: row.PublishedAt, ArchivedAt: row.ArchivedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, Variants: make([]domain.Variant, 0), CategoryIDs: make([]ids.CategoryID, 0), Media: make([]domain.MediaReference, 0)}
	var variants []variantRow
	if err := tx.Where("store_id = ? AND product_id = ?", storeID.String(), productID.String()).Order("created_at, variant_id").Find(&variants).Error; err != nil {
		return domain.Product{}, fmt.Errorf("load variants: %w", err)
	}
	for _, value := range variants {
		id, err := ids.ParseVariantID(value.VariantID)
		if err != nil {
			return domain.Product{}, fmt.Errorf("restore variant ID: %w", err)
		}
		options := map[string]string{}
		if err := json.Unmarshal(value.OptionValues, &options); err != nil {
			return domain.Product{}, fmt.Errorf("restore variant options: %w", err)
		}
		product.Variants = append(product.Variants, domain.Variant{StoreID: storeID, ID: id, ProductID: productID, SKU: value.SKU, Barcode: value.Barcode, OptionValues: options, WeightGrams: value.WeightGrams, Dimensions: domain.Dimensions{LengthMM: value.LengthMM, WidthMM: value.WidthMM, HeightMM: value.HeightMM}, Status: domain.ItemStatus(value.Status), Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, ArchivedAt: value.ArchivedAt})
	}
	var assignments []productCategoryRow
	if err := tx.Where("store_id = ? AND product_id = ?", storeID.String(), productID.String()).Order("category_id").Find(&assignments).Error; err != nil {
		return domain.Product{}, fmt.Errorf("load product categories: %w", err)
	}
	for _, value := range assignments {
		id, err := ids.ParseCategoryID(value.CategoryID)
		if err != nil {
			return domain.Product{}, err
		}
		product.CategoryIDs = append(product.CategoryIDs, id)
	}
	var media []mediaRow
	if err := tx.Where("store_id = ? AND product_id = ? AND archived_at IS NULL", storeID.String(), productID.String()).Order("position").Find(&media).Error; err != nil {
		return domain.Product{}, fmt.Errorf("load media: %w", err)
	}
	for _, value := range media {
		id, err := ids.ParseMediaID(value.MediaID)
		if err != nil {
			return domain.Product{}, err
		}
		product.Media = append(product.Media, domain.MediaReference{StoreID: storeID, ID: id, ProductID: productID, ObjectKey: value.ObjectKey, AltText: value.AltText, Position: value.Position, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, ArchivedAt: value.ArchivedAt})
	}
	return product, nil
}

func validateCategories(tx *gorm.DB, storeID ids.StoreID, categoryIDs []ids.CategoryID) error {
	if len(categoryIDs) == 0 {
		return nil
	}
	values := make([]string, len(categoryIDs))
	for index, id := range categoryIDs {
		values[index] = id.String()
	}
	var count int64
	if err := tx.Model(&categoryRow{}).Where("store_id = ? AND category_id IN ? AND status = 'active'", storeID.String(), values).Count(&count).Error; err != nil {
		return fmt.Errorf("validate product categories: %w", err)
	}
	if count != int64(len(values)) {
		return application.ErrNotFound
	}
	return nil
}

func validateParent(tx *gorm.DB, storeID ids.StoreID, categoryID ids.CategoryID, parentID *ids.CategoryID) error {
	if parentID == nil {
		return nil
	}
	var cycle bool
	var depth int
	err := tx.Raw(`WITH RECURSIVE ancestors AS (SELECT category_id,parent_id,1 AS depth FROM catalog_categories WHERE store_id=? AND category_id=? AND status='active' UNION ALL SELECT c.category_id,c.parent_id,a.depth+1 FROM catalog_categories c JOIN ancestors a ON c.category_id=a.parent_id WHERE c.store_id=? AND c.status='active' AND a.depth<9) SELECT COALESCE(bool_or(category_id=?),false),COALESCE(max(depth),0) FROM ancestors`, storeID.String(), parentID.String(), storeID.String(), categoryID.String()).Row().Scan(&cycle, &depth)
	if err != nil {
		return fmt.Errorf("validate category parent: %w", err)
	}
	if cycle || depth == 0 || depth >= 8 {
		return application.ErrConflict
	}
	return nil
}

func loadCategory(tx *gorm.DB, storeID ids.StoreID, categoryID ids.CategoryID) (domain.Category, error) {
	var row categoryRow
	if err := tx.Where("store_id = ? AND category_id = ?", storeID.String(), categoryID.String()).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Category{}, application.ErrNotFound
	} else if err != nil {
		return domain.Category{}, fmt.Errorf("get category: %w", err)
	}
	return restoreCategory(row)
}

func restoreCategory(row categoryRow) (domain.Category, error) {
	storeID, err := ids.ParseStoreID(row.StoreID)
	if err != nil {
		return domain.Category{}, err
	}
	id, err := ids.ParseCategoryID(row.CategoryID)
	if err != nil {
		return domain.Category{}, err
	}
	var parent *ids.CategoryID
	if row.ParentID != nil {
		parsed, err := ids.ParseCategoryID(*row.ParentID)
		if err != nil {
			return domain.Category{}, err
		}
		parent = &parsed
	}
	return domain.Category{StoreID: storeID, ID: id, ParentID: parent, Name: row.Name, Slug: row.Slug, Status: domain.ItemStatus(row.Status), Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ArchivedAt: row.ArchivedAt}, nil
}

func toProductRow(product domain.Product) *productRow {
	return &productRow{StoreID: product.StoreID.String(), ProductID: product.ID.String(), Name: product.Name, Slug: product.Slug, Description: product.Description, Status: string(product.Status), Version: product.Version, PublishedAt: product.PublishedAt, ArchivedAt: product.ArchivedAt, CreatedAt: product.CreatedAt, UpdatedAt: product.UpdatedAt}
}
func toCategoryRow(category domain.Category) *categoryRow {
	return &categoryRow{StoreID: category.StoreID.String(), CategoryID: category.ID.String(), ParentID: stringPointer(category.ParentID), Name: category.Name, Slug: category.Slug, Status: string(category.Status), Version: category.Version, CreatedAt: category.CreatedAt, UpdatedAt: category.UpdatedAt, ArchivedAt: category.ArchivedAt}
}
func stringPointer(value *ids.CategoryID) *string {
	if value == nil {
		return nil
	}
	result := value.String()
	return &result
}

func distinguishProduct(tx *gorm.DB, storeID ids.StoreID, productID ids.ProductID) error {
	var count int64
	if err := tx.Model(&productRow{}).Where("store_id = ? AND product_id = ?", storeID.String(), productID.String()).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return application.ErrNotFound
	}
	return application.ErrConflict
}
func distinguishCategory(tx *gorm.DB, storeID ids.StoreID, categoryID ids.CategoryID) error {
	var count int64
	if err := tx.Model(&categoryRow{}).Where("store_id = ? AND category_id = ?", storeID.String(), categoryID.String()).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return application.ErrNotFound
	}
	return application.ErrConflict
}

type stateError interface{ SQLState() string }

func classify(err error) error {
	if err == nil {
		return nil
	}
	var state stateError
	if errors.As(err, &state) && (state.SQLState() == "23505" || state.SQLState() == "23503" || state.SQLState() == "23514") {
		return application.ErrConflict
	}
	if errors.Is(err, sql.ErrNoRows) {
		return application.ErrNotFound
	}
	return err
}
