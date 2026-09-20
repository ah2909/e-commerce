package postgres

import (
	"context"
	"fmt"
	"time"

	eventapplication "github.com/ah2909/e-commerce/internal/eventing/application"
	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	searchapplication "github.com/ah2909/e-commerce/internal/search/application"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
)

type Effect struct{ resolver searchapplication.Resolver }

func NewEffect(resolver searchapplication.Resolver) *Effect { return &Effect{resolver: resolver} }

func (effect *Effect) Apply(ctx context.Context, tx *gorm.DB, event eventdomain.Event) (bool, error) {
	if event.Type != "product.published.v1" && event.Type != "product.updated.v1" && event.Type != "product.archived.v1" {
		return false, nil
	}
	productID, err := ids.ParseProductID(event.AggregateID)
	if err != nil {
		return false, eventapplication.ErrPermanent
	}
	product, visible, err := effect.resolver.ResolveSearchProduct(ctx, event.StoreID, productID)
	if err != nil {
		return false, fmt.Errorf("resolve search document: %w", err)
	}
	if !visible {
		if err := tx.Exec(`DELETE FROM search_documents WHERE store_id=? AND product_id=? AND aggregate_version <= ?`, event.StoreID.String(), productID.String(), event.AggregateVersion).Error; err != nil {
			return false, fmt.Errorf("remove search document: %w", err)
		}
		return true, nil
	}
	updated := time.Now().UTC()
	if err := tx.Exec(`
INSERT INTO search_documents (store_id,product_id,aggregate_version,name,slug,description,published_at,updated_at)
VALUES (?,?,?,?,?,?,?,?)
ON CONFLICT (store_id,product_id) DO UPDATE SET aggregate_version=EXCLUDED.aggregate_version,name=EXCLUDED.name,slug=EXCLUDED.slug,description=EXCLUDED.description,published_at=EXCLUDED.published_at,updated_at=EXCLUDED.updated_at
WHERE search_documents.aggregate_version < EXCLUDED.aggregate_version`, event.StoreID.String(), product.ProductID.String(), product.AggregateVersion, product.Name, product.Slug, product.Description, product.PublishedAt.UTC(), updated).Error; err != nil {
		return false, fmt.Errorf("upsert search document: %w", err)
	}
	return true, nil
}
