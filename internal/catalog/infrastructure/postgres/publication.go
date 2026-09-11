package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ah2909/e-commerce/internal/catalog/application"
	"github.com/ah2909/e-commerce/internal/catalog/domain"
	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shared/money"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type TransactionPriceResolver interface {
	ResolveCurrentAtInTransaction(context.Context, *gorm.DB, ids.StoreID, []ids.VariantID, time.Time) (map[ids.VariantID]money.Money, error)
}

type TransactionEventWriter interface {
	Append(context.Context, *gorm.DB, eventdomain.Event) error
}

type PublicationCoordinator struct {
	db         *gorm.DB
	prices     TransactionPriceResolver
	events     TransactionEventWriter
	newEventID func() (ids.EventID, error)
}

func NewPublicationCoordinator(db *gorm.DB, prices TransactionPriceResolver, events TransactionEventWriter, newEventID func() (ids.EventID, error)) *PublicationCoordinator {
	return &PublicationCoordinator{db: db, prices: prices, events: events, newEventID: newEventID}
}

func (coordinator *PublicationCoordinator) Publish(ctx context.Context, storeID ids.StoreID, productID ids.ProductID, expectedVersion int64, correlationID string) (domain.Product, error) {
	var published domain.Product
	err := coordinator.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockHierarchy(tx, storeID); err != nil {
			return err
		}
		var locked productRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id = ? AND product_id = ?", storeID.String(), productID.String()).First(&locked).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return application.ErrNotFound
		} else if err != nil {
			return fmt.Errorf("lock product for publication: %w", err)
		}
		if locked.Status != string(domain.ProductStatusDraft) || locked.Version != expectedVersion {
			return application.ErrConflict
		}
		var databaseNow time.Time
		if err := tx.Raw("SELECT clock_timestamp()").Scan(&databaseNow).Error; err != nil {
			return fmt.Errorf("read publication time: %w", err)
		}
		product, err := loadProduct(tx, storeID, productID)
		if err != nil {
			return err
		}
		variants := make([]ids.VariantID, 0, len(product.Variants))
		for _, variant := range product.Variants {
			if variant.Status == domain.ItemActive {
				variants = append(variants, variant.ID)
			}
		}
		current, err := coordinator.prices.ResolveCurrentAtInTransaction(ctx, tx, storeID, variants, databaseNow)
		if err != nil {
			return err
		}
		if len(current) == 0 {
			return application.ErrConflict
		}
		published, err = product.Published(databaseNow)
		if err != nil {
			return err
		}
		result := tx.Model(&productRow{}).Where("store_id = ? AND product_id = ? AND version = ? AND status = 'draft'", storeID.String(), productID.String(), expectedVersion).Updates(map[string]any{"status": "published", "published_at": published.PublishedAt, "updated_at": published.UpdatedAt, "version": published.Version})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return application.ErrConflict
		}
		eventID, err := coordinator.newEventID()
		if err != nil {
			return fmt.Errorf("generate publication event ID: %w", err)
		}
		event, err := eventdomain.New(storeID, eventID, "product.published.v1", "product", productID.String(), published.Version, databaseNow, correlationID, map[string]any{"product_id": productID.String(), "version": published.Version})
		if err != nil {
			return err
		}
		if err := coordinator.events.Append(ctx, tx, event); err != nil {
			return err
		}
		published, err = loadProduct(tx, storeID, productID)
		return err
	})
	if err != nil {
		return domain.Product{}, classify(err)
	}
	return published, nil
}
