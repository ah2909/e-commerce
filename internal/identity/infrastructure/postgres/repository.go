package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/ah2909/e-commerce/internal/identity/application"
	identitydomain "github.com/ah2909/e-commerce/internal/identity/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"gorm.io/gorm"
)

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

type userRow struct {
	StoreID        string    `gorm:"column:store_id;type:uuid;primaryKey"`
	UserID         string    `gorm:"column:user_id;type:uuid;primaryKey"`
	Email          *string   `gorm:"column:email"`
	CanonicalEmail *string   `gorm:"column:canonical_email"`
	Status         string    `gorm:"column:status"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
}

func (userRow) TableName() string { return "users" }

type passwordRow struct {
	StoreID      string    `gorm:"column:store_id;type:uuid;primaryKey"`
	UserID       string    `gorm:"column:user_id;type:uuid;primaryKey"`
	PasswordHash string    `gorm:"column:password_hash"`
	UpdatedAt    time.Time `gorm:"column:updated_at"`
}

func (passwordRow) TableName() string { return "identity_password_credentials" }

type userSessionRow struct {
	StoreID       string     `gorm:"column:store_id;type:uuid;primaryKey"`
	SessionID     string     `gorm:"column:session_id;type:uuid;primaryKey"`
	UserID        string     `gorm:"column:user_id;type:uuid"`
	ExpiresAt     time.Time  `gorm:"column:expires_at"`
	RevokedAt     *time.Time `gorm:"column:revoked_at"`
	CompromisedAt *time.Time `gorm:"column:compromised_at"`
	CreatedAt     time.Time  `gorm:"column:created_at"`
}

func (userSessionRow) TableName() string { return "identity_sessions" }

type tokenRow struct {
	StoreID   string    `gorm:"column:store_id;type:uuid;primaryKey"`
	Digest    []byte    `gorm:"column:token_digest;primaryKey"`
	SessionID string    `gorm:"column:session_id;type:uuid"`
	ExpiresAt time.Time `gorm:"column:expires_at"`
	CreatedAt time.Time `gorm:"column:created_at"`
}

type guestRow struct {
	StoreID   string    `gorm:"column:store_id;type:uuid;primaryKey"`
	GuestID   string    `gorm:"column:guest_id;type:uuid;primaryKey"`
	CreatedAt time.Time `gorm:"column:created_at"`
	ExpiresAt time.Time `gorm:"column:expires_at"`
}

func (guestRow) TableName() string { return "identity_guests" }

type guestSessionRow struct {
	StoreID       string     `gorm:"column:store_id;type:uuid;primaryKey"`
	SessionID     string     `gorm:"column:session_id;type:uuid;primaryKey"`
	GuestID       string     `gorm:"column:guest_id;type:uuid"`
	ExpiresAt     time.Time  `gorm:"column:expires_at"`
	RevokedAt     *time.Time `gorm:"column:revoked_at"`
	CompromisedAt *time.Time `gorm:"column:compromised_at"`
	CreatedAt     time.Time  `gorm:"column:created_at"`
}

func (guestSessionRow) TableName() string { return "identity_guest_sessions" }

type socialIdentityRow struct {
	StoreID         string    `gorm:"column:store_id;type:uuid;primaryKey"`
	Provider        string    `gorm:"column:provider;primaryKey"`
	ProviderSubject string    `gorm:"column:provider_subject;primaryKey"`
	UserID          string    `gorm:"column:user_id;type:uuid"`
	CreatedAt       time.Time `gorm:"column:created_at"`
}

func (socialIdentityRow) TableName() string { return "identity_social_identities" }

func (repository *Repository) Register(ctx context.Context, user application.UserRecord, passwordHash string, session application.SessionRecord) error {
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return repository.InsertRegistration(ctx, tx, user, passwordHash, session)
	})
	if sqlState(err) == "23505" {
		return application.ErrIdentityExists
	}
	if err != nil {
		return fmt.Errorf("register transaction: %w", err)
	}
	return nil
}

func (repository *Repository) InsertRegistration(ctx context.Context, tx *gorm.DB, user application.UserRecord, passwordHash string, session application.SessionRecord) error {
	if session.StoreID != user.StoreID || session.UserID != user.ID {
		return errors.New("registration store scope mismatch")
	}
	if err := repository.InsertAccount(ctx, tx, application.PreparedAccount{User: user, PasswordHash: passwordHash}); err != nil {
		return err
	}
	return insertUserSession(ctx, tx, session)
}

func (repository *Repository) InsertAccount(ctx context.Context, tx *gorm.DB, account application.PreparedAccount) error {
	user := account.User
	if user.StoreID == "" {
		return errors.New("registration store scope mismatch")
	}
	var presented, canonical *string
	if user.Email != nil {
		presentedValue, canonicalValue := user.Email.Presented(), user.Email.Canonical()
		presented, canonical = &presentedValue, &canonicalValue
	}
	if err := tx.WithContext(ctx).Create(&userRow{StoreID: user.StoreID.String(), UserID: user.ID.String(), Email: presented, CanonicalEmail: canonical, Status: string(user.Status), CreatedAt: user.CreatedAt, UpdatedAt: user.UpdatedAt}).Error; err != nil {
		return err
	}
	return tx.WithContext(ctx).Create(&passwordRow{StoreID: user.StoreID.String(), UserID: user.ID.String(), PasswordHash: account.PasswordHash, UpdatedAt: user.UpdatedAt}).Error
}

func (repository *Repository) PasswordUser(ctx context.Context, storeID ids.StoreID, canonicalEmail string) (application.UserRecord, string, bool, error) {
	var idText, presented, canonical, hash string
	var user application.UserRecord
	err := repository.db.WithContext(ctx).Raw(`SELECT u.user_id::text,u.email,u.canonical_email,u.status,u.created_at,u.updated_at,p.password_hash FROM users u JOIN identity_password_credentials p ON p.store_id=u.store_id AND p.user_id=u.user_id WHERE u.store_id=?::uuid AND u.canonical_email=?`, storeID.String(), canonicalEmail).Row().Scan(&idText, &presented, &canonical, &user.Status, &user.CreatedAt, &user.UpdatedAt, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		return application.UserRecord{}, "", false, nil
	}
	if err != nil {
		return application.UserRecord{}, "", false, fmt.Errorf("query password user: %w", err)
	}
	userID, err := ids.ParseUserID(idText)
	if err != nil {
		return application.UserRecord{}, "", false, err
	}
	email, err := identitydomain.ParseEmail(presented)
	if err != nil || email.Canonical() != canonical {
		return application.UserRecord{}, "", false, errors.New("invalid persisted email")
	}
	user.StoreID, user.ID, user.Email = storeID, userID, &email
	return user, hash, true, nil
}

func (repository *Repository) UpdatePasswordHash(ctx context.Context, storeID ids.StoreID, userID ids.UserID, currentHash, replacementHash string, now time.Time) error {
	return repository.db.WithContext(ctx).Model(&passwordRow{}).Where("store_id = ? AND user_id = ? AND password_hash = ?", storeID.String(), userID.String(), currentHash).Updates(map[string]any{"password_hash": replacementHash, "updated_at": now}).Error
}

func (repository *Repository) IsActiveUser(ctx context.Context, storeID ids.StoreID, userID ids.UserID) (bool, error) {
	var row userRow
	err := repository.db.WithContext(ctx).Select("status").Where("store_id = ? AND user_id = ?", storeID.String(), userID.String()).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return row.Status == string(identitydomain.UserStatusActive), err
}

func (repository *Repository) CreateUserSession(ctx context.Context, session application.SessionRecord) (bool, error) {
	created := false
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var status identitydomain.UserStatus
		err := tx.Raw(`SELECT status FROM users WHERE store_id=?::uuid AND user_id=?::uuid FOR UPDATE`, session.StoreID.String(), session.UserID.String()).Row().Scan(&status)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil || status != identitydomain.UserStatusActive {
			return err
		}
		if err := insertUserSession(ctx, tx, session); err != nil {
			return err
		}
		created = true
		return nil
	})
	return created, err
}

func insertUserSession(ctx context.Context, tx *gorm.DB, session application.SessionRecord) error {
	if err := tx.WithContext(ctx).Create(&userSessionRow{StoreID: session.StoreID.String(), SessionID: session.SessionID, UserID: session.UserID.String(), ExpiresAt: session.ExpiresAt, CreatedAt: session.CreatedAt}).Error; err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	if err := createToken(ctx, tx, "identity_access_tokens", session.StoreID, session.SessionID, session.Access); err != nil {
		return fmt.Errorf("insert access token: %w", err)
	}
	if err := createToken(ctx, tx, "identity_refresh_tokens", session.StoreID, session.SessionID, session.Refresh); err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}

func createToken(ctx context.Context, tx *gorm.DB, table string, storeID ids.StoreID, sessionID string, token application.TokenRecord) error {
	row := tokenRow{StoreID: storeID.String(), Digest: token.Digest[:], SessionID: sessionID, ExpiresAt: token.ExpiresAt, CreatedAt: token.CreatedAt}
	return tx.WithContext(ctx).Table(table).Create(&row).Error
}

func (repository *Repository) AuthenticateUser(ctx context.Context, storeID ids.StoreID, digest identitydomain.TokenDigest, now time.Time) (application.UserRecord, string, bool, error) {
	var idText, sessionID string
	var presented, canonical *string
	var user application.UserRecord
	err := repository.db.WithContext(ctx).Raw(`SELECT u.user_id::text,u.email,u.canonical_email,u.status,u.created_at,u.updated_at,s.session_id::text FROM identity_access_tokens a JOIN identity_sessions s ON s.store_id=a.store_id AND s.session_id=a.session_id JOIN users u ON u.store_id=s.store_id AND u.user_id=s.user_id WHERE a.store_id=?::uuid AND a.token_digest=? AND a.expires_at>? AND s.expires_at>? AND s.revoked_at IS NULL AND s.compromised_at IS NULL`, storeID.String(), digest[:], now, now).Row().Scan(&idText, &presented, &canonical, &user.Status, &user.CreatedAt, &user.UpdatedAt, &sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return application.UserRecord{}, "", false, nil
	}
	if err != nil {
		return application.UserRecord{}, "", false, fmt.Errorf("query access token: %w", err)
	}
	userID, err := ids.ParseUserID(idText)
	if err != nil {
		return application.UserRecord{}, "", false, err
	}
	user.StoreID, user.ID = storeID, userID
	if presented != nil && canonical != nil {
		email, parseErr := identitydomain.ParseEmail(*presented)
		if parseErr != nil || email.Canonical() != *canonical {
			return application.UserRecord{}, "", false, errors.New("invalid persisted email")
		}
		user.Email = &email
	}
	return user, sessionID, true, nil
}

func (repository *Repository) RotateUser(ctx context.Context, storeID ids.StoreID, old identitydomain.TokenDigest, access, refresh application.TokenRecord, now time.Time) (sessionID string, sessionExpires time.Time, replay bool, err error) {
	err = repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tokenExpires time.Time
		var userStatus identitydomain.UserStatus
		var usedAt, tokenRevoked, sessionRevoked, compromised *time.Time
		scanErr := tx.Raw(`SELECT r.session_id::text,r.expires_at,r.used_at,r.revoked_at,s.expires_at,s.revoked_at,s.compromised_at,u.status FROM identity_refresh_tokens r JOIN identity_sessions s ON s.store_id=r.store_id AND s.session_id=r.session_id JOIN users u ON u.store_id=s.store_id AND u.user_id=s.user_id WHERE r.store_id=?::uuid AND r.token_digest=? FOR UPDATE OF r,s,u`, storeID.String(), old[:]).Row().Scan(&sessionID, &tokenExpires, &usedAt, &tokenRevoked, &sessionExpires, &sessionRevoked, &compromised, &userStatus)
		if errors.Is(scanErr, sql.ErrNoRows) {
			sessionID = ""
			return nil
		}
		if scanErr != nil {
			return scanErr
		}
		if userStatus != identitydomain.UserStatusActive {
			if err := revokeUserFamily(tx, storeID, sessionID, now); err != nil {
				return err
			}
			sessionID = ""
			return nil
		}
		if usedAt != nil || tokenRevoked != nil {
			if err := compromiseUserFamily(tx, storeID, sessionID, now); err != nil {
				return err
			}
			replay = true
			return nil
		}
		if !tokenExpires.After(now) || !sessionExpires.After(now) || sessionRevoked != nil || compromised != nil {
			sessionID = ""
			return nil
		}
		if access.ExpiresAt.After(sessionExpires) {
			access.ExpiresAt = sessionExpires
		}
		refresh.ExpiresAt = sessionExpires
		if err := tx.Model(&tokenRow{}).Table("identity_refresh_tokens").Where("store_id = ? AND token_digest = ?", storeID.String(), old[:]).Update("used_at", now).Error; err != nil {
			return err
		}
		if err := createToken(ctx, tx, "identity_access_tokens", storeID, sessionID, access); err != nil {
			return err
		}
		return createToken(ctx, tx, "identity_refresh_tokens", storeID, sessionID, refresh)
	})
	return sessionID, sessionExpires, replay, err
}

func revokeUserFamily(tx *gorm.DB, storeID ids.StoreID, sessionID string, now time.Time) error {
	if err := tx.Exec(`UPDATE identity_sessions SET revoked_at=COALESCE(revoked_at,?) WHERE store_id=?::uuid AND session_id=?::uuid`, now, storeID.String(), sessionID).Error; err != nil {
		return err
	}
	return tx.Exec(`UPDATE identity_refresh_tokens SET revoked_at=COALESCE(revoked_at,?) WHERE store_id=?::uuid AND session_id=?::uuid`, now, storeID.String(), sessionID).Error
}

func compromiseUserFamily(tx *gorm.DB, storeID ids.StoreID, sessionID string, now time.Time) error {
	if err := tx.Exec(`UPDATE identity_sessions SET compromised_at=?,revoked_at=? WHERE store_id=?::uuid AND session_id=?::uuid AND compromised_at IS NULL`, now, now, storeID.String(), sessionID).Error; err != nil {
		return err
	}
	return tx.Exec(`UPDATE identity_refresh_tokens SET revoked_at=COALESCE(revoked_at,?) WHERE store_id=?::uuid AND session_id=?::uuid`, now, storeID.String(), sessionID).Error
}

func (repository *Repository) RevokeUserSession(ctx context.Context, storeID ids.StoreID, digest identitydomain.TokenDigest, now time.Time) (bool, error) {
	result := repository.db.WithContext(ctx).Exec(`UPDATE identity_sessions s SET revoked_at=COALESCE(s.revoked_at,?) FROM identity_access_tokens a WHERE s.store_id=?::uuid AND a.store_id=s.store_id AND a.session_id=s.session_id AND a.token_digest=?`, now, storeID.String(), digest[:])
	return result.RowsAffected == 1, result.Error
}

func (repository *Repository) CreateGuest(ctx context.Context, guest application.GuestRecord, session application.SessionRecord) error {
	return repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&guestRow{StoreID: guest.StoreID.String(), GuestID: guest.GuestID.String(), CreatedAt: guest.CreatedAt, ExpiresAt: guest.ExpiresAt}).Error; err != nil {
			return err
		}
		if err := tx.Create(&guestSessionRow{StoreID: session.StoreID.String(), SessionID: session.SessionID, GuestID: session.GuestID.String(), ExpiresAt: session.ExpiresAt, CreatedAt: session.CreatedAt}).Error; err != nil {
			return err
		}
		if err := createToken(ctx, tx, "identity_guest_access_tokens", session.StoreID, session.SessionID, session.Access); err != nil {
			return err
		}
		return createToken(ctx, tx, "identity_guest_refresh_tokens", session.StoreID, session.SessionID, session.Refresh)
	})
}

func (repository *Repository) AuthenticateGuest(ctx context.Context, storeID ids.StoreID, digest identitydomain.TokenDigest, now time.Time) (ids.GuestID, string, time.Time, time.Time, bool, error) {
	var guestText, sessionID string
	var accessExpires, sessionExpires time.Time
	err := repository.db.WithContext(ctx).Raw(`SELECT s.guest_id::text,s.session_id::text,a.expires_at,s.expires_at FROM identity_guest_access_tokens a JOIN identity_guest_sessions s ON s.store_id=a.store_id AND s.session_id=a.session_id WHERE a.store_id=?::uuid AND a.token_digest=? AND a.expires_at>? AND s.expires_at>? AND s.revoked_at IS NULL AND s.compromised_at IS NULL`, storeID.String(), digest[:], now, now).Row().Scan(&guestText, &sessionID, &accessExpires, &sessionExpires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", time.Time{}, time.Time{}, false, nil
	}
	if err != nil {
		return "", "", time.Time{}, time.Time{}, false, err
	}
	guestID, err := ids.ParseGuestID(guestText)
	return guestID, sessionID, accessExpires, sessionExpires, err == nil, err
}

func (repository *Repository) RotateGuest(ctx context.Context, storeID ids.StoreID, old identitydomain.TokenDigest, access, refresh application.TokenRecord, now time.Time) (sessionID string, sessionExpires time.Time, replay bool, err error) {
	err = repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tokenExpires time.Time
		var usedAt, tokenRevoked, sessionRevoked, compromised *time.Time
		scanErr := tx.Raw(`SELECT r.session_id::text,r.expires_at,r.used_at,r.revoked_at,s.expires_at,s.revoked_at,s.compromised_at FROM identity_guest_refresh_tokens r JOIN identity_guest_sessions s ON s.store_id=r.store_id AND s.session_id=r.session_id WHERE r.store_id=?::uuid AND r.token_digest=? FOR UPDATE OF r,s`, storeID.String(), old[:]).Row().Scan(&sessionID, &tokenExpires, &usedAt, &tokenRevoked, &sessionExpires, &sessionRevoked, &compromised)
		if errors.Is(scanErr, sql.ErrNoRows) {
			sessionID = ""
			return nil
		}
		if scanErr != nil {
			return scanErr
		}
		if usedAt != nil || tokenRevoked != nil {
			if err := tx.Exec(`UPDATE identity_guest_sessions SET compromised_at=?,revoked_at=? WHERE store_id=?::uuid AND session_id=?::uuid AND compromised_at IS NULL`, now, now, storeID.String(), sessionID).Error; err != nil {
				return err
			}
			if err := tx.Exec(`UPDATE identity_guest_refresh_tokens SET revoked_at=COALESCE(revoked_at,?) WHERE store_id=?::uuid AND session_id=?::uuid`, now, storeID.String(), sessionID).Error; err != nil {
				return err
			}
			replay = true
			return nil
		}
		if !tokenExpires.After(now) || !sessionExpires.After(now) || sessionRevoked != nil || compromised != nil {
			sessionID = ""
			return nil
		}
		if access.ExpiresAt.After(sessionExpires) {
			access.ExpiresAt = sessionExpires
		}
		refresh.ExpiresAt = sessionExpires
		if err := tx.Model(&tokenRow{}).Table("identity_guest_refresh_tokens").Where("store_id = ? AND token_digest = ?", storeID.String(), old[:]).Update("used_at", now).Error; err != nil {
			return err
		}
		if err := createToken(ctx, tx, "identity_guest_access_tokens", storeID, sessionID, access); err != nil {
			return err
		}
		return createToken(ctx, tx, "identity_guest_refresh_tokens", storeID, sessionID, refresh)
	})
	return sessionID, sessionExpires, replay, err
}

func (repository *Repository) RevokeGuestSession(ctx context.Context, storeID ids.StoreID, digest identitydomain.TokenDigest, now time.Time) (bool, error) {
	result := repository.db.WithContext(ctx).Exec(`UPDATE identity_guest_sessions s SET revoked_at=COALESCE(s.revoked_at,?) FROM identity_guest_access_tokens a WHERE s.store_id=?::uuid AND a.store_id=s.store_id AND a.session_id=s.session_id AND a.token_digest=?`, now, storeID.String(), digest[:])
	return result.RowsAffected == 1, result.Error
}

var errSocialRace = errors.New("social identity race")

func (repository *Repository) ResolveSocial(ctx context.Context, storeID ids.StoreID, provider identitydomain.Provider, subject string, email *identitydomain.Email, newUserID ids.UserID, now time.Time) (application.UserRecord, error) {
	var resolved application.UserRecord
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		lockKey := storeID.String() + "|" + string(provider) + "|" + subject
		if err := tx.Exec(`SELECT pg_advisory_xact_lock(hashtextextended(?,0))`, lockKey).Error; err != nil {
			return err
		}
		user, found, err := socialUser(ctx, tx, storeID, provider, subject)
		if err != nil {
			return err
		}
		if found {
			resolved = user
			return nil
		}
		var presented, canonical *string
		if email != nil {
			presentedValue, canonicalValue := email.Presented(), email.Canonical()
			presented, canonical = &presentedValue, &canonicalValue
		}
		if err := tx.Create(&userRow{StoreID: storeID.String(), UserID: newUserID.String(), Email: presented, CanonicalEmail: canonical, Status: string(identitydomain.UserStatusActive), CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			if sqlState(err) == "23505" {
				return application.ErrIdentityExists
			}
			return err
		}
		row := socialIdentityRow{StoreID: storeID.String(), Provider: string(provider), ProviderSubject: subject, UserID: newUserID.String(), CreatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			if sqlState(err) == "23505" {
				return errSocialRace
			}
			return err
		}
		resolved = application.UserRecord{StoreID: storeID, ID: newUserID, Email: email, Status: identitydomain.UserStatusActive, CreatedAt: now, UpdatedAt: now}
		return nil
	})
	if errors.Is(err, errSocialRace) {
		winner, found, lookupErr := socialUser(ctx, repository.db, storeID, provider, subject)
		if lookupErr != nil {
			return application.UserRecord{}, lookupErr
		}
		if !found {
			return application.UserRecord{}, application.ErrIdentityExists
		}
		return winner, nil
	}
	return resolved, err
}

func (repository *Repository) LinkSocial(ctx context.Context, storeID ids.StoreID, userID ids.UserID, sessionID string, provider identitydomain.Provider, subject string, now time.Time) error {
	result := repository.db.WithContext(ctx).Exec(`INSERT INTO identity_social_identities (store_id,provider,provider_subject,user_id,created_at) SELECT ?::uuid,?,?,u.user_id,? FROM users u JOIN identity_sessions s ON s.store_id=u.store_id AND s.user_id=u.user_id WHERE u.store_id=?::uuid AND u.user_id=?::uuid AND s.session_id=?::uuid AND u.status='active' AND s.expires_at>? AND s.revoked_at IS NULL AND s.compromised_at IS NULL`, storeID.String(), provider, subject, now, storeID.String(), userID.String(), sessionID, now)
	if sqlState(result.Error) == "23505" {
		return application.ErrIdentityExists
	}
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return application.ErrUnauthenticated
	}
	return nil
}

func socialUser(ctx context.Context, db *gorm.DB, storeID ids.StoreID, provider identitydomain.Provider, subject string) (application.UserRecord, bool, error) {
	var idText string
	var presented, canonical *string
	var user application.UserRecord
	err := db.WithContext(ctx).Raw(`SELECT u.user_id::text,u.email,u.canonical_email,u.status,u.created_at,u.updated_at FROM identity_social_identities i JOIN users u ON u.store_id=i.store_id AND u.user_id=i.user_id WHERE i.store_id=?::uuid AND i.provider=? AND i.provider_subject=?`, storeID.String(), provider, subject).Row().Scan(&idText, &presented, &canonical, &user.Status, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return application.UserRecord{}, false, nil
	}
	if err != nil {
		return application.UserRecord{}, false, err
	}
	userID, err := ids.ParseUserID(idText)
	if err != nil {
		return application.UserRecord{}, false, err
	}
	user.StoreID, user.ID = storeID, userID
	if presented != nil && canonical != nil {
		email, parseErr := identitydomain.ParseEmail(*presented)
		if parseErr != nil || email.Canonical() != *canonical {
			return application.UserRecord{}, false, errors.New("invalid persisted email")
		}
		user.Email = &email
	}
	return user, true, nil
}

type stateError interface{ SQLState() string }

func sqlState(err error) string {
	var state stateError
	if errors.As(err, &state) {
		return state.SQLState()
	}
	return ""
}

var _ application.Repository = (*Repository)(nil)
