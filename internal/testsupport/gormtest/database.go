//go:build integration

package gormtest

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func FromPool(tb testing.TB, pool *pgxpool.Pool) *gorm.DB {
	tb.Helper()
	sqlDB := stdlib.OpenDBFromPool(pool)
	tb.Cleanup(func() { _ = sqlDB.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		tb.Fatalf("open GORM test connection: %v", err)
	}
	return db
}
