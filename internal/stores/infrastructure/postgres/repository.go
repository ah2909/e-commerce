package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	auditdomain "github.com/ah2909/e-commerce/internal/audit/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/stores/application"
	"github.com/ah2909/e-commerce/internal/stores/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct {
	pool  *pgxpool.Pool
	audit AuditWriter
}

type AuditWriter interface {
	Insert(context.Context, pgx.Tx, auditdomain.Record) error
}

func NewRepository(pool *pgxpool.Pool, audit AuditWriter) *Repository {
	return &Repository{pool: pool, audit: audit}
}

func (repository *Repository) IsActive(ctx context.Context, storeID ids.StoreID) (bool, error) {
	var active bool
	err := repository.pool.QueryRow(ctx, `SELECT status = 'active' FROM stores WHERE store_id = $1::uuid`, storeID.String()).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query active store: %w", err)
	}
	return active, nil
}

func (repository *Repository) InsertStore(ctx context.Context, tx pgx.Tx, store domain.Store) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO stores (
			store_id, subdomain, name, status, default_currency, locale, timezone,
			settings_version, created_at, updated_at
		) VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		store.ID().String(), store.Subdomain(), store.Name(), store.Status(), store.DefaultCurrency(),
		store.Locale(), store.Timezone(), store.SettingsVersion(), store.CreatedAt(), store.UpdatedAt())
	if err != nil {
		return fmt.Errorf("insert store: %w", err)
	}
	return nil
}

func (repository *Repository) InsertMembership(ctx context.Context, tx pgx.Tx, membership domain.Membership) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO store_memberships (store_id, user_id, status, role, created_at, updated_at)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6)`,
		membership.StoreID().String(), membership.UserID().String(), membership.Status(), membership.Role(),
		membership.CreatedAt(), membership.UpdatedAt())
	if err != nil {
		return fmt.Errorf("insert store membership: %w", err)
	}
	return nil
}

func (repository *Repository) InsertStoreCreatedAudit(ctx context.Context, tx pgx.Tx, store domain.Store, actorID, requestID string) error {
	return repository.insertAudit(ctx, tx, store.ID(), auditdomain.ActorPlatformAdmin, actorID, "store.created", "store", store.ID().String(), requestID, store.CreatedAt())
}

func (repository *Repository) ResolveStoreBySubdomain(ctx context.Context, subdomain domain.Subdomain) (application.StoreRecord, bool, error) {
	var idText string
	var record application.StoreRecord
	err := repository.pool.QueryRow(ctx, `SELECT store_id::text,subdomain,name,status,default_currency,locale,timezone,settings_version FROM stores WHERE subdomain=$1`, subdomain.String()).Scan(&idText, &record.Subdomain, &record.Name, &record.Status, &record.DefaultCurrency, &record.Locale, &record.Timezone, &record.SettingsVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.StoreRecord{}, false, nil
	}
	if err != nil {
		return application.StoreRecord{}, false, fmt.Errorf("query store by subdomain: %w", err)
	}
	storeID, err := ids.ParseStoreID(idText)
	if err != nil {
		return application.StoreRecord{}, false, fmt.Errorf("restore store ID: %w", err)
	}
	record.StoreID = storeID
	return record, true, nil
}

func (repository *Repository) ListMemberships(ctx context.Context, storeID ids.StoreID) ([]domain.Membership, error) {
	rows, err := repository.pool.Query(ctx, `SELECT user_id::text,status,role,created_at,updated_at FROM store_memberships WHERE store_id=$1::uuid ORDER BY created_at,user_id`, storeID.String())
	if err != nil {
		return nil, fmt.Errorf("list store memberships: %w", err)
	}
	defer rows.Close()
	memberships := make([]domain.Membership, 0)
	for rows.Next() {
		var userIDText string
		var status domain.MembershipStatus
		var role domain.Role
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&userIDText, &status, &role, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan store membership: %w", err)
		}
		userID, err := ids.ParseUserID(userIDText)
		if err != nil {
			return nil, fmt.Errorf("restore membership user ID: %w", err)
		}
		membership, err := domain.RestoreMembershipWithRole(storeID, userID, status, role, createdAt, updatedAt)
		if err != nil {
			return nil, fmt.Errorf("restore membership: %w", err)
		}
		memberships = append(memberships, membership)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list store memberships: %w", err)
	}
	return memberships, nil
}

func (repository *Repository) CreateMembership(ctx context.Context, membership domain.Membership, actorID ids.UserID, requestID string) error {
	err := pgx.BeginFunc(ctx, repository.pool, func(tx pgx.Tx) error {
		if err := lockOwner(ctx, tx, membership.StoreID(), actorID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO store_memberships (store_id,user_id,status,role,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6)`, membership.StoreID().String(), membership.UserID().String(), membership.Status(), membership.Role(), membership.CreatedAt(), membership.UpdatedAt())
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return application.ErrMembershipTarget
			}
			return fmt.Errorf("insert membership: %w", err)
		}
		return repository.insertAudit(ctx, tx, membership.StoreID(), auditdomain.ActorUser, actorID.String(), "membership.created", "membership", membership.UserID().String(), requestID, membership.CreatedAt())
	})
	return err
}

