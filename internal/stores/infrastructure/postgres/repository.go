package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/ah2909/e-commerce/internal/stores/application"
	"github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (repository *Repository) CreateWithMembership(ctx context.Context, store domain.Store, membership domain.Membership) error {
	if store.ID() != membership.StoreID() {
		return fmt.Errorf("create store with membership: mismatched store scope")
	}
	err := pgx.BeginFunc(ctx, repository.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO stores (
				store_id, name, status, default_currency, locale, timezone,
				settings_version, created_at, updated_at
			) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9)`,
			store.ID().String(), store.Name(), store.Status(), store.DefaultCurrency(),
			store.Locale(), store.Timezone(), store.SettingsVersion(), store.CreatedAt(), store.UpdatedAt())
		if err != nil {
			return fmt.Errorf("insert store: %w", err)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO store_memberships (store_id, user_id, status, created_at, updated_at)
			VALUES ($1::uuid, $2::uuid, $3, $4, $5)`,
			membership.StoreID().String(), membership.UserID().String(), membership.Status(),
			membership.CreatedAt(), membership.UpdatedAt())
		if err != nil {
			return fmt.Errorf("insert store membership: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("create store transaction: %w", err)
	}
	return nil
}

func (repository *Repository) ResolveTenant(ctx context.Context, storeID domain.StoreID, userID domain.UserID) (application.TenantRecord, bool, error) {
	row := repository.pool.QueryRow(ctx, `
		SELECT
			s.store_id::text, m.user_id::text, s.name, s.status, m.status,
			s.default_currency, s.locale, s.timezone, s.settings_version
		FROM store_memberships AS m
		JOIN stores AS s ON s.store_id = m.store_id
		WHERE m.store_id = $1::uuid AND m.user_id = $2::uuid`,
		storeID.String(), userID.String())
	var (
		storeIDText string
		userIDText  string
		record      application.TenantRecord
	)
	err := row.Scan(
		&storeIDText, &userIDText, &record.StoreName, &record.StoreStatus, &record.MembershipStatus,
		&record.DefaultCurrency, &record.Locale, &record.Timezone, &record.SettingsVersion,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.TenantRecord{}, false, nil
	}
	if err != nil {
		return application.TenantRecord{}, false, fmt.Errorf("query tenant context: %w", err)
	}
	resolvedStoreID, err := domain.ParseStoreID(storeIDText)
	if err != nil {
		return application.TenantRecord{}, false, fmt.Errorf("restore tenant store ID: %w", err)
	}
	resolvedUserID, err := domain.ParseUserID(userIDText)
	if err != nil {
		return application.TenantRecord{}, false, fmt.Errorf("restore tenant user ID: %w", err)
	}
	record.StoreID = resolvedStoreID
	record.UserID = resolvedUserID
	return record, true, nil
}
