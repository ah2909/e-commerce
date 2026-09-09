package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ah2909/e-commerce/internal/identity/application"
	identitydomain "github.com/ah2909/e-commerce/internal/identity/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ pool *pgxpool.Pool }

func NewRepository(pool *pgxpool.Pool) *Repository { return &Repository{pool: pool} }

func (repository *Repository) Register(ctx context.Context, user application.UserRecord, passwordHash string, session application.SessionRecord) error {
	err := pgx.BeginFunc(ctx, repository.pool, func(tx pgx.Tx) error {
		return repository.InsertRegistration(ctx, tx, user, passwordHash, session)
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return application.ErrIdentityExists
	}
	if err != nil {
		return fmt.Errorf("register transaction: %w", err)
	}
	return nil
}

func (repository *Repository) InsertRegistration(ctx context.Context, tx pgx.Tx, user application.UserRecord, passwordHash string, session application.SessionRecord) error {
	if session.StoreID != user.StoreID || session.UserID != user.ID {
		return errors.New("registration store scope mismatch")
	}
	if err := repository.InsertAccount(ctx, tx, application.PreparedAccount{User: user, PasswordHash: passwordHash}); err != nil {
		return err
	}
	return insertUserSession(ctx, tx, session)
}

func (repository *Repository) InsertAccount(ctx context.Context, tx pgx.Tx, account application.PreparedAccount) error {
	user, passwordHash := account.User, account.PasswordHash
	var email, canonical any
	if user.Email != nil {
		email, canonical = user.Email.Presented(), user.Email.Canonical()
	}
	if user.StoreID == "" {
		return errors.New("registration store scope mismatch")
	}
	if _, err := tx.Exec(ctx, `INSERT INTO users (store_id,user_id,email,canonical_email,status,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6,$7)`, user.StoreID.String(), user.ID.String(), email, canonical, user.Status, user.CreatedAt, user.UpdatedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO identity_password_credentials (store_id,user_id,password_hash,updated_at) VALUES ($1::uuid,$2::uuid,$3,$4)`, user.StoreID.String(), user.ID.String(), passwordHash, user.UpdatedAt); err != nil {
		return err
	}
	return nil
}

func (repository *Repository) PasswordUser(ctx context.Context, storeID ids.StoreID, canonicalEmail string) (application.UserRecord, string, bool, error) {
	row := repository.pool.QueryRow(ctx, `SELECT u.user_id::text,u.email,u.canonical_email,u.status,u.created_at,u.updated_at,p.password_hash FROM users u JOIN identity_password_credentials p ON p.store_id=u.store_id AND p.user_id=u.user_id WHERE u.store_id=$1::uuid AND u.canonical_email=$2`, storeID.String(), canonicalEmail)
	var idText, presented, canonical, hash string
	var user application.UserRecord
	if err := row.Scan(&idText, &presented, &canonical, &user.Status, &user.CreatedAt, &user.UpdatedAt, &hash); errors.Is(err, pgx.ErrNoRows) {
		return application.UserRecord{}, "", false, nil
	} else if err != nil {
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
	_, err := repository.pool.Exec(ctx, `UPDATE identity_password_credentials SET password_hash=$4,updated_at=$5 WHERE store_id=$1::uuid AND user_id=$2::uuid AND password_hash=$3`, storeID.String(), userID.String(), currentHash, replacementHash, now)
	return err
}

func (repository *Repository) IsActiveUser(ctx context.Context, storeID ids.StoreID, userID ids.UserID) (bool, error) {
	var active bool
	err := repository.pool.QueryRow(ctx, `SELECT status='active' FROM users WHERE store_id=$1::uuid AND user_id=$2::uuid`, storeID.String(), userID.String()).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return active, err
}

func (repository *Repository) CreateUserSession(ctx context.Context, session application.SessionRecord) (bool, error) {
	created := false
	err := pgx.BeginFunc(ctx, repository.pool, func(tx pgx.Tx) error {
		var status identitydomain.UserStatus
		err := tx.QueryRow(ctx, `SELECT status FROM users WHERE store_id=$1::uuid AND user_id=$2::uuid FOR UPDATE`, session.StoreID.String(), session.UserID.String()).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if status != identitydomain.UserStatusActive {
			return nil
		}
		if err := insertUserSession(ctx, tx, session); err != nil {
			return err
		}
		created = true
		return nil
	})
	return created, err
}

func insertUserSession(ctx context.Context, tx pgx.Tx, session application.SessionRecord) error {
	if _, err := tx.Exec(ctx, `INSERT INTO identity_sessions (store_id,session_id,user_id,expires_at,created_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5)`, session.StoreID.String(), session.SessionID, session.UserID.String(), session.ExpiresAt, session.CreatedAt); err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO identity_access_tokens (store_id,token_digest,session_id,expires_at,created_at) VALUES ($1::uuid,$2,$3::uuid,$4,$5)`, session.StoreID.String(), session.Access.Digest[:], session.SessionID, session.Access.ExpiresAt, session.Access.CreatedAt); err != nil {
		return fmt.Errorf("insert access token: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO identity_refresh_tokens (store_id,token_digest,session_id,expires_at,created_at) VALUES ($1::uuid,$2,$3::uuid,$4,$5)`, session.StoreID.String(), session.Refresh.Digest[:], session.SessionID, session.Refresh.ExpiresAt, session.Refresh.CreatedAt); err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}

func (repository *Repository) AuthenticateUser(ctx context.Context, storeID ids.StoreID, digest identitydomain.TokenDigest, now time.Time) (application.UserRecord, string, bool, error) {
	row := repository.pool.QueryRow(ctx, `SELECT u.user_id::text,u.email,u.canonical_email,u.status,u.created_at,u.updated_at,s.session_id::text FROM identity_access_tokens a JOIN identity_sessions s ON s.store_id=a.store_id AND s.session_id=a.session_id JOIN users u ON u.store_id=s.store_id AND u.user_id=s.user_id WHERE a.store_id=$1::uuid AND a.token_digest=$2 AND a.expires_at>$3 AND s.expires_at>$3 AND s.revoked_at IS NULL AND s.compromised_at IS NULL`, storeID.String(), digest[:], now)
	var idText, sessionID string
	var presented, canonical *string
	var user application.UserRecord
	if err := row.Scan(&idText, &presented, &canonical, &user.Status, &user.CreatedAt, &user.UpdatedAt, &sessionID); errors.Is(err, pgx.ErrNoRows) {
		return application.UserRecord{}, "", false, nil
	} else if err != nil {
		return application.UserRecord{}, "", false, fmt.Errorf("query access token: %w", err)
	}
	userID, err := ids.ParseUserID(idText)
	if err != nil {
		return application.UserRecord{}, "", false, err
	}
	user.StoreID, user.ID = storeID, userID
	if presented != nil && canonical != nil {
		email, err := identitydomain.ParseEmail(*presented)
		if err != nil || email.Canonical() != *canonical {
			return application.UserRecord{}, "", false, errors.New("invalid persisted email")
		}
		user.Email = &email
	}
	return user, sessionID, true, nil
}

func (repository *Repository) RotateUser(ctx context.Context, storeID ids.StoreID, old identitydomain.TokenDigest, access, refresh application.TokenRecord, now time.Time) (string, time.Time, bool, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return "", time.Time{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var sessionID string
	var tokenExpires, sessionExpires time.Time
	var userStatus identitydomain.UserStatus
	var usedAt, tokenRevoked, sessionRevoked, compromised *time.Time
	err = tx.QueryRow(ctx, `SELECT r.session_id::text,r.expires_at,r.used_at,r.revoked_at,s.expires_at,s.revoked_at,s.compromised_at,u.status FROM identity_refresh_tokens r JOIN identity_sessions s ON s.store_id=r.store_id AND s.session_id=r.session_id JOIN users u ON u.store_id=s.store_id AND u.user_id=s.user_id WHERE r.store_id=$1::uuid AND r.token_digest=$2 FOR UPDATE OF r,s,u`, storeID.String(), old[:]).Scan(&sessionID, &tokenExpires, &usedAt, &tokenRevoked, &sessionExpires, &sessionRevoked, &compromised, &userStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}
	if userStatus != identitydomain.UserStatusActive {
		if _, err := tx.Exec(ctx, `UPDATE identity_sessions SET revoked_at=COALESCE(revoked_at,$3) WHERE store_id=$1::uuid AND session_id=$2::uuid`, storeID.String(), sessionID, now); err != nil {
			return "", time.Time{}, false, err
		}
		if _, err := tx.Exec(ctx, `UPDATE identity_refresh_tokens SET revoked_at=COALESCE(revoked_at,$3) WHERE store_id=$1::uuid AND session_id=$2::uuid`, storeID.String(), sessionID, now); err != nil {
			return "", time.Time{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", time.Time{}, false, err
		}
		return "", time.Time{}, false, nil
	}
	if usedAt != nil || tokenRevoked != nil {
		if _, err := tx.Exec(ctx, `UPDATE identity_sessions SET compromised_at=$3,revoked_at=$3 WHERE store_id=$1::uuid AND session_id=$2::uuid AND compromised_at IS NULL`, storeID.String(), sessionID, now); err != nil {
			return "", time.Time{}, false, err
		}
		if _, err := tx.Exec(ctx, `UPDATE identity_refresh_tokens SET revoked_at=COALESCE(revoked_at,$3) WHERE store_id=$1::uuid AND session_id=$2::uuid`, storeID.String(), sessionID, now); err != nil {
			return "", time.Time{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", time.Time{}, false, err
		}
		return sessionID, sessionExpires, true, nil
	}
	if !tokenExpires.After(now) || !sessionExpires.After(now) || sessionRevoked != nil || compromised != nil {
		return "", time.Time{}, false, nil
	}
	if access.ExpiresAt.After(sessionExpires) {
		access.ExpiresAt = sessionExpires
	}
	refresh.ExpiresAt = sessionExpires
	if _, err := tx.Exec(ctx, `UPDATE identity_refresh_tokens SET used_at=$3 WHERE store_id=$1::uuid AND token_digest=$2`, storeID.String(), old[:], now); err != nil {
		return "", time.Time{}, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO identity_access_tokens (store_id,token_digest,session_id,expires_at,created_at) VALUES ($1::uuid,$2,$3::uuid,$4,$5)`, storeID.String(), access.Digest[:], sessionID, access.ExpiresAt, access.CreatedAt); err != nil {
		return "", time.Time{}, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO identity_refresh_tokens (store_id,token_digest,session_id,expires_at,created_at) VALUES ($1::uuid,$2,$3::uuid,$4,$5)`, storeID.String(), refresh.Digest[:], sessionID, refresh.ExpiresAt, refresh.CreatedAt); err != nil {
		return "", time.Time{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", time.Time{}, false, err
	}
	return sessionID, sessionExpires, false, nil
}

func (repository *Repository) RevokeUserSession(ctx context.Context, storeID ids.StoreID, digest identitydomain.TokenDigest, now time.Time) (bool, error) {
	result, err := repository.pool.Exec(ctx, `UPDATE identity_sessions s SET revoked_at=COALESCE(s.revoked_at,$3) FROM identity_access_tokens a WHERE s.store_id=$1::uuid AND a.store_id=s.store_id AND a.session_id=s.session_id AND a.token_digest=$2`, storeID.String(), digest[:], now)
	return result.RowsAffected() == 1, err
}

func (repository *Repository) CreateGuest(ctx context.Context, guest application.GuestRecord, session application.SessionRecord) error {
	return pgx.BeginFunc(ctx, repository.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO identity_guests (store_id,guest_id,created_at,expires_at) VALUES ($1::uuid,$2::uuid,$3,$4)`, guest.StoreID.String(), guest.GuestID.String(), guest.CreatedAt, guest.ExpiresAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO identity_guest_sessions (store_id,session_id,guest_id,expires_at,created_at) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5)`, session.StoreID.String(), session.SessionID, session.GuestID.String(), session.ExpiresAt, session.CreatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO identity_guest_access_tokens (store_id,token_digest,session_id,expires_at,created_at) VALUES ($1::uuid,$2,$3::uuid,$4,$5)`, session.StoreID.String(), session.Access.Digest[:], session.SessionID, session.Access.ExpiresAt, session.Access.CreatedAt); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO identity_guest_refresh_tokens (store_id,token_digest,session_id,expires_at,created_at) VALUES ($1::uuid,$2,$3::uuid,$4,$5)`, session.StoreID.String(), session.Refresh.Digest[:], session.SessionID, session.Refresh.ExpiresAt, session.Refresh.CreatedAt)
		return err
	})
}

func (repository *Repository) AuthenticateGuest(ctx context.Context, storeID ids.StoreID, digest identitydomain.TokenDigest, now time.Time) (ids.GuestID, string, time.Time, time.Time, bool, error) {
	var guestText, sessionID string
	var accessExpires, sessionExpires time.Time
	err := repository.pool.QueryRow(ctx, `SELECT s.guest_id::text,s.session_id::text,a.expires_at,s.expires_at FROM identity_guest_access_tokens a JOIN identity_guest_sessions s ON s.store_id=a.store_id AND s.session_id=a.session_id WHERE a.store_id=$1::uuid AND a.token_digest=$2 AND a.expires_at>$3 AND s.expires_at>$3 AND s.revoked_at IS NULL AND s.compromised_at IS NULL`, storeID.String(), digest[:], now).Scan(&guestText, &sessionID, &accessExpires, &sessionExpires)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", time.Time{}, time.Time{}, false, nil
	}
	if err != nil {
		return "", "", time.Time{}, time.Time{}, false, err
	}
	guestID, err := ids.ParseGuestID(guestText)
	return guestID, sessionID, accessExpires, sessionExpires, err == nil, err
}

func (repository *Repository) RotateGuest(ctx context.Context, storeID ids.StoreID, old identitydomain.TokenDigest, access, refresh application.TokenRecord, now time.Time) (string, time.Time, bool, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return "", time.Time{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var sessionID string
	var tokenExpires, sessionExpires time.Time
	var usedAt, tokenRevoked, sessionRevoked, compromised *time.Time
	err = tx.QueryRow(ctx, `SELECT r.session_id::text,r.expires_at,r.used_at,r.revoked_at,s.expires_at,s.revoked_at,s.compromised_at FROM identity_guest_refresh_tokens r JOIN identity_guest_sessions s ON s.store_id=r.store_id AND s.session_id=r.session_id WHERE r.store_id=$1::uuid AND r.token_digest=$2 FOR UPDATE OF r,s`, storeID.String(), old[:]).Scan(&sessionID, &tokenExpires, &usedAt, &tokenRevoked, &sessionExpires, &sessionRevoked, &compromised)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}
	if usedAt != nil || tokenRevoked != nil {
		if _, err := tx.Exec(ctx, `UPDATE identity_guest_sessions SET compromised_at=$3,revoked_at=$3 WHERE store_id=$1::uuid AND session_id=$2::uuid AND compromised_at IS NULL`, storeID.String(), sessionID, now); err != nil {
			return "", time.Time{}, false, err
		}
		if _, err := tx.Exec(ctx, `UPDATE identity_guest_refresh_tokens SET revoked_at=COALESCE(revoked_at,$3) WHERE store_id=$1::uuid AND session_id=$2::uuid`, storeID.String(), sessionID, now); err != nil {
			return "", time.Time{}, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return "", time.Time{}, false, err
		}
		return sessionID, sessionExpires, true, nil
	}
	if !tokenExpires.After(now) || !sessionExpires.After(now) || sessionRevoked != nil || compromised != nil {
		return "", time.Time{}, false, nil
	}
	if access.ExpiresAt.After(sessionExpires) {
		access.ExpiresAt = sessionExpires
	}
	refresh.ExpiresAt = sessionExpires
	if _, err := tx.Exec(ctx, `UPDATE identity_guest_refresh_tokens SET used_at=$3 WHERE store_id=$1::uuid AND token_digest=$2`, storeID.String(), old[:], now); err != nil {
		return "", time.Time{}, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO identity_guest_access_tokens (store_id,token_digest,session_id,expires_at,created_at) VALUES ($1::uuid,$2,$3::uuid,$4,$5)`, storeID.String(), access.Digest[:], sessionID, access.ExpiresAt, access.CreatedAt); err != nil {
		return "", time.Time{}, false, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO identity_guest_refresh_tokens (store_id,token_digest,session_id,expires_at,created_at) VALUES ($1::uuid,$2,$3::uuid,$4,$5)`, storeID.String(), refresh.Digest[:], sessionID, refresh.ExpiresAt, refresh.CreatedAt); err != nil {
		return "", time.Time{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", time.Time{}, false, err
	}
	return sessionID, sessionExpires, false, nil
}

func (repository *Repository) RevokeGuestSession(ctx context.Context, storeID ids.StoreID, digest identitydomain.TokenDigest, now time.Time) (bool, error) {
	result, err := repository.pool.Exec(ctx, `UPDATE identity_guest_sessions s SET revoked_at=COALESCE(s.revoked_at,$3) FROM identity_guest_access_tokens a WHERE s.store_id=$1::uuid AND a.store_id=s.store_id AND a.session_id=s.session_id AND a.token_digest=$2`, storeID.String(), digest[:], now)
	return result.RowsAffected() == 1, err
}

func (repository *Repository) ResolveSocial(ctx context.Context, storeID ids.StoreID, provider identitydomain.Provider, subject string, email *identitydomain.Email, newUserID ids.UserID, now time.Time) (application.UserRecord, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return application.UserRecord{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	lockKey := storeID.String() + "|" + string(provider) + "|" + subject
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, lockKey); err != nil {
		return application.UserRecord{}, err
	}
	user, found, err := socialUser(ctx, tx, storeID, provider, subject)
	if err != nil {
		return application.UserRecord{}, err
	}
	if found {
		if err := tx.Commit(ctx); err != nil {
			return application.UserRecord{}, err
		}
		return user, nil
	}
	var presented, canonical any
	if email != nil {
		presented, canonical = email.Presented(), email.Canonical()
	}
	_, err = tx.Exec(ctx, `INSERT INTO users (store_id,user_id,email,canonical_email,status,created_at,updated_at) VALUES ($1::uuid,$2::uuid,$3,$4,'active',$5,$5)`, storeID.String(), newUserID.String(), presented, canonical, now)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return application.UserRecord{}, application.ErrIdentityExists
	}
	if err != nil {
		return application.UserRecord{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO identity_social_identities (store_id,provider,provider_subject,user_id,created_at) VALUES ($1::uuid,$2,$3,$4::uuid,$5)`, storeID.String(), provider, subject, newUserID.String(), now); err != nil {
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
			return application.UserRecord{}, err
		}
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			return application.UserRecord{}, fmt.Errorf("rollback social identity race: %w", rollbackErr)
		}
		winner, found, lookupErr := socialUser(ctx, repository.pool, storeID, provider, subject)
		if lookupErr != nil {
			return application.UserRecord{}, lookupErr
		}
		if !found {
			return application.UserRecord{}, application.ErrIdentityExists
		}
		return winner, nil
	}
	if err := tx.Commit(ctx); err != nil {
		return application.UserRecord{}, err
	}
	return application.UserRecord{StoreID: storeID, ID: newUserID, Email: email, Status: identitydomain.UserStatusActive, CreatedAt: now, UpdatedAt: now}, nil
}

func (repository *Repository) LinkSocial(ctx context.Context, storeID ids.StoreID, userID ids.UserID, sessionID string, provider identitydomain.Provider, subject string, now time.Time) error {
	result, err := repository.pool.Exec(ctx, `INSERT INTO identity_social_identities (store_id,provider,provider_subject,user_id,created_at) SELECT $1::uuid,$2,$3,u.user_id,$6 FROM users u JOIN identity_sessions s ON s.store_id=u.store_id AND s.user_id=u.user_id WHERE u.store_id=$1::uuid AND u.user_id=$4::uuid AND s.session_id=$5::uuid AND u.status='active' AND s.expires_at>$6 AND s.revoked_at IS NULL AND s.compromised_at IS NULL`, storeID.String(), provider, subject, userID.String(), sessionID, now)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return application.ErrIdentityExists
	}
	if err != nil {
		return err
	}
	if result.RowsAffected() != 1 {
		return application.ErrUnauthenticated
	}
	return nil
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func socialUser(ctx context.Context, query queryRower, storeID ids.StoreID, provider identitydomain.Provider, subject string) (application.UserRecord, bool, error) {
	var idText string
	var presented, canonical *string
	var user application.UserRecord
	err := query.QueryRow(ctx, `SELECT u.user_id::text,u.email,u.canonical_email,u.status,u.created_at,u.updated_at FROM identity_social_identities i JOIN users u ON u.store_id=i.store_id AND u.user_id=i.user_id WHERE i.store_id=$1::uuid AND i.provider=$2 AND i.provider_subject=$3`, storeID.String(), provider, subject).Scan(&idText, &presented, &canonical, &user.Status, &user.CreatedAt, &user.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
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
		email, err := identitydomain.ParseEmail(*presented)
		if err != nil || email.Canonical() != *canonical {
			return application.UserRecord{}, false, errors.New("invalid persisted email")
		}
		user.Email = &email
	}
	return user, true, nil
}

var _ application.Repository = (*Repository)(nil)
