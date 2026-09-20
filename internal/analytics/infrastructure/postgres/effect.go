package postgres

import (
	"context"
	"fmt"

	eventdomain "github.com/ah2909/e-commerce/internal/eventing/domain"
	"gorm.io/gorm"
)

type Effect struct{}

func NewEffect() Effect { return Effect{} }

func (Effect) Apply(ctx context.Context, tx *gorm.DB, event eventdomain.Event) (bool, error) {
	date := event.OccurredAt.UTC().Format("2006-01-02")
	if err := tx.WithContext(ctx).Exec(`
INSERT INTO analytics_daily_counts (store_id,event_date,event_type,event_count,updated_at)
VALUES (?,?,?,1,clock_timestamp())
ON CONFLICT (store_id,event_date,event_type) DO UPDATE SET event_count=analytics_daily_counts.event_count+1,updated_at=clock_timestamp()`, event.StoreID.String(), date, event.Type).Error; err != nil {
		return false, fmt.Errorf("increment analytics count: %w", err)
	}
	return true, nil
}
