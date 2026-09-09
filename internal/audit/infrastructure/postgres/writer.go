package postgres

import (
	"context"
	"fmt"

	"github.com/ah2909/e-commerce/internal/audit/domain"
	"github.com/jackc/pgx/v5"
)

type Writer struct{}

func NewWriter() Writer { return Writer{} }

func (Writer) Insert(ctx context.Context, tx pgx.Tx, record domain.Record) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_records (store_id,audit_id,actor_type,actor_id,action,subject_type,subject_id,request_id,occurred_at) VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6,$7::uuid,$8,$9)`, record.StoreID.String(), record.AuditID.String(), record.ActorType, record.ActorID, record.Action, record.SubjectType, record.SubjectID, record.RequestID, record.OccurredAt)
	if err != nil {
		return fmt.Errorf("insert audit record: %w", err)
	}
	return nil
}