func (repository *Repository) UpdateMembership(ctx context.Context, storeID ids.StoreID, userID ids.UserID, role domain.Role, status domain.MembershipStatus, actorID ids.UserID, requestID string, now time.Time) (domain.Membership, error) {
	var updated domain.Membership
	err := pgx.BeginFunc(ctx, repository.pool, func(tx pgx.Tx) error {
		if err := lockOwner(ctx, tx, storeID, actorID); err != nil {
			return err
		}
		var currentStatus domain.MembershipStatus
		var currentRole domain.Role
		var createdAt time.Time
		err := tx.QueryRow(ctx, `SELECT status,role,created_at FROM store_memberships WHERE store_id=$1::uuid AND user_id=$2::uuid FOR UPDATE`, storeID.String(), userID.String()).Scan(&currentStatus, &currentRole, &createdAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return application.ErrMembershipTarget
		}
		if err != nil {
			return fmt.Errorf("lock membership: %w", err)
		}
		if currentStatus == domain.MembershipStatusActive && currentRole == domain.RoleOwner && (status != domain.MembershipStatusActive || role != domain.RoleOwner) {
			var otherOwners int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM store_memberships WHERE store_id=$1::uuid AND user_id<>$2::uuid AND status='active' AND role='owner'`, storeID.String(), userID.String()).Scan(&otherOwners); err != nil {
				return fmt.Errorf("count active owners: %w", err)
			}
			if otherOwners == 0 {
				return application.ErrLastOwner
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE store_memberships SET status=$3,role=$4,updated_at=$5 WHERE store_id=$1::uuid AND user_id=$2::uuid`, storeID.String(), userID.String(), status, role, now); err != nil {
			return fmt.Errorf("update membership: %w", err)
		}
		if err := repository.insertAudit(ctx, tx, storeID, auditdomain.ActorUser, actorID.String(), "membership.updated", "membership", userID.String(), requestID, now); err != nil {
			return err
		}
		updated, err = domain.RestoreMembershipWithRole(storeID, userID, status, role, createdAt, now)
		return err
	})
	return updated, err
}

func lockOwner(ctx context.Context, tx pgx.Tx, storeID ids.StoreID, actorID ids.UserID) error {
	var allowed bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM store_memberships WHERE store_id=$1::uuid AND user_id=$2::uuid AND status='active' AND role='owner') FROM stores WHERE store_id=$1::uuid FOR UPDATE`, storeID.String(), actorID.String()).Scan(&allowed)
	if errors.Is(err, pgx.ErrNoRows) || !allowed {
		return application.ErrTenantAccessDenied
	}
	if err != nil {
		return fmt.Errorf("lock store owner set: %w", err)
	}
	return nil
}

func (repository *Repository) insertAudit(ctx context.Context, tx pgx.Tx, storeID ids.StoreID, actorType auditdomain.ActorType, actorID, action, subjectType, subjectID, requestID string, occurredAt time.Time) error {
	if repository.audit == nil {
		return errors.New("audit writer is required")
	}
	record, err := auditdomain.NewRecord(storeID, actorType, actorID, action, subjectType, subjectID, requestID, occurredAt)
	if err != nil {
		return fmt.Errorf("create audit record: %w", err)
	}
	return repository.audit.Insert(ctx, tx, record)
}

func (repository *Repository) ResolveTenant(ctx context.Context, storeID ids.StoreID, userID ids.UserID) (application.TenantRecord, bool, error) {
	row := repository.pool.QueryRow(ctx, `
		SELECT
			s.store_id::text, m.user_id::text, s.subdomain, s.name, s.status, m.status, m.role,
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
		&storeIDText, &userIDText, &record.Subdomain, &record.StoreName, &record.StoreStatus, &record.MembershipStatus, &record.Role,
		&record.DefaultCurrency, &record.Locale, &record.Timezone, &record.SettingsVersion,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.TenantRecord{}, false, nil
	}
	if err != nil {
		return application.TenantRecord{}, false, fmt.Errorf("query tenant context: %w", err)
	}
	resolvedStoreID, err := ids.ParseStoreID(storeIDText)
	if err != nil {
		return application.TenantRecord{}, false, fmt.Errorf("restore tenant store ID: %w", err)
	}
	resolvedUserID, err := ids.ParseUserID(userIDText)
	if err != nil {
		return application.TenantRecord{}, false, fmt.Errorf("restore tenant user ID: %w", err)
	}
	record.StoreID = resolvedStoreID
	record.UserID = resolvedUserID
	return record, true, nil
}
