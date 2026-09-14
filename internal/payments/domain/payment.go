package domain

import (
	"errors"
	"strings"
	"time"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shared/money"
)

var ErrInvalidPayment = errors.New("invalid payment")

const ProviderVietQR = "vietqr"

type Payment struct {
	StoreID  ids.StoreID
	ID       ids.PaymentID
	OrderID  ids.OrderID
	Provider string
	Amount   money.Money
	State    string
	Version  int64
}

type VietQRConfigDraft struct {
	BankBIN       string
	AccountNumber string
	AccountName   string
	Enabled       bool
}

type VietQRConfig struct {
	StoreID       ids.StoreID `json:"-"`
	BankBIN       string      `json:"bank_bin"`
	MaskedAccount string      `json:"masked_account_number"`
	AccountName   string      `json:"account_name"`
	Enabled       bool        `json:"enabled"`
	Version       int64       `json:"version"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

type Instruction struct {
	AttemptID         ids.PaymentAttemptID `json:"attempt_id"`
	PaymentID         ids.PaymentID        `json:"payment_id"`
	OrderID           ids.OrderID          `json:"order_id"`
	TransferReference string               `json:"transfer_reference"`
	Amount            money.Money          `json:"amount"`
	ExpiresAt         time.Time            `json:"expires_at"`
	Payload           string               `json:"qr_payload"`
	PNGBase64         string               `json:"qr_png_base64"`
	Created           bool                 `json:"created"`
}

type Confirmation struct {
	PaymentID        ids.PaymentID        `json:"payment_id"`
	AttemptID        ids.PaymentAttemptID `json:"attempt_id,omitempty"`
	Outcome          string               `json:"outcome"`
	ReconciliationID ids.ReconciliationID `json:"reconciliation_id,omitempty"`
	Created          bool                 `json:"created"`
}

type Reconciliation struct {
	ID                  ids.ReconciliationID `json:"reconciliation_id"`
	PaymentID           ids.PaymentID        `json:"payment_id"`
	AttemptID           ids.PaymentAttemptID `json:"attempt_id,omitempty"`
	TransferReference   string               `json:"transfer_reference"`
	ObservedAmountMinor int64                `json:"observed_amount_minor"`
	ExpectedAmountMinor int64                `json:"expected_amount_minor"`
	Currency            string               `json:"currency"`
	Reason              string               `json:"reason"`
	ReceivedAt          time.Time            `json:"received_at"`
	CreatedAt           time.Time            `json:"created_at"`
}

func ValidateVietQRConfig(draft VietQRConfigDraft) (VietQRConfigDraft, error) {
	draft.BankBIN = strings.TrimSpace(draft.BankBIN)
	draft.AccountNumber = strings.TrimSpace(draft.AccountNumber)
	draft.AccountName = strings.Join(strings.Fields(strings.ToUpper(draft.AccountName)), " ")
	if len(draft.BankBIN) != 6 || !digits(draft.BankBIN) || len(draft.AccountNumber) < 4 || len(draft.AccountNumber) > 19 || !digits(draft.AccountNumber) || len(draft.AccountName) < 1 || len(draft.AccountName) > 50 || !asciiName(draft.AccountName) {
		return VietQRConfigDraft{}, ErrInvalidPayment
	}
	return draft, nil
}

func MaskAccount(value string) string {
	if len(value) < 4 {
		return ""
	}
	return strings.Repeat("*", len(value)-4) + value[len(value)-4:]
}

func ValidIdempotencyKey(value string) bool {
	return value == strings.TrimSpace(value) && len(value) >= 1 && len(value) <= 128
}

func ValidTransferReference(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 1 && len(value) <= 64
}

func digits(value string) bool {
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func asciiName(value string) bool {
	for _, character := range value {
		if (character < 'A' || character > 'Z') && character != ' ' && character != '.' && character != '-' {
			return false
		}
	}
	return true
}
