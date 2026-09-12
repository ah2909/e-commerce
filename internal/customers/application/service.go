package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ah2909/e-commerce/internal/customers/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

var (
	ErrNotFound = errors.New("customer resource not found")
	ErrConflict = errors.New("customer conflict")
)

type Cursor struct {
	CreatedAt time.Time     `json:"created_at"`
	AddressID ids.AddressID `json:"address_id"`
}

type AddressPage struct {
	Addresses  []domain.Address `json:"addresses"`
	NextCursor string           `json:"next_cursor,omitempty"`
}

type Repository interface {
	GetProfile(context.Context, ids.StoreID, ids.UserID) (domain.Profile, error)
	PutProfile(context.Context, ids.StoreID, ids.UserID, ids.CustomerID, int64, domain.ProfileDraft) (domain.Profile, error)
	CreateAddress(context.Context, ids.StoreID, ids.UserID, ids.AddressID, domain.AddressDraft) (domain.Address, error)
	GetAddress(context.Context, ids.StoreID, ids.UserID, ids.AddressID) (domain.Address, error)
	ListAddresses(context.Context, ids.StoreID, ids.UserID, int, *Cursor) ([]domain.Address, error)
	ReplaceAddress(context.Context, ids.StoreID, ids.UserID, ids.AddressID, int64, domain.AddressDraft) (domain.Address, error)
	ArchiveAddress(context.Context, ids.StoreID, ids.UserID, ids.AddressID, int64) error
}

type Service struct {
	repository  Repository
	newCustomer func() (ids.CustomerID, error)
	newAddress  func() (ids.AddressID, error)
}

func NewService(repository Repository) Service {
	return Service{repository: repository, newCustomer: ids.NewCustomerID, newAddress: ids.NewAddressID}
}

func (service Service) GetProfile(ctx context.Context, storeID ids.StoreID, userID ids.UserID) (domain.Profile, error) {
	if storeID == "" || userID == "" {
		return domain.Profile{}, domain.ErrInvalidCustomer
	}
	return service.repository.GetProfile(ctx, storeID, userID)
}

func (service Service) PutProfile(ctx context.Context, storeID ids.StoreID, userID ids.UserID, expected int64, draft domain.ProfileDraft) (domain.Profile, error) {
	if storeID == "" || userID == "" || expected < 0 {
		return domain.Profile{}, domain.ErrInvalidCustomer
	}
	validated, _, _, err := domain.ValidateProfileDraft(draft)
	if err != nil {
		return domain.Profile{}, err
	}
	id, err := service.newCustomer()
	if err != nil {
		return domain.Profile{}, fmt.Errorf("generate customer ID: %w", err)
	}
	return service.repository.PutProfile(ctx, storeID, userID, id, expected, validated)
}

func (service Service) CreateAddress(ctx context.Context, storeID ids.StoreID, userID ids.UserID, draft domain.AddressDraft) (domain.Address, error) {
	validated, _, err := domain.ValidateAddressDraft(draft)
	if storeID == "" || userID == "" || err != nil {
		return domain.Address{}, domain.ErrInvalidCustomer
	}
	id, err := service.newAddress()
	if err != nil {
		return domain.Address{}, fmt.Errorf("generate address ID: %w", err)
	}
	return service.repository.CreateAddress(ctx, storeID, userID, id, validated)
}

func (service Service) GetAddress(ctx context.Context, storeID ids.StoreID, userID ids.UserID, addressID ids.AddressID) (domain.Address, error) {
	if storeID == "" || userID == "" || addressID == "" {
		return domain.Address{}, domain.ErrInvalidCustomer
	}
	return service.repository.GetAddress(ctx, storeID, userID, addressID)
}

func (service Service) ListAddresses(ctx context.Context, storeID ids.StoreID, userID ids.UserID, limit int, encoded string) (AddressPage, error) {
	if limit == 0 {
		limit = 25
	}
	if storeID == "" || userID == "" || limit < 1 || limit > 100 {
		return AddressPage{}, domain.ErrInvalidCustomer
	}
	cursor, err := decodeCursor(encoded)
	if err != nil {
		return AddressPage{}, domain.ErrInvalidCustomer
	}
	values, err := service.repository.ListAddresses(ctx, storeID, userID, limit+1, cursor)
	if err != nil {
		return AddressPage{}, err
	}
	page := AddressPage{Addresses: values}
	if len(values) > limit {
		page.Addresses = values[:limit]
		last := page.Addresses[len(page.Addresses)-1]
		page.NextCursor, err = encodeCursor(Cursor{CreatedAt: last.CreatedAt, AddressID: last.ID})
	}
	return page, err
}

func (service Service) ReplaceAddress(ctx context.Context, storeID ids.StoreID, userID ids.UserID, addressID ids.AddressID, expected int64, draft domain.AddressDraft) (domain.Address, error) {
	validated, _, err := domain.ValidateAddressDraft(draft)
	if storeID == "" || userID == "" || addressID == "" || expected < 1 || err != nil {
		return domain.Address{}, domain.ErrInvalidCustomer
	}
	return service.repository.ReplaceAddress(ctx, storeID, userID, addressID, expected, validated)
}

func (service Service) ArchiveAddress(ctx context.Context, storeID ids.StoreID, userID ids.UserID, addressID ids.AddressID, expected int64) error {
	if storeID == "" || userID == "" || addressID == "" || expected < 1 {
		return domain.ErrInvalidCustomer
	}
	return service.repository.ArchiveAddress(ctx, storeID, userID, addressID, expected)
}

func encodeCursor(cursor Cursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeCursor(value string) (*Cursor, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > 512 {
		return nil, domain.ErrInvalidCustomer
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	var cursor Cursor
	if json.Unmarshal(raw, &cursor) != nil || cursor.CreatedAt.IsZero() || cursor.AddressID == "" {
		return nil, domain.ErrInvalidCustomer
	}
	if _, err := ids.ParseAddressID(cursor.AddressID.String()); err != nil {
		return nil, err
	}
	return &cursor, nil
}
