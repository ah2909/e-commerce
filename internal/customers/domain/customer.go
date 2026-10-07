package domain

import (
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/ah2909/e-commerce/internal/shared/ids"
)

var ErrInvalidCustomer = errors.New("invalid customer")

type ProfileDraft struct {
	FullName string `json:"full_name"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
}

type Profile struct {
	StoreID        ids.StoreID    `json:"-"`
	ID             ids.CustomerID `json:"customer_id"`
	UserID         ids.UserID     `json:"-"`
	FullName       string         `json:"full_name"`
	Email          string         `json:"email"`
	Phone          string         `json:"phone"`
	CanonicalMail  string         `json:"-"`
	CanonicalPhone string         `json:"-"`
	Version        int64          `json:"version"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	ArchivedAt     *time.Time     `json:"-"`
}

type AddressDraft struct {
	Label         string `json:"label"`
	RecipientName string `json:"recipient_name"`
	Phone         string `json:"phone"`
	Line1         string `json:"line1"`
	Line2         string `json:"line2"`
	Ward          string `json:"ward"`
	District      string `json:"district"`
	Province      string `json:"province"`
	PostalCode    string `json:"postal_code"`
	CountryCode   string `json:"country_code"`
}

type Address struct {
	StoreID    ids.StoreID    `json:"-"`
	ID         ids.AddressID  `json:"address_id"`
	CustomerID ids.CustomerID `json:"-"`
	AddressDraft
	CanonicalPhone string     `json:"-"`
	Version        int64      `json:"version"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	ArchivedAt     *time.Time `json:"-"`
}

func NewProfile(storeID ids.StoreID, id ids.CustomerID, userID ids.UserID, draft ProfileDraft, now time.Time) (Profile, error) {
	draft, mail, phone, err := ValidateProfileDraft(draft)
	if err != nil || storeID == "" || id == "" || userID == "" || now.IsZero() {
		return Profile{}, ErrInvalidCustomer
	}
	now = now.UTC()
	return Profile{StoreID: storeID, ID: id, UserID: userID, FullName: draft.FullName, Email: draft.Email, Phone: draft.Phone, CanonicalMail: mail, CanonicalPhone: phone, Version: 1, CreatedAt: now, UpdatedAt: now}, nil
}

func ValidateProfileDraft(draft ProfileDraft) (ProfileDraft, string, string, error) {
	draft.FullName = strings.TrimSpace(draft.FullName)
	draft.Email = strings.TrimSpace(draft.Email)
	draft.Phone = strings.TrimSpace(draft.Phone)
	canonicalMail := strings.ToLower(draft.Email)
	parsedMail, mailErr := mail.ParseAddress(draft.Email)
	phone, ok := normalizePhone(draft.Phone)
	if len(draft.FullName) < 1 || len(draft.FullName) > 160 || len(draft.Email) < 3 || len(draft.Email) > 254 || len(draft.Phone) < 8 || len(draft.Phone) > 20 || mailErr != nil || parsedMail.Name != "" || parsedMail.Address != draft.Email || !ok {
		return ProfileDraft{}, "", "", ErrInvalidCustomer
	}
	return draft, canonicalMail, phone, nil
}

func ValidateAddressDraft(draft AddressDraft) (AddressDraft, string, error) {
	draft.Label = strings.TrimSpace(draft.Label)
	draft.RecipientName = strings.TrimSpace(draft.RecipientName)
	draft.Phone = strings.TrimSpace(draft.Phone)
	draft.Line1 = strings.TrimSpace(draft.Line1)
	draft.Line2 = strings.TrimSpace(draft.Line2)
	draft.Ward = strings.TrimSpace(draft.Ward)
	draft.District = strings.TrimSpace(draft.District)
	draft.Province = strings.TrimSpace(draft.Province)
	draft.PostalCode = strings.TrimSpace(draft.PostalCode)
	draft.CountryCode = strings.ToUpper(strings.TrimSpace(draft.CountryCode))
	phone, ok := normalizePhone(draft.Phone)
	valid := bounded(draft.Label, 1, 80) && bounded(draft.RecipientName, 1, 160) && bounded(draft.Line1, 1, 240) && bounded(draft.Line2, 0, 240) && bounded(draft.Ward, 1, 120) && bounded(draft.District, 0, 120) && bounded(draft.Province, 1, 120) && bounded(draft.PostalCode, 0, 20)
	if !valid || len(draft.Phone) < 8 || len(draft.Phone) > 20 || !ok || draft.CountryCode != "VN" {
		return AddressDraft{}, "", ErrInvalidCustomer
	}
	return draft, phone, nil
}

func normalizePhone(value string) (string, bool) {
	compact := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(value)
	if strings.HasPrefix(compact, "0") {
		compact = "+84" + strings.TrimPrefix(compact, "0")
	}
	if !strings.HasPrefix(compact, "+") || len(compact) < 9 || len(compact) > 16 {
		return "", false
	}
	if compact[1] < '1' || compact[1] > '9' {
		return "", false
	}
	for _, char := range compact[1:] {
		if char < '0' || char > '9' {
			return "", false
		}
	}
	return compact, true
}

func bounded(value string, minimum, maximum int) bool {
	return len(value) >= minimum && len(value) <= maximum
}
