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

type homepageRow struct {
	StoreID     string `gorm:"primaryKey"`
	Draft       string `gorm:"type:jsonb"`
	Published   string `gorm:"type:jsonb"`
	Version     int64
	UpdatedAt   time.Time
	PublishedAt *time.Time
}

func (homepageRow) TableName() string { return "store_homepages" }

func (r *Repository) GetPublishedHomepage(ctx context.Context, storeID ids.StoreID) (domain.Homepage, error) {
	var row homepageRow
	err := r.db.WithContext(ctx).Select("published").Where("store_id = ?", storeID.String()).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.DefaultHomepage(), nil
	}
	if err != nil {
		return domain.Homepage{}, fmt.Errorf("read published homepage: %w", err)
	}
	return restoreHomepage(row.Published)
}

func (r *Repository) GetHomepage(ctx context.Context, storeID ids.StoreID) (domain.HomepageState, error) {
	row, err := readHomepageRow(r.db.WithContext(ctx), storeID)
	if err != nil {
		return domain.HomepageState{}, err
	}
	return row.state()
}

func (r *Repository) SaveHomepageDraft(ctx context.Context, storeID ids.StoreID, actorID ids.UserID, draft domain.Homepage, expected int64, requestID string) (domain.HomepageState, error) {
	content, err := json.Marshal(draft)
	if err != nil {
		return domain.HomepageState{}, fmt.Errorf("encode homepage draft: %w", err)
	}
	return r.writeHomepage(ctx, storeID, actorID, expected, requestID, string(content))
}

func (r *Repository) PublishHomepage(ctx context.Context, storeID ids.StoreID, actorID ids.UserID, expected int64, requestID string) (domain.HomepageState, error) {
	return r.writeHomepage(ctx, storeID, actorID, expected, requestID, "")
}

func (r *Repository) writeHomepage(ctx context.Context, storeID ids.StoreID, actorID ids.UserID, expected int64, requestID, draft string) (domain.HomepageState, error) {
	var state domain.HomepageState
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Lock the store to serialize first writes before a homepage row exists.
		var store storeRow
		err := tx.Select("store_id").Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id = ?", storeID.String()).Take(&store).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return application.ErrTenantAccessDenied
		}
		if err != nil {
			return fmt.Errorf("lock store for homepage write: %w", err)
		}
		allowed, err := r.HasPermissionInTransaction(ctx, tx, storeID, actorID, string(domain.PermissionStoreManage))
		if err != nil {
			return fmt.Errorf("check homepage management permission: %w", err)
		}
		if !allowed {
			return application.ErrTenantAccessDenied
		}
		row, err := readHomepageRow(tx, storeID)
		if err != nil {
			return err
		}
		publishing := draft == ""
		if row.Version != expected || (publishing && row.Version == 0) {
			return application.ErrHomepageConflict
		}
		now := time.Now().UTC()
		action := auditdomain.StoreHomepageDraftSaved
		columns := []string{"draft", "version", "updated_at"}
		if publishing {
			action = auditdomain.StoreHomepagePublished
			columns = []string{"published", "published_at", "version", "updated_at"}
			row.Published, row.PublishedAt = row.Draft, &now
		} else {
			row.Draft = draft
		}
		row.Version++
		row.UpdatedAt = now
		state, err = row.state()
		if err != nil {
			return err
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "store_id"}}, DoUpdates: clause.AssignmentColumns(columns)}).Create(&row).Error; err != nil {
			return fmt.Errorf("persist homepage: %w", err)
		}
		return r.insertAudit(ctx, tx, storeID, auditdomain.ActorUser, actorID.String(), action, "store", storeID.String(), requestID, now)
	})
	if err != nil {
		return domain.HomepageState{}, err
	}
	return state, nil
}

func readHomepageRow(db *gorm.DB, storeID ids.StoreID) (homepageRow, error) {
	var row homepageRow
	err := db.Where("store_id = ?", storeID.String()).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		initial, err := json.Marshal(domain.DefaultHomepage())
		if err != nil {
			return homepageRow{}, fmt.Errorf("encode default homepage: %w", err)
		}
		return homepageRow{StoreID: storeID.String(), Draft: string(initial), Published: string(initial)}, nil
	}
	if err != nil {
		return homepageRow{}, fmt.Errorf("read homepage: %w", err)
	}
	return row, nil
}

func (r homepageRow) state() (domain.HomepageState, error) {
	draft, err := restoreHomepage(r.Draft)
	if err != nil {
		return domain.HomepageState{}, err
	}
	published, err := restoreHomepage(r.Published)
	if err != nil {
		return domain.HomepageState{}, err
	}
	return domain.HomepageState{Draft: draft, Published: published, Version: r.Version}, nil
}

func restoreHomepage(content string) (domain.Homepage, error) {
	var homepage domain.Homepage
	if err := json.Unmarshal([]byte(content), &homepage); err != nil {
		return domain.Homepage{}, fmt.Errorf("restore homepage: %w", err)
	}
	if err := homepage.Validate(); err != nil {
		return domain.Homepage{}, errors.New("stored homepage failed validation")
	}
	return homepage, nil
}
