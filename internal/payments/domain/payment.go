package domain

import (
	"crypto/subtle"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/ah2909/e-commerce/internal/shared/money"
)

var ErrInvalidPayment = errors.New("invalid payment")

const (
	ProviderVietQR = "vietqr"
	ProviderMoMo   = "momo"
)

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

type MoMoConfigDraft struct {
	PartnerCode string
	AccessKey   string
	SecretKey   string
	Enabled     bool
}

type MoMoConfig struct {
	StoreID       ids.StoreID `json:"-"`
	MaskedPartner string      `json:"masked_partner_code"`
	Enabled       bool        `json:"enabled"`
	Version       int64       `json:"version"`
	CreatedAt     time.Time   `json:"created_at"`
	UpdatedAt     time.Time   `json:"updated_at"`
}

type MoMoAttempt struct {
	AttemptID ids.PaymentAttemptID `json:"attempt_id"`
	PaymentID ids.PaymentID        `json:"payment_id"`
	OrderID   ids.OrderID          `json:"order_id"`
	State     string               `json:"state"`
	PayURL    string               `json:"pay_url,omitempty"`
	Deeplink  string               `json:"deeplink,omitempty"`
	QRCodeURL string               `json:"qr_code_url,omitempty"`
	ExpiresAt time.Time            `json:"expires_at"`
	Created   bool                 `json:"created"`
}

type MoMoIPN struct {
	PartnerCode string
	OrderID     string
	RequestID   string
	AmountMinor int64
	OrderInfo   string
	OrderType   string
	Transaction int64
	ResultCode  int
	Message     string
	PayType     string
	ResponseMS  int64
	ExtraData   string
}

type WebhookResult struct {
	Outcome string
	Created bool
}

type Refund struct {
	StoreID            ids.StoreID   `json:"-"`
	ID                 ids.RefundID  `json:"refund_id"`
	OrderID            ids.OrderID   `json:"order_id"`
	PaymentID          ids.PaymentID `json:"payment_id"`
	Provider           string        `json:"provider"`
	AmountMinor        int64         `json:"amount_minor"`
	Currency           string        `json:"currency"`
	Reason             string        `json:"reason"`
	State              string        `json:"state"`
	ProviderResultCode *int          `json:"provider_result_code,omitempty"`
	ExternalReference  string        `json:"external_reference,omitempty"`
	CreatedAt          time.Time     `json:"created_at"`
	UpdatedAt          time.Time     `json:"updated_at"`
	SettledAt          *time.Time    `json:"settled_at,omitempty"`
	Created            bool          `json:"created,omitempty"`
}

func ValidateCancellationReason(value string) (string, error) {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) < 1 || len(value) > 240 {
		return "", ErrInvalidPayment
	}
	return value, nil
}

type MoMoResultClass int

const (
	MoMoResultSuccess MoMoResultClass = iota
	MoMoResultRetryable
	MoMoResultFinal
)

func ClassifyMoMoResult(code int) MoMoResultClass {
	switch code {
	case 0:
		return MoMoResultSuccess
	case 10, 1000, 7000, 7002:
		return MoMoResultRetryable
	default:
		return MoMoResultFinal
	}
}

func ClassifyMoMoRefundResult(code int) MoMoResultClass {
	switch code {
	case 0:
		return MoMoResultSuccess
	case 10, 1000, 1080, 1081, 7000, 7002:
		return MoMoResultRetryable
	default:
		return MoMoResultFinal
	}
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

func ValidateMoMoConfig(draft MoMoConfigDraft) (MoMoConfigDraft, error) {
	draft.PartnerCode = strings.TrimSpace(draft.PartnerCode)
	draft.AccessKey = strings.TrimSpace(draft.AccessKey)
	draft.SecretKey = strings.TrimSpace(draft.SecretKey)
	if !boundedToken(draft.PartnerCode, 2, 50) || !boundedToken(draft.AccessKey, 8, 128) || !boundedToken(draft.SecretKey, 16, 256) {
		return MoMoConfigDraft{}, ErrInvalidPayment
	}
	return draft, nil
}

func MaskPartnerCode(value string) string {
	if len(value) <= 4 {
		return strings.Repeat("*", len(value))
	}
	return value[:2] + strings.Repeat("*", len(value)-4) + value[len(value)-2:]
}

func ValidActionURL(value string, sandbox bool) bool {
	if value == "" {
		return true
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() == "" || len(value) > 2048 {
		return false
	}
	expected := "payment.momo.vn"
	if sandbox {
		expected = "test-payment.momo.vn"
	}
	return subtle.ConstantTimeCompare([]byte(strings.ToLower(parsed.Hostname())), []byte(expected)) == 1
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

func boundedToken(value string, minimum, maximum int) bool {
	if len(value) < minimum || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}
