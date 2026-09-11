package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ah2909/e-commerce/internal/pricing/application"
	"github.com/ah2909/e-commerce/internal/pricing/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shared/money"
	"gorm.io/gorm"
)

type CatalogVariantLocker interface {
	LockActiveVariants(context.Context, *gorm.DB, ids.StoreID, []ids.VariantID) (bool, error)
}

type Repository struct {
	db      *gorm.DB
	catalog CatalogVariantLocker
}

func NewRepository(db *gorm.DB, catalog CatalogVariantLocker) *Repository {
	return &Repository{db: db, catalog: catalog}
}

type priceListRow struct {
	StoreID     string     `gorm:"column:store_id"`
	PriceListID string     `gorm:"column:price_list_id"`
	Name        string     `gorm:"column:name"`
	Channel     string     `gorm:"column:channel"`
	Currency    string     `gorm:"column:currency"`
	Priority    int        `gorm:"column:priority"`
	ValidFrom   *time.Time `gorm:"column:valid_from"`
	ValidUntil  *time.Time `gorm:"column:valid_until"`
	Status      string     `gorm:"column:status"`
	Version     int64      `gorm:"column:version"`
	CreatedAt   time.Time  `gorm:"column:created_at"`
	UpdatedAt   time.Time  `gorm:"column:updated_at"`
	ArchivedAt  *time.Time `gorm:"column:archived_at"`
}

func (priceListRow) TableName() string { return "pricing_price_lists" }

