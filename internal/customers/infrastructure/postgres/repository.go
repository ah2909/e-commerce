package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/ah2909/e-commerce/internal/customers/application"
	"github.com/ah2909/e-commerce/internal/customers/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

type customerRow struct {
	StoreID        string     `gorm:"column:store_id"`
	CustomerID     string     `gorm:"column:customer_id"`
	UserID         string     `gorm:"column:user_id"`
	FullName       string     `gorm:"column:full_name"`
	Email          string     `gorm:"column:email"`
	CanonicalEmail string     `gorm:"column:canonical_email"`
	Phone          string     `gorm:"column:phone"`
	CanonicalPhone string     `gorm:"column:canonical_phone"`
	Version        int64      `gorm:"column:version"`
	CreatedAt      time.Time  `gorm:"column:created_at"`
	UpdatedAt      time.Time  `gorm:"column:updated_at"`
	ArchivedAt     *time.Time `gorm:"column:archived_at"`
}

func (customerRow) TableName() string { return "customers" }

type addressRow struct {
	StoreID        string     `gorm:"column:store_id"`
	AddressID      string     `gorm:"column:address_id"`
	CustomerID     string     `gorm:"column:customer_id"`
	Label          string     `gorm:"column:label"`
	RecipientName  string     `gorm:"column:recipient_name"`
	Phone          string     `gorm:"column:phone"`
	CanonicalPhone string     `gorm:"column:canonical_phone"`
	Line1          string     `gorm:"column:line1"`
	Line2          string     `gorm:"column:line2"`
	Ward           string     `gorm:"column:ward"`
	District       string     `gorm:"column:district"`
	Province       string     `gorm:"column:province"`
	PostalCode     string     `gorm:"column:postal_code"`
	CountryCode    string     `gorm:"column:country_code"`
	Version        int64      `gorm:"column:version"`
	CreatedAt      time.Time  `gorm:"column:created_at"`
	UpdatedAt      time.Time  `gorm:"column:updated_at"`
	ArchivedAt     *time.Time `gorm:"column:archived_at"`
}

func (addressRow) TableName() string { return "customer_addresses" }

func (repository *Repository) GetProfile(ctx context.Context, storeID ids.StoreID, userID ids.UserID) (domain.Profile, error) {
	var row customerRow
	err := repository.db.WithContext(ctx).Where("store_id = ? AND user_id = ? AND archived_at IS NULL", storeID, userID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Profile{}, application.ErrNotFound
	}
	if err != nil {
		return domain.Profile{}, err
	}
	return profile(row), nil
}

func (repository *Repository) PutProfile(ctx context.Context, storeID ids.StoreID, userID ids.UserID, id ids.CustomerID, expected int64, draft domain.ProfileDraft) (domain.Profile, error) {
	validated, email, phone, err := domain.ValidateProfileDraft(draft)
	if err != nil {
		return domain.Profile{}, err
	}
	var result domain.Profile
	err = repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current customerRow
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("store_id = ? AND user_id = ? AND archived_at IS NULL", storeID, userID).First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if expected != 0 {
				return application.ErrNotFound
			}
			now, err := databaseTime(tx)
			if err != nil {
				return err
			}
			created := customerRow{StoreID: storeID.String(), CustomerID: id.String(), UserID: userID.String(), FullName: validated.FullName, Email: validated.Email, CanonicalEmail: email, Phone: validated.Phone, CanonicalPhone: phone, Version: 1, CreatedAt: now, UpdatedAt: now}
			if err := tx.Create(&created).Error; err != nil {
				return classify(err)
			}
			result = profile(created)
			return nil
		}
		if err != nil {
			return err
		}
		if expected == 0 || current.Version != expected {
			return application.ErrConflict
		}
		now, err := databaseTime(tx)
		if err != nil {
			return err
		}
		updates := map[string]any{"full_name": validated.FullName, "email": validated.Email, "canonical_email": email, "phone": validated.Phone, "canonical_phone": phone, "version": gorm.Expr("version + 1"), "updated_at": now}
		res := tx.Model(&customerRow{}).Where("store_id = ? AND customer_id = ? AND version = ? AND archived_at IS NULL", storeID, current.CustomerID, expected).Updates(updates)
		if res.Error != nil {
			return classify(res.Error)
		}
		if res.RowsAffected != 1 {
			return application.ErrConflict
		}
		if err := tx.Where("store_id = ? AND customer_id = ?", storeID, current.CustomerID).First(&current).Error; err != nil {
			return err
		}
		result = profile(current)
		return nil
	})
	if err != nil {
		return domain.Profile{}, err
	}
	return result, err
}

