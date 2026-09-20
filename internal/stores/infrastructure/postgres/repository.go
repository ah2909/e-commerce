package postgres

import (
	"context"
	"database/sql"
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

type Repository struct {
	db    *gorm.DB
	audit AuditWriter
}

type AuditWriter interface {
	Insert(context.Context, *gorm.DB, auditdomain.Record) error
}

func NewRepository(db *gorm.DB, audit AuditWriter) *Repository {
	return &Repository{db: db, audit: audit}
}

type storeRow struct {
	StoreID         string    `gorm:"column:store_id;type:uuid;primaryKey"`
	Subdomain       string    `gorm:"column:subdomain"`
	Name            string    `gorm:"column:name"`
	Status          string    `gorm:"column:status"`
	DefaultCurrency string    `gorm:"column:default_currency"`
	Locale          string    `gorm:"column:locale"`
	Timezone        string    `gorm:"column:timezone"`
	SettingsVersion int64     `gorm:"column:settings_version"`
	CreatedAt       time.Time `gorm:"column:created_at"`
	UpdatedAt       time.Time `gorm:"column:updated_at"`
}

func (storeRow) TableName() string { return "stores" }

type membershipRow struct {
	StoreID   string    `gorm:"column:store_id;type:uuid;primaryKey"`
	UserID    string    `gorm:"column:user_id;type:uuid;primaryKey"`
	Status    string    `gorm:"column:status"`
	Role      string    `gorm:"column:role"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

type membershipPermissionRow struct {
	StoreID    string    `gorm:"column:store_id;primaryKey"`
	UserID     string    `gorm:"column:user_id;primaryKey"`
	Permission string    `gorm:"column:permission;primaryKey"`
	Version    int64     `gorm:"column:version"`
	CreatedAt  time.Time `gorm:"column:created_at"`
	UpdatedAt  time.Time `gorm:"column:updated_at"`
}

func (membershipPermissionRow) TableName() string { return "store_membership_permissions" }

type membershipPermissionSetRow struct {
	StoreID   string    `gorm:"column:store_id;primaryKey"`
	UserID    string    `gorm:"column:user_id;primaryKey"`
	Version   int64     `gorm:"column:version"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (membershipPermissionSetRow) TableName() string { return "store_membership_permission_sets" }

func (membershipRow) TableName() string { return "store_memberships" }

func (repository *Repository) IsActive(ctx context.Context, storeID ids.StoreID) (bool, error) {
	var row storeRow
	err := repository.db.WithContext(ctx).Select("status").Where("store_id = ?", storeID.String()).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("query active store: %w", err)
	}
	return row.Status == string(domain.StoreStatusActive), nil
}

func (repository *Repository) InsertStore(ctx context.Context, tx *gorm.DB, store domain.Store) error {
	row := storeRow{StoreID: store.ID().String(), Subdomain: store.Subdomain().String(), Name: store.Name(), Status: string(store.Status()), DefaultCurrency: store.DefaultCurrency(), Locale: store.Locale(), Timezone: store.Timezone(), SettingsVersion: store.SettingsVersion(), CreatedAt: store.CreatedAt(), UpdatedAt: store.UpdatedAt()}
	if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("insert store: %w", err)
	}
	return nil
}

func (repository *Repository) InsertMembership(ctx context.Context, tx *gorm.DB, membership domain.Membership) error {
	row := membershipRow{StoreID: membership.StoreID().String(), UserID: membership.UserID().String(), Status: string(membership.Status()), Role: string(membership.Role()), CreatedAt: membership.CreatedAt(), UpdatedAt: membership.UpdatedAt()}
	if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("insert store membership: %w", err)
	}
	return nil
}

func (repository *Repository) InsertStoreCreatedAudit(ctx context.Context, tx *gorm.DB, store domain.Store, actorID, requestID string) error {
	return repository.insertAudit(ctx, tx, store.ID(), auditdomain.ActorPlatformAdmin, actorID, "store.created", "store", store.ID().String(), requestID, store.CreatedAt())
}

func (repository *Repository) ResolveStoreBySubdomain(ctx context.Context, subdomain domain.Subdomain) (application.StoreRecord, bool, error) {
	var row storeRow
	err := repository.db.WithContext(ctx).Where("subdomain = ?", subdomain.String()).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return application.StoreRecord{}, false, nil
	}
	if err != nil {
		return application.StoreRecord{}, false, fmt.Errorf("query store by subdomain: %w", err)
	}
	storeID, err := ids.ParseStoreID(row.StoreID)
	if err != nil {
		return application.StoreRecord{}, false, fmt.Errorf("restore store ID: %w", err)
	}
	return application.StoreRecord{StoreID: storeID, Subdomain: domain.Subdomain(row.Subdomain), Name: row.Name, Status: domain.StoreStatus(row.Status), DefaultCurrency: row.DefaultCurrency, Locale: row.Locale, Timezone: row.Timezone, SettingsVersion: row.SettingsVersion}, true, nil
}

