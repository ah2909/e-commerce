package application

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/ah2909/e-commerce/internal/pricing/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shared/money"
)

var (
	ErrNotFound = errors.New("pricing resource not found")
	ErrConflict = errors.New("pricing conflict")
)

const MaxPageSize = 100

type Cursor struct {
	UpdatedAt   time.Time       `json:"updated_at"`
	PriceListID ids.PriceListID `json:"price_list_id"`
}

type Page struct {
	PriceLists []domain.PriceList
	NextCursor string
}

type Repository interface {
	Create(context.Context, ids.StoreID, domain.PriceList) (domain.PriceList, error)
	Replace(context.Context, ids.StoreID, ids.PriceListID, int64, domain.PriceList) (domain.PriceList, error)
	Get(context.Context, ids.StoreID, ids.PriceListID) (domain.PriceList, error)
	List(context.Context, ids.StoreID, int, *Cursor) ([]domain.PriceList, error)
	Archive(context.Context, ids.StoreID, ids.PriceListID, int64, time.Time) (domain.PriceList, error)
	ResolveCurrent(context.Context, ids.StoreID, []ids.VariantID) (map[ids.VariantID]money.Money, error)
}

type Service struct {
	repository Repository
	newID      func() (ids.PriceListID, error)
	now        func() time.Time
}

func NewService(repository Repository, newID func() (ids.PriceListID, error), now func() time.Time) Service {
	return Service{repository: repository, newID: newID, now: now}
}

func (service Service) Create(ctx context.Context, storeID ids.StoreID, draft domain.PriceListDraft) (domain.PriceList, error) {
	id, err := service.newID()
	if err != nil {
		return domain.PriceList{}, fmt.Errorf("generate price list ID: %w", err)
	}
	list, err := domain.NewPriceList(storeID, id, draft, service.now())
	if err != nil {
		return domain.PriceList{}, err
	}
	created, err := service.repository.Create(ctx, storeID, list)
	if err != nil {
		return domain.PriceList{}, fmt.Errorf("create price list: %w", err)
	}
	return created, nil
}

func (service Service) Replace(ctx context.Context, storeID ids.StoreID, id ids.PriceListID, expectedVersion int64, draft domain.PriceListDraft) (domain.PriceList, error) {
	if expectedVersion < 1 {
		return domain.PriceList{}, domain.ErrInvalidPricing
	}
	current, err := service.repository.Get(ctx, storeID, id)
	if err != nil {
		return domain.PriceList{}, fmt.Errorf("load price list: %w", err)
	}
	if current.Version != expectedVersion {
		return domain.PriceList{}, ErrConflict
	}
	if current.Status != domain.StatusActive {
		return domain.PriceList{}, ErrConflict
	}
	replacement, err := current.Replaced(draft, service.now())
	if err != nil {
		return domain.PriceList{}, err
	}
	updated, err := service.repository.Replace(ctx, storeID, id, expectedVersion, replacement)
	if err != nil {
		return domain.PriceList{}, fmt.Errorf("replace price list: %w", err)
	}
	return updated, nil
}

func (service Service) Get(ctx context.Context, storeID ids.StoreID, id ids.PriceListID) (domain.PriceList, error) {
	return service.repository.Get(ctx, storeID, id)
}

func (service Service) Archive(ctx context.Context, storeID ids.StoreID, id ids.PriceListID, expectedVersion int64) (domain.PriceList, error) {
	if expectedVersion < 1 {
		return domain.PriceList{}, domain.ErrInvalidPricing
	}
	value, err := service.repository.Archive(ctx, storeID, id, expectedVersion, service.now().UTC())
	if err != nil {
		return domain.PriceList{}, fmt.Errorf("archive price list: %w", err)
	}
	return value, nil
}

func (service Service) List(ctx context.Context, storeID ids.StoreID, limit int, encoded string) (Page, error) {
	if limit == 0 {
		limit = 25
	}
	if limit < 1 || limit > MaxPageSize {
		return Page{}, domain.ErrInvalidPricing
	}
	cursor, err := DecodeCursor(encoded)
	if err != nil {
		return Page{}, domain.ErrInvalidPricing
	}
	values, err := service.repository.List(ctx, storeID, limit+1, cursor)
	if err != nil {
		return Page{}, fmt.Errorf("list price lists: %w", err)
	}
	page := Page{PriceLists: values}
	if len(values) > limit {
		page.PriceLists = values[:limit]
		last := page.PriceLists[len(page.PriceLists)-1]
		page.NextCursor, err = EncodeCursor(Cursor{UpdatedAt: last.UpdatedAt, PriceListID: last.ID})
	}
	return page, err
}

func (service Service) ResolveCurrent(ctx context.Context, storeID ids.StoreID, variants []ids.VariantID) (map[ids.VariantID]money.Money, error) {
	return service.repository.ResolveCurrent(ctx, storeID, variants)
}

func EncodeCursor(cursor Cursor) (string, error) {
	if cursor.UpdatedAt.IsZero() || cursor.PriceListID == "" {
		return "", domain.ErrInvalidPricing
	}
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func DecodeCursor(encoded string) (*Cursor, error) {
	if encoded == "" {
		return nil, nil
	}
	if len(encoded) > 512 {
		return nil, domain.ErrInvalidPricing
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, domain.ErrInvalidPricing
	}
	var cursor Cursor
	if err := json.Unmarshal(raw, &cursor); err != nil || cursor.UpdatedAt.IsZero() || cursor.PriceListID == "" {
		return nil, domain.ErrInvalidPricing
	}
	if _, err := ids.ParsePriceListID(cursor.PriceListID.String()); err != nil {
		return nil, domain.ErrInvalidPricing
	}
	return &cursor, nil
}