func (repository *Repository) CreateAddress(ctx context.Context, storeID ids.StoreID, userID ids.UserID, id ids.AddressID, draft domain.AddressDraft) (domain.Address, error) {
	validated, phone, err := domain.ValidateAddressDraft(draft)
	if err != nil {
		return domain.Address{}, err
	}
	var created addressRow
	err = repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		customer, err := ownedCustomer(tx, storeID, userID)
		if err != nil {
			return err
		}
		now, err := databaseTime(tx)
		if err != nil {
			return err
		}
		created = addressRowFrom(storeID, id, customer.CustomerID, validated, phone, now)
		return classify(tx.Create(&created).Error)
	})
	return address(created), err
}

func (repository *Repository) GetAddress(ctx context.Context, storeID ids.StoreID, userID ids.UserID, id ids.AddressID) (domain.Address, error) {
	var row addressRow
	err := repository.ownedAddress(repository.db.WithContext(ctx), storeID, userID, id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.Address{}, application.ErrNotFound
	}
	return address(row), err
}

func (repository *Repository) ListAddresses(ctx context.Context, storeID ids.StoreID, userID ids.UserID, limit int, cursor *application.Cursor) ([]domain.Address, error) {
	base := repository.db.WithContext(ctx).Table("customer_addresses a").Joins("JOIN customers c ON c.store_id = a.store_id AND c.customer_id = a.customer_id").Where("a.store_id = ? AND c.user_id = ? AND c.archived_at IS NULL AND a.archived_at IS NULL", storeID, userID)
	if cursor != nil {
		var anchors int64
		if err := base.Session(&gorm.Session{}).Where("a.address_id = ? AND a.created_at = ?", cursor.AddressID, cursor.CreatedAt).Count(&anchors).Error; err != nil {
			return nil, err
		}
		if anchors != 1 {
			return nil, application.ErrNotFound
		}
		base = base.Where("(a.created_at, a.address_id) < (?, ?)", cursor.CreatedAt, cursor.AddressID)
	}
	var rows []addressRow
	if err := base.Select("a.*").Order("a.created_at DESC, a.address_id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	values := make([]domain.Address, len(rows))
	for i := range rows {
		values[i] = address(rows[i])
	}
	return values, nil
}

func (repository *Repository) ReplaceAddress(ctx context.Context, storeID ids.StoreID, userID ids.UserID, id ids.AddressID, expected int64, draft domain.AddressDraft) (domain.Address, error) {
	validated, phone, err := domain.ValidateAddressDraft(draft)
	if err != nil {
		return domain.Address{}, err
	}
	var result domain.Address
	err = repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current addressRow
		if err := repository.ownedAddress(tx.Clauses(clause.Locking{Strength: "UPDATE"}), storeID, userID, id).First(&current).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return application.ErrNotFound
		} else if err != nil {
			return err
		}
		if current.Version != expected {
			return application.ErrConflict
		}
		now, err := databaseTime(tx)
		if err != nil {
			return err
		}
		updates := addressUpdates(validated, phone, now)
		res := tx.Model(&addressRow{}).Where("store_id = ? AND address_id = ? AND version = ? AND archived_at IS NULL", storeID, id, expected).Updates(updates)
		if res.Error != nil {
			return classify(res.Error)
		}
		if res.RowsAffected != 1 {
			return application.ErrConflict
		}
		if err := tx.Where("store_id = ? AND address_id = ?", storeID, id).First(&current).Error; err != nil {
			return err
		}
		result = address(current)
		return nil
	})
	return result, err
}