func (repository *Repository) ListMemberships(ctx context.Context, storeID ids.StoreID) ([]domain.Membership, error) {
	var rows []membershipRow
	if err := repository.db.WithContext(ctx).Where("store_id = ?", storeID.String()).Order("created_at,user_id").Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("list store memberships: %w", err)
	}
	memberships := make([]domain.Membership, 0, len(rows))
	for _, row := range rows {
		userID, err := ids.ParseUserID(row.UserID)
		if err != nil {
			return nil, fmt.Errorf("restore membership user ID: %w", err)
		}
		membership, err := domain.RestoreMembershipWithRole(storeID, userID, domain.MembershipStatus(row.Status), domain.Role(row.Role), row.CreatedAt, row.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("restore membership: %w", err)
		}
		memberships = append(memberships, membership)
	}
	return memberships, nil
}

func (repository *Repository) CreateMembership(ctx context.Context, membership domain.Membership, actorID ids.UserID, requestID string) error {
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockOwner(ctx, tx, membership.StoreID(), actorID); err != nil {
			return err
		}
		row := membershipRow{StoreID: membership.StoreID().String(), UserID: membership.UserID().String(), Status: string(membership.Status()), Role: string(membership.Role()), CreatedAt: membership.CreatedAt(), UpdatedAt: membership.UpdatedAt()}
		if err := tx.Create(&row).Error; err != nil {
			if sqlState(err) == "23505" {
				return application.ErrMembershipTarget
			}
			return fmt.Errorf("insert membership: %w", err)
		}
		return repository.insertAudit(ctx, tx, membership.StoreID(), auditdomain.ActorUser, actorID.String(), "membership.created", "membership", membership.UserID().String(), requestID, membership.CreatedAt())
	})
}

func (repository *Repository) UpdateMembership(ctx context.Context, storeID ids.StoreID, userID ids.UserID, role domain.Role, status domain.MembershipStatus, actorID ids.UserID, requestID string, now time.Time) (domain.Membership, error) {
	var updated domain.Membership
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockOwner(ctx, tx, storeID, actorID); err != nil {
			return err
		}
		var current membershipRow
		err := tx.Raw(`SELECT store_id::text,user_id::text,status,role,created_at,updated_at FROM store_memberships WHERE store_id=?::uuid AND user_id=?::uuid FOR UPDATE`, storeID.String(), userID.String()).Row().Scan(&current.StoreID, &current.UserID, &current.Status, &current.Role, &current.CreatedAt, &current.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return application.ErrMembershipTarget
		}
		if err != nil {
			return fmt.Errorf("lock membership: %w", err)
		}
		if current.Status == string(domain.MembershipStatusActive) && current.Role == string(domain.RoleOwner) && (status != domain.MembershipStatusActive || role != domain.RoleOwner) {
			var otherOwners int64
			if err := tx.Raw(`SELECT count(*) FROM store_memberships WHERE store_id=?::uuid AND user_id<>?::uuid AND status='active' AND role='owner'`, storeID.String(), userID.String()).Scan(&otherOwners).Error; err != nil {
				return fmt.Errorf("count active owners: %w", err)
			}
			if otherOwners == 0 {
				return application.ErrLastOwner
			}
		}
		result := tx.Model(&membershipRow{}).Where("store_id = ? AND user_id = ?", storeID.String(), userID.String()).Updates(map[string]any{"status": string(status), "role": string(role), "updated_at": now})
		if result.Error != nil {
			return fmt.Errorf("update membership: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return application.ErrMembershipTarget
		}
		if err := repository.insertAudit(ctx, tx, storeID, auditdomain.ActorUser, actorID.String(), "membership.updated", "membership", userID.String(), requestID, now); err != nil {
			return err
		}
		updated, err = domain.RestoreMembershipWithRole(storeID, userID, status, role, current.CreatedAt, now)
		return err
	})
	return updated, err
}

func (repository *Repository) ReplaceStaffPermissions(ctx context.Context, storeID ids.StoreID, userID ids.UserID, permissions []domain.Permission, expected int64, actorID ids.UserID, requestID string, now time.Time) (int64, error) {
	var version int64
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockOwner(ctx, tx, storeID, actorID); err != nil {
			return err
		}
		var membership membershipRow
		err := tx.Raw(`SELECT store_id::text,user_id::text,status,role,created_at,updated_at FROM store_memberships WHERE store_id=?::uuid AND user_id=?::uuid FOR UPDATE`, storeID.String(), userID.String()).Row().Scan(&membership.StoreID, &membership.UserID, &membership.Status, &membership.Role, &membership.CreatedAt, &membership.UpdatedAt)
		if errors.Is(err, sql.ErrNoRows) {
			return application.ErrMembershipTarget
		}
		if err != nil {
			return fmt.Errorf("lock staff membership: %w", err)
		}
		if membership.Status != string(domain.MembershipStatusActive) || membership.Role != string(domain.RoleStaff) {
			return application.ErrMembershipTarget
		}
		var set membershipPermissionSetRow
		err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id=? AND user_id=?", storeID.String(), userID.String()).First(&set).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			set = membershipPermissionSetRow{StoreID: storeID.String(), UserID: userID.String(), Version: 0, UpdatedAt: now}
			if err := tx.Create(&set).Error; err != nil {
				return fmt.Errorf("create membership permission set: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("lock membership permission set: %w", err)
		}
		if set.Version != expected {
			return application.ErrMembershipTarget
		}
		version = set.Version + 1
		advanced := tx.Model(&membershipPermissionSetRow{}).Where("store_id=? AND user_id=? AND version=?", storeID.String(), userID.String(), set.Version).Updates(map[string]any{"version": version, "updated_at": now})
		if advanced.Error != nil || advanced.RowsAffected != 1 {
			if advanced.Error != nil {
				return fmt.Errorf("advance membership permission set: %w", advanced.Error)
			}
			return application.ErrMembershipTarget
		}
		if err := tx.Where("store_id=? AND user_id=?", storeID.String(), userID.String()).Delete(&membershipPermissionRow{}).Error; err != nil {
			return fmt.Errorf("replace membership permissions: %w", err)
		}
		for _, permission := range permissions {
			row := membershipPermissionRow{StoreID: storeID.String(), UserID: userID.String(), Permission: string(permission), Version: version, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&row).Error; err != nil {
				return fmt.Errorf("insert membership permission: %w", err)
			}
		}
		return repository.insertAudit(ctx, tx, storeID, auditdomain.ActorUser, actorID.String(), "membership.permissions_updated", "membership", userID.String(), requestID, now)
	})
	return version, err
}

