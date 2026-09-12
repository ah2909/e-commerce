package postgres

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ah2909/e-commerce/internal/carts/application"
	"github.com/ah2909/e-commerce/internal/carts/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

type cartRow struct {
	StoreID   string     `gorm:"column:store_id"`
	CartID    string     `gorm:"column:cart_id"`
	OwnerKind string     `gorm:"column:owner_kind"`
	UserID    *string    `gorm:"column:user_id"`
	GuestID   *string    `gorm:"column:guest_id"`
	Currency  string     `gorm:"column:currency"`
	Status    string     `gorm:"column:status"`
	Version   int64      `gorm:"column:version"`
	ExpiresAt time.Time  `gorm:"column:expires_at"`
	CreatedAt time.Time  `gorm:"column:created_at"`
	UpdatedAt time.Time  `gorm:"column:updated_at"`
	ClosedAt  *time.Time `gorm:"column:closed_at"`
}

func (cartRow) TableName() string { return "carts" }

type lineRow struct {
	StoreID   string    `gorm:"column:store_id"`
	CartID    string    `gorm:"column:cart_id"`
	VariantID string    `gorm:"column:variant_id"`
	Quantity  int64     `gorm:"column:quantity"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (lineRow) TableName() string { return "cart_lines" }

func (repository *Repository) CreateOrGet(ctx context.Context, storeID ids.StoreID, owner domain.Owner, id ids.CartID) (domain.Cart, bool, error) {
	if err := repository.expireOwner(ctx, storeID, owner); err != nil {
		return domain.Cart{}, false, err
	}
	if current, err := repository.get(ctx, storeID, owner); err == nil {
		return current, false, nil
	} else if !errors.Is(err, application.ErrNotFound) {
		return domain.Cart{}, false, err
	}
	now, err := databaseTime(repository.db.WithContext(ctx))
	if err != nil {
		return domain.Cart{}, false, err
	}
	row := cartRow{StoreID: storeID.String(), CartID: id.String(), OwnerKind: string(owner.Kind), Currency: "VND", Status: string(domain.StatusActive), Version: 1, ExpiresAt: now.Add(domain.TTL), CreatedAt: now, UpdatedAt: now}
	if owner.Kind == domain.OwnerUser {
		value := owner.ID
		row.UserID = &value
	} else {
		value := owner.ID
		row.GuestID = &value
	}
	if err := repository.db.WithContext(ctx).Create(&row).Error; err != nil {
		if isConflict(err) {
			current, getErr := repository.get(ctx, storeID, owner)
			return current, false, getErr
		}
		return domain.Cart{}, false, err
	}
	return mapCart(row, nil), true, nil
}

func (repository *Repository) Get(ctx context.Context, storeID ids.StoreID, owner domain.Owner) (domain.Cart, error) {
	return repository.get(ctx, storeID, owner)
}

func (repository *Repository) get(ctx context.Context, storeID ids.StoreID, owner domain.Owner) (domain.Cart, error) {
	var result domain.Cart
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row cartRow
		err := ownerQuery(tx.Where("store_id = ? AND status = 'active' AND expires_at > clock_timestamp()", storeID), owner).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return application.ErrNotFound
		}
		if err != nil {
			return err
		}
		var lines []lineRow
		if err := tx.Where("store_id = ? AND cart_id = ?", row.StoreID, row.CartID).Order("variant_id").Find(&lines).Error; err != nil {
			return err
		}
		result = mapCart(row, lines)
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return result, err
}

func (repository *Repository) expireOwner(ctx context.Context, storeID ids.StoreID, owner domain.Owner) error {
	now, err := databaseTime(repository.db.WithContext(ctx))
	if err != nil {
		return err
	}
	query := ownerQuery(repository.db.WithContext(ctx).Model(&cartRow{}).Where("store_id = ? AND status = 'active' AND expires_at <= ?", storeID, now), owner)
	return query.Updates(map[string]any{"status": domain.StatusExpired, "version": gorm.Expr("version + 1"), "updated_at": now, "closed_at": now}).Error
}

func (repository *Repository) SetLine(ctx context.Context, storeID ids.StoreID, owner domain.Owner, variantID ids.VariantID, quantity, expected int64) (domain.Cart, error) {
	var id string
	var result domain.Cart
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, now, err := lockActive(tx, storeID, owner, expected)
		if err != nil {
			return err
		}
		id = row.CartID
		var existing int64
		if err := tx.Model(&lineRow{}).Where("store_id = ? AND cart_id = ? AND variant_id = ?", storeID, id, variantID).Count(&existing).Error; err != nil {
			return err
		}
		if existing == 0 {
			var count int64
			if err := tx.Model(&lineRow{}).Where("store_id = ? AND cart_id = ?", storeID, id).Count(&count).Error; err != nil {
				return err
			}
			if count >= domain.MaxLines {
				return application.ErrConflict
			}
		}
		line := lineRow{StoreID: storeID.String(), CartID: id, VariantID: variantID.String(), Quantity: quantity, CreatedAt: now, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "store_id"}, {Name: "cart_id"}, {Name: "variant_id"}}, DoUpdates: clause.Assignments(map[string]any{"quantity": quantity, "updated_at": now})}).Create(&line).Error; err != nil {
			return classify(err)
		}
		if err := touchCart(tx, storeID, id, expected, now); err != nil {
			return err
		}
		result, err = loadTx(tx, storeID, id)
		return err
	})
	if err != nil {
		return domain.Cart{}, err
	}
	return result, nil
}

func (repository *Repository) RemoveLine(ctx context.Context, storeID ids.StoreID, owner domain.Owner, variantID ids.VariantID, expected int64) (domain.Cart, error) {
	var id string
	var result domain.Cart
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, now, err := lockActive(tx, storeID, owner, expected)
		if err != nil {
			return err
		}
		id = row.CartID
		deleted := tx.Where("store_id = ? AND cart_id = ? AND variant_id = ?", storeID, id, variantID).Delete(&lineRow{})
		if deleted.Error != nil {
			return deleted.Error
		}
		if deleted.RowsAffected != 1 {
			return application.ErrNotFound
		}
		if err := touchCart(tx, storeID, id, expected, now); err != nil {
			return err
		}
		result, err = loadTx(tx, storeID, id)
		return err
	})
	if err != nil {
		return domain.Cart{}, err
	}
	return result, nil
}

func (repository *Repository) Abandon(ctx context.Context, storeID ids.StoreID, owner domain.Owner, expected int64) error {
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, now, err := lockActive(tx, storeID, owner, expected)
		if err != nil {
			return err
		}
		result := tx.Model(&cartRow{}).Where("store_id = ? AND cart_id = ? AND version = ? AND status = 'active'", storeID, row.CartID, expected).Updates(map[string]any{"status": domain.StatusAbandoned, "version": gorm.Expr("version + 1"), "updated_at": now, "closed_at": now})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return application.ErrConflict
		}
		return nil
	})
}

func (repository *Repository) ExpireDue(ctx context.Context, limit int) (int, error) {
	type candidate struct {
		StoreID string
		CartID  string
	}
	var candidates []candidate
	if err := repository.db.WithContext(ctx).Raw("SELECT store_id, cart_id FROM carts WHERE status = 'active' AND expires_at <= clock_timestamp() ORDER BY expires_at, store_id, cart_id LIMIT ?", limit).Scan(&candidates).Error; err != nil {
		return 0, err
	}
	processed := 0
	for _, candidate := range candidates {
		transitioned := false
		err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var row cartRow
			err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).Where("store_id = ? AND cart_id = ? AND status = 'active'", candidate.StoreID, candidate.CartID).First(&row).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			now, err := databaseTime(tx)
			if err != nil {
				return err
			}
			if row.ExpiresAt.After(now) {
				return nil
			}
			result := tx.Model(&cartRow{}).Where("store_id = ? AND cart_id = ? AND status = 'active'", candidate.StoreID, candidate.CartID).Updates(map[string]any{"status": domain.StatusExpired, "version": gorm.Expr("version + 1"), "updated_at": now, "closed_at": now})
			if result.Error != nil {
				return result.Error
			}
			transitioned = result.RowsAffected == 1
			return nil
		})
		if err != nil {
			return processed, err
		}
		if transitioned {
			processed++
		}
	}
	return processed, nil
}

func loadTx(tx *gorm.DB, storeID ids.StoreID, id string) (domain.Cart, error) {
	var row cartRow
	if err := tx.Where("store_id = ? AND cart_id = ?", storeID, id).First(&row).Error; err != nil {
		return domain.Cart{}, err
	}
	var lines []lineRow
	if err := tx.Where("store_id = ? AND cart_id = ?", storeID, id).Order("variant_id").Find(&lines).Error; err != nil {
		return domain.Cart{}, err
	}
	return mapCart(row, lines), nil
}

func lockActive(tx *gorm.DB, storeID ids.StoreID, owner domain.Owner, expected int64) (cartRow, time.Time, error) {
	var row cartRow
	err := ownerQuery(tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id = ? AND status = 'active'", storeID), owner).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return cartRow{}, time.Time{}, application.ErrNotFound
	}
	if err != nil {
		return cartRow{}, time.Time{}, err
	}
	now, err := databaseTime(tx)
	if err != nil {
		return cartRow{}, time.Time{}, err
	}
	if row.Version != expected || !row.ExpiresAt.After(now) {
		return cartRow{}, time.Time{}, application.ErrConflict
	}
	return row, now, nil
}

func touchCart(tx *gorm.DB, storeID ids.StoreID, id string, expected int64, now time.Time) error {
	result := tx.Model(&cartRow{}).Where("store_id = ? AND cart_id = ? AND version = ? AND status = 'active'", storeID, id, expected).Updates(map[string]any{"version": gorm.Expr("version + 1"), "updated_at": now, "expires_at": now.Add(domain.TTL)})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return application.ErrConflict
	}
	return nil
}

func ownerQuery(query *gorm.DB, owner domain.Owner) *gorm.DB {
	if owner.Kind == domain.OwnerUser {
		return query.Where("owner_kind = 'user' AND user_id = ?", owner.ID)
	}
	return query.Where("owner_kind = 'guest' AND guest_id = ?", owner.ID)
}

func mapCart(row cartRow, rows []lineRow) domain.Cart {
	owner := domain.Owner{Kind: domain.OwnerKind(row.OwnerKind)}
	if row.UserID != nil {
		owner.ID = *row.UserID
	} else if row.GuestID != nil {
		owner.ID = *row.GuestID
	}
	lines := make([]domain.Line, len(rows))
	for i := range rows {
		lines[i] = domain.Line{VariantID: ids.VariantID(rows[i].VariantID), Quantity: rows[i].Quantity}
	}
	return domain.Cart{StoreID: ids.StoreID(row.StoreID), ID: ids.CartID(row.CartID), Owner: owner, Currency: row.Currency, Status: domain.Status(row.Status), Version: row.Version, ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ClosedAt: row.ClosedAt, Lines: lines}
}

func databaseTime(tx *gorm.DB) (time.Time, error) {
	var now time.Time
	err := tx.Raw("SELECT clock_timestamp()").Scan(&now).Error
	return now.UTC(), err
}

type stateError interface{ SQLState() string }

func isConflict(err error) bool {
	var state stateError
	return errors.As(err, &state) && state.SQLState() == "23505"
}
func classify(err error) error {
	var state stateError
	if errors.As(err, &state) && (state.SQLState() == "23505" || state.SQLState() == "23503" || state.SQLState() == "23514") {
		return application.ErrConflict
	}
	return err
}