func (repository *Repository) ArchiveAddress(ctx context.Context, storeID ids.StoreID, userID ids.UserID, id ids.AddressID, expected int64) error {
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current addressRow
		if err := repository.ownedAddress(tx.Clauses(clause.Locking{Strength: "UPDATE"}), storeID, userID, id).First(&current).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return application.ErrNotFound
		} else if err != nil {
			return err
		}
		if current.Version != expected {
			return application.ErrConflict
		}
		now, err := databaseTime(tx)
		if err != nil {
			return err
		}
		res := tx.Model(&addressRow{}).Where("store_id = ? AND address_id = ? AND version = ? AND archived_at IS NULL", storeID, id, expected).Updates(map[string]any{"version": gorm.Expr("version + 1"), "updated_at": now, "archived_at": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return application.ErrConflict
		}
		return nil
	})
}

func ownedCustomer(tx *gorm.DB, storeID ids.StoreID, userID ids.UserID) (customerRow, error) {
	var row customerRow
	err := tx.Where("store_id = ? AND user_id = ? AND archived_at IS NULL", storeID, userID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = application.ErrNotFound
	}
	return row, err
}

func (repository *Repository) ownedAddress(tx *gorm.DB, storeID ids.StoreID, userID ids.UserID, id ids.AddressID) *gorm.DB {
	return tx.Table("customer_addresses a").Select("a.*").Joins("JOIN customers c ON c.store_id = a.store_id AND c.customer_id = a.customer_id").Where("a.store_id = ? AND a.address_id = ? AND c.user_id = ? AND c.archived_at IS NULL AND a.archived_at IS NULL", storeID, id, userID)
}

func databaseTime(tx *gorm.DB) (time.Time, error) {
	var now time.Time
	err := tx.Raw("SELECT clock_timestamp()").Scan(&now).Error
	return now.UTC(), err
}

func profile(row customerRow) domain.Profile {
	return domain.Profile{StoreID: ids.StoreID(row.StoreID), ID: ids.CustomerID(row.CustomerID), UserID: ids.UserID(row.UserID), FullName: row.FullName, Email: row.Email, Phone: row.Phone, CanonicalMail: row.CanonicalEmail, CanonicalPhone: row.CanonicalPhone, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ArchivedAt: row.ArchivedAt}
}

func address(row addressRow) domain.Address {
	return domain.Address{StoreID: ids.StoreID(row.StoreID), ID: ids.AddressID(row.AddressID), CustomerID: ids.CustomerID(row.CustomerID), AddressDraft: domain.AddressDraft{Label: row.Label, RecipientName: row.RecipientName, Phone: row.Phone, Line1: row.Line1, Line2: row.Line2, Ward: row.Ward, District: row.District, Province: row.Province, PostalCode: row.PostalCode, CountryCode: row.CountryCode}, CanonicalPhone: row.CanonicalPhone, Version: row.Version, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ArchivedAt: row.ArchivedAt}
}

func addressRowFrom(storeID ids.StoreID, id ids.AddressID, customerID string, draft domain.AddressDraft, phone string, now time.Time) addressRow {
	return addressRow{StoreID: storeID.String(), AddressID: id.String(), CustomerID: customerID, Label: draft.Label, RecipientName: draft.RecipientName, Phone: draft.Phone, CanonicalPhone: phone, Line1: draft.Line1, Line2: draft.Line2, Ward: draft.Ward, District: draft.District, Province: draft.Province, PostalCode: draft.PostalCode, CountryCode: draft.CountryCode, Version: 1, CreatedAt: now, UpdatedAt: now}
}

func addressUpdates(draft domain.AddressDraft, phone string, now time.Time) map[string]any {
	return map[string]any{"label": draft.Label, "recipient_name": draft.RecipientName, "phone": draft.Phone, "canonical_phone": phone, "line1": draft.Line1, "line2": draft.Line2, "ward": draft.Ward, "district": draft.District, "province": draft.Province, "postal_code": draft.PostalCode, "country_code": draft.CountryCode, "version": gorm.Expr("version + 1"), "updated_at": now}
}

type stateError interface{ SQLState() string }

func classify(err error) error {
	var state stateError
	if errors.As(err, &state) && (state.SQLState() == "23505" || state.SQLState() == "23503" || state.SQLState() == "23514") {
		return application.ErrConflict
	}
	return err
}
