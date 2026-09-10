package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/ah2909/e-commerce/internal/audit/domain"
	"gorm.io/gorm"
)

type Writer struct{}

func NewWriter() Writer { return Writer{} }

type auditRecord struct {
	StoreID     string    `gorm:"column:store_id;type:uuid;primaryKey"`
	AuditID     string    `gorm:"column:audit_id;type:uuid;primaryKey"`
	ActorType   string    `gorm:"column:actor_type"`
	ActorID     string    `gorm:"column:actor_id"`
	Action      string    `gorm:"column:action"`
	SubjectType string    `gorm:"column:subject_type"`
	SubjectID   string    `gorm:"column:subject_id;type:uuid"`
	RequestID   string    `gorm:"column:request_id"`
	OccurredAt  time.Time `gorm:"column:occurred_at"`
}

func (auditRecord) TableName() string { return "audit_records" }

func (Writer) Insert(ctx context.Context, tx *gorm.DB, record domain.Record) error {
	row := auditRecord{StoreID: record.StoreID.String(), AuditID: record.AuditID.String(), ActorType: string(record.ActorType), ActorID: record.ActorID, Action: record.Action, SubjectType: record.SubjectType, SubjectID: record.SubjectID, RequestID: record.RequestID, OccurredAt: record.OccurredAt}
	if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("insert audit record: %w", err)
	}
	return nil
}