func (repository *Repository) HasPermission(ctx context.Context, storeID ids.StoreID, userID ids.UserID, permission domain.Permission) (bool, error) {
	var allowed bool
	err := repository.db.WithContext(ctx).Raw(`SELECT EXISTS (SELECT 1 FROM store_memberships m JOIN store_membership_permissions p ON p.store_id=m.store_id AND p.user_id=m.user_id WHERE m.store_id=?::uuid AND m.user_id=?::uuid AND m.status='active' AND m.role='staff' AND p.permission=?)`, storeID.String(), userID.String(), string(permission)).Scan(&allowed).Error
	return allowed, err
}

func (repository *Repository) HasPermissionInTransaction(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, userID ids.UserID, permission string) (bool, error) {
	var store storeRow
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Select("store_id", "status").Where("store_id=? AND status='active'", storeID.String()).First(&store).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var row membershipRow
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "SHARE"}).Where("store_id=? AND user_id=?", storeID.String(), userID.String()).First(&row).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if row.Status != string(domain.MembershipStatusActive) {
		return false, nil
	}
	role := domain.Role(row.Role)
	requested := domain.Permission(permission)
	if role.Allows(requested) {
		return true, nil
	}
	if role != domain.RoleStaff || (requested != domain.PermissionPaymentConfirm && requested != domain.PermissionPaymentReconcile && requested != domain.PermissionOrderCancel && requested != domain.PermissionPaymentRefund && requested != domain.PermissionOrderFulfill) {
		return false, nil
	}
	var count int64
	if err := tx.WithContext(ctx).Model(&membershipPermissionRow{}).Where("store_id=? AND user_id=? AND permission=?", storeID.String(), userID.String(), permission).Count(&count).Error; err != nil {
		return false, err
	}
	return count == 1, nil
}

func lockOwner(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, actorID ids.UserID) error {
	var allowed bool
	err := tx.WithContext(ctx).Raw(`SELECT EXISTS (SELECT 1 FROM store_memberships WHERE store_id=?::uuid AND user_id=?::uuid AND status='active' AND role='owner') FROM stores WHERE store_id=?::uuid FOR UPDATE`, storeID.String(), actorID.String(), storeID.String()).Row().Scan(&allowed)
	if errors.Is(err, sql.ErrNoRows) || !allowed {
		return application.ErrTenantAccessDenied
	}
	if err != nil {
		return fmt.Errorf("lock store owner set: %w", err)
	}
	return nil
}

func (repository *Repository) insertAudit(ctx context.Context, tx *gorm.DB, storeID ids.StoreID, actorType auditdomain.ActorType, actorID, action, subjectType, subjectID, requestID string, occurredAt time.Time) error {
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
	var storeIDText, userIDText string
	var record application.TenantRecord
	err := repository.db.WithContext(ctx).Raw(`SELECT s.store_id::text,m.user_id::text,s.subdomain,s.name,s.status,m.status,m.role,s.default_currency,s.locale,s.timezone,s.settings_version FROM store_memberships m JOIN stores s ON s.store_id=m.store_id WHERE m.store_id=?::uuid AND m.user_id=?::uuid`, storeID.String(), userID.String()).Row().Scan(&storeIDText, &userIDText, &record.Subdomain, &record.StoreName, &record.StoreStatus, &record.MembershipStatus, &record.Role, &record.DefaultCurrency, &record.Locale, &record.Timezone, &record.SettingsVersion)
	if errors.Is(err, sql.ErrNoRows) {
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
	record.StoreID, record.UserID = resolvedStoreID, resolvedUserID
	return record, true, nil
}

type stateError interface{ SQLState() string }

func sqlState(err error) string {
	var state stateError
	if errors.As(err, &state) {
		return state.SQLState()
	}
	return ""
}