type priceRow struct {
	StoreID     string    `gorm:"column:store_id"`
	PriceListID string    `gorm:"column:price_list_id"`
	VariantID   string    `gorm:"column:variant_id"`
	AmountMinor int64     `gorm:"column:amount_minor"`
	Version     int64     `gorm:"column:version"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

func (priceRow) TableName() string { return "pricing_prices" }

func (repository *Repository) Create(ctx context.Context, storeID ids.StoreID, list domain.PriceList) (domain.PriceList, error) {
	var created domain.PriceList
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if list.StoreID != storeID {
			return application.ErrNotFound
		}
		if err := lockStore(tx, storeID); err != nil {
			return err
		}
		if err := repository.validate(tx, ctx, storeID, list, ""); err != nil {
			return err
		}
		if err := tx.Create(toListRow(list)).Error; err != nil {
			return classify(err)
		}
		if err := replacePrices(tx, list); err != nil {
			return err
		}
		var err error
		created, err = load(tx, storeID, list.ID)
		return err
	})
	return created, classify(err)
}

func (repository *Repository) Replace(ctx context.Context, storeID ids.StoreID, id ids.PriceListID, expectedVersion int64, list domain.PriceList) (domain.PriceList, error) {
	var updated domain.PriceList
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if list.StoreID != storeID || list.ID != id {
			return application.ErrNotFound
		}
		if err := lockStore(tx, storeID); err != nil {
			return err
		}
		if err := repository.validate(tx, ctx, storeID, list, id.String()); err != nil {
			return err
		}
		result := tx.Model(&priceListRow{}).Where("store_id = ? AND price_list_id = ? AND version = ? AND status = 'active'", storeID.String(), id.String(), expectedVersion).Updates(map[string]any{"name": list.Name, "channel": list.Channel, "currency": list.Currency, "priority": list.Priority, "valid_from": list.ValidFrom, "valid_until": list.ValidUntil, "version": expectedVersion + 1, "updated_at": list.UpdatedAt})
		if result.Error != nil {
			return classify(result.Error)
		}
		if result.RowsAffected != 1 {
			return distinguish(tx, storeID, id)
		}
		if err := tx.Where("store_id = ? AND price_list_id = ?", storeID.String(), id.String()).Delete(&priceRow{}).Error; err != nil {
			return fmt.Errorf("replace prices: %w", err)
		}
		if err := replacePrices(tx, list); err != nil {
			return err
		}
		var err error
		updated, err = load(tx, storeID, id)
		return err
	})
	return updated, classify(err)
}

func (repository *Repository) Get(ctx context.Context, storeID ids.StoreID, id ids.PriceListID) (domain.PriceList, error) {
	var list domain.PriceList
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		list, err = load(tx, storeID, id)
		return err
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return list, err
}

func (repository *Repository) List(ctx context.Context, storeID ids.StoreID, limit int, cursor *application.Cursor) ([]domain.PriceList, error) {
	result := make([]domain.PriceList, 0)
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.Where("store_id = ?", storeID.String())
		if cursor != nil {
			var anchors int64
			if err := tx.Model(&priceListRow{}).Where("store_id = ? AND price_list_id = ? AND updated_at = ?", storeID.String(), cursor.PriceListID.String(), cursor.UpdatedAt).Count(&anchors).Error; err != nil {
				return err
			}
			if anchors != 1 {
				return application.ErrNotFound
			}
			query = query.Where("(updated_at, price_list_id) < (?, ?::uuid)", cursor.UpdatedAt, cursor.PriceListID.String())
		}
		var rows []priceListRow
		if err := query.Order("updated_at DESC, price_list_id DESC").Limit(limit).Find(&rows).Error; err != nil {
			return fmt.Errorf("list price lists: %w", err)
		}
		for _, row := range rows {
			list, err := load(tx, storeID, ids.PriceListID(row.PriceListID))
			if err != nil {
				return err
			}
			result = append(result, list)
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return result, err
}

func (repository *Repository) Archive(ctx context.Context, storeID ids.StoreID, id ids.PriceListID, expectedVersion int64, now time.Time) (domain.PriceList, error) {
	var archived domain.PriceList
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockStore(tx, storeID); err != nil {
			return err
		}
		result := tx.Model(&priceListRow{}).Where("store_id = ? AND price_list_id = ? AND version = ? AND status = 'active'", storeID.String(), id.String(), expectedVersion).Updates(map[string]any{"status": "archived", "archived_at": now, "updated_at": now, "version": expectedVersion + 1})
		if result.Error != nil {
			return classify(result.Error)
		}
		if result.RowsAffected != 1 {
			return distinguish(tx, storeID, id)
		}
		var err error
		archived, err = load(tx, storeID, id)
		return err
	})
	return archived, classify(err)
}

func (repository *Repository) ResolveCurrent(ctx context.Context, storeID ids.StoreID, variants []ids.VariantID) (map[ids.VariantID]money.Money, error) {
	return resolveCurrent(repository.db.WithContext(ctx), storeID, variants)
}

func (repository *Repository) ResolveCurrentInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, variants []ids.VariantID) (map[ids.VariantID]money.Money, error) {
	var at time.Time
	if err := tx.WithContext(ctx).Raw("SELECT clock_timestamp()").Scan(&at).Error; err != nil {
		return nil, fmt.Errorf("read current price time: %w", err)
	}
	return repository.ResolveCurrentAtInTransaction(ctx, tx, storeID, variants, at)
}

func (repository *Repository) ResolveCurrentAtInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, variants []ids.VariantID, at time.Time) (map[ids.VariantID]money.Money, error) {
	return resolveCurrentAt(tx.WithContext(ctx), storeID, variants, at.UTC())
}

func resolveCurrent(db *gorm.DB, storeID ids.StoreID, variants []ids.VariantID) (map[ids.VariantID]money.Money, error) {
	var at time.Time
	if err := db.Raw("SELECT clock_timestamp()").Scan(&at).Error; err != nil {
		return nil, fmt.Errorf("read current price time: %w", err)
	}
	return resolveCurrentAt(db, storeID, variants, at)
}

func resolveCurrentAt(db *gorm.DB, storeID ids.StoreID, variants []ids.VariantID, at time.Time) (map[ids.VariantID]money.Money, error) {
	result := make(map[ids.VariantID]money.Money)
	if len(variants) == 0 {
		return result, nil
	}
	values := variantStrings(variants)
	type resolvedRow struct {
		VariantID   string `gorm:"column:variant_id"`
		AmountMinor int64  `gorm:"column:amount_minor"`
		Currency    string `gorm:"column:currency"`
	}
	var rows []resolvedRow
	err := db.Raw(`SELECT DISTINCT ON (p.variant_id) p.variant_id,p.amount_minor,l.currency FROM pricing_prices p JOIN pricing_price_lists l ON l.store_id=p.store_id AND l.price_list_id=p.price_list_id WHERE p.store_id=? AND p.variant_id IN ? AND l.status='active' AND l.channel='storefront' AND l.currency='VND' AND (l.valid_from IS NULL OR l.valid_from<=?) AND (l.valid_until IS NULL OR ?<l.valid_until) ORDER BY p.variant_id,l.priority DESC,l.price_list_id ASC`, storeID.String(), values, at, at).Scan(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("resolve current prices: %w", err)
	}
	for _, row := range rows {
		id, err := ids.ParseVariantID(row.VariantID)
		if err != nil {
			return nil, err
		}
		value, err := money.New(row.AmountMinor, row.Currency)
		if err != nil {
			return nil, err
		}
		result[id] = value
	}
	return result, nil
}

func (repository *Repository) validate(tx *gorm.DB, ctx context.Context, storeID ids.StoreID, list domain.PriceList, excludeID string) error {
	variants := make([]ids.VariantID, len(list.Prices))
	for index, price := range list.Prices {
		variants[index] = price.VariantID
	}
	valid, err := repository.catalog.LockActiveVariants(ctx, tx, storeID, variants)
	if err != nil {
		return err
	}
	if !valid {
		return application.ErrNotFound
	}
	var overlaps int64
	err = tx.Raw(`SELECT count(*) FROM pricing_price_lists l JOIN pricing_prices p ON p.store_id=l.store_id AND p.price_list_id=l.price_list_id WHERE l.store_id=? AND l.status='active' AND l.channel=? AND l.currency=? AND l.priority=? AND l.price_list_id<>?::uuid AND p.variant_id IN ? AND (?::timestamptz IS NULL OR l.valid_from IS NULL OR l.valid_from<?::timestamptz) AND (?::timestamptz IS NULL OR l.valid_until IS NULL OR ?::timestamptz<l.valid_until)`, storeID.String(), list.Channel, list.Currency, list.Priority, nullableUUID(excludeID), variantStrings(variants), list.ValidUntil, list.ValidUntil, list.ValidFrom, list.ValidFrom).Scan(&overlaps).Error
	if err != nil {
		return fmt.Errorf("validate price overlap: %w", err)
	}
	if overlaps > 0 {
		return application.ErrConflict
	}
	return nil
}

func replacePrices(tx *gorm.DB, list domain.PriceList) error {
	for _, price := range list.Prices {
		row := priceRow{StoreID: list.StoreID.String(), PriceListID: list.ID.String(), VariantID: price.VariantID.String(), AmountMinor: price.Money.AmountMinor, Version: price.Version, CreatedAt: price.CreatedAt, UpdatedAt: price.UpdatedAt}
		if err := tx.Create(&row).Error; err != nil {
			return classify(err)
		}
	}
	return nil
}

func load(tx *gorm.DB, storeID ids.StoreID, id ids.PriceListID) (domain.PriceList, error) {
	var row priceListRow
	if err := tx.Where("store_id = ? AND price_list_id = ?", storeID.String(), id.String()).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.PriceList{}, application.ErrNotFound
	} else if err != nil {
		return domain.PriceList{}, err
	}
	list := domain.PriceList{StoreID: storeID, ID: id, Name: row.Name, Channel: row.Channel, Currency: row.Currency, Priority: row.Priority, ValidFrom: row.ValidFrom, ValidUntil: row.ValidUntil, Status: domain.Status(row.Status), Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ArchivedAt: row.ArchivedAt, Prices: make([]domain.Price, 0)}
	var rows []priceRow
	if err := tx.Where("store_id = ? AND price_list_id = ?", storeID.String(), id.String()).Order("variant_id").Find(&rows).Error; err != nil {
		return domain.PriceList{}, err
	}
	for _, value := range rows {
		variantID, err := ids.ParseVariantID(value.VariantID)
		if err != nil {
			return domain.PriceList{}, err
		}
		amount, err := money.New(value.AmountMinor, row.Currency)
		if err != nil {
			return domain.PriceList{}, err
		}
		list.Prices = append(list.Prices, domain.Price{StoreID: storeID, PriceListID: id, VariantID: variantID, Money: amount, Version: value.Version, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt})
	}
	return list, nil
}

func toListRow(list domain.PriceList) *priceListRow {
	return &priceListRow{StoreID: list.StoreID.String(), PriceListID: list.ID.String(), Name: list.Name, Channel: list.Channel, Currency: list.Currency, Priority: list.Priority, ValidFrom: list.ValidFrom, ValidUntil: list.ValidUntil, Status: string(list.Status), Version: list.Version, CreatedAt: list.CreatedAt, UpdatedAt: list.UpdatedAt, ArchivedAt: list.ArchivedAt}
}

func lockStore(tx *gorm.DB, storeID ids.StoreID) error {
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", storeID.String()).Error; err != nil {
		return fmt.Errorf("lock pricing store: %w", err)
	}
	return nil
}

func distinguish(tx *gorm.DB, storeID ids.StoreID, id ids.PriceListID) error {
	var count int64
	if err := tx.Model(&priceListRow{}).Where("store_id = ? AND price_list_id = ?", storeID.String(), id.String()).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return application.ErrNotFound
	}
	return application.ErrConflict
}

func variantStrings(values []ids.VariantID) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = value.String()
	}
	return result
}

func nullableUUID(value string) any {
	if value == "" {
		return "00000000-0000-0000-0000-000000000000"
	}
	return value
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
	return err
}
