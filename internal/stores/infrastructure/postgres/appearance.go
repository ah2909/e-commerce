package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	auditdomain "github.com/ah2909/e-commerce/internal/audit/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/stores/application"
	"github.com/ah2909/e-commerce/internal/stores/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type appearanceRow struct {
	StoreID   string    `gorm:"column:store_id;type:uuid;primaryKey"`
	Content   string    `gorm:"column:content;type:jsonb"`
	Version   int64     `gorm:"column:version"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (appearanceRow) TableName() string { return "store_appearances" }

func (r *Repository) GetAppearance(ctx context.Context, storeID ids.StoreID) (domain.Appearance, error) {
	var row appearanceRow
	err := r.db.WithContext(ctx).Where("store_id = ?", storeID.String()).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		store, found, err := r.ResolveStoreByID(ctx, storeID)
		if err != nil {
			return domain.Appearance{}, err
		}
		if !found {
			return domain.Appearance{}, application.ErrStoreUnavailable
		}
		return domain.DefaultAppearance(store.Name), nil
	}
	if err != nil {
		return domain.Appearance{}, fmt.Errorf("read appearance: %w", err)
	}
	var a domain.Appearance
	if err := json.Unmarshal([]byte(row.Content), &a); err != nil {
		return a, fmt.Errorf("restore appearance: %w", err)
	}
	a.Version = row.Version
	return a, nil
}

func (r *Repository) SaveAppearance(ctx context.Context, storeID ids.StoreID, actorID ids.UserID, a domain.Appearance, requestID string) (domain.Appearance, error) {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var store storeRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id = ?", storeID.String()).Take(&store).Error; err != nil {
			return fmt.Errorf("lock appearance store: %w", err)
		}
		allowed, err := r.HasPermissionInTransaction(ctx, tx, storeID, actorID, string(domain.PermissionStoreManage))
		if err != nil {
			return err
		}
		if !allowed || store.Status != string(domain.StoreStatusActive) {
			return application.ErrTenantAccessDenied
		}
		var row appearanceRow
		err = tx.Where("store_id = ?", storeID.String()).Take(&row).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("read appearance version: %w", err)
		}
		if row.Version != a.Version {
			return application.ErrAppearanceConflict
		}
		a.Version++
		content, err := json.Marshal(a)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		row = appearanceRow{StoreID: storeID.String(), Content: string(content), Version: a.Version, UpdatedAt: now}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "store_id"}}, DoUpdates: clause.AssignmentColumns([]string{"content", "version", "updated_at"})}).Create(&row).Error; err != nil {
			return fmt.Errorf("save appearance: %w", err)
		}
		return r.insertAudit(ctx, tx, storeID, auditdomain.ActorUser, actorID.String(), "store.appearance_updated", "store", storeID.String(), requestID, now)
	})
	if err != nil {
		return domain.Appearance{}, err
	}
	return a, nil
}
