package momo

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ah2909/e-commerce/internal/payments/application"
	"github.com/ah2909/e-commerce/internal/payments/domain"
)

var ErrInvalidResponse = errors.New("invalid MoMo response")

type Client struct {
	httpClient  *http.Client
	createURL   string
	redirectURL string
	publicURL   string
	sandbox     bool
}

func NewClient(httpClient *http.Client, baseURL, redirectURL, publicURL string) (Client, error) {
	base, err := validateDeploymentURL(baseURL)
	if err != nil || base.Port() != "" || (base.Hostname() != "test-payment.momo.vn" && base.Hostname() != "payment.momo.vn") {
		return Client{}, fmt.Errorf("invalid MoMo API URL")
	}
	redirect, redirectErr := validateDeploymentURL(redirectURL)
	public, publicErr := validateDeploymentURL(publicURL)
	if redirectErr != nil || publicErr != nil || public.RawQuery != "" || public.Fragment != "" {
		return Client{}, fmt.Errorf("invalid MoMo callback URL")
	}
	return Client{httpClient: httpClient, createURL: strings.TrimRight(base.String(), "/") + "/v2/gateway/api/create", redirectURL: redirect.String(), publicURL: strings.TrimRight(public.String(), "/"), sandbox: base.Hostname() == "test-payment.momo.vn"}, nil
}

type createRequest struct {
	PartnerCode string `json:"partnerCode"`
	StoreID     string `json:"storeId"`
	RequestID   string `json:"requestId"`
	Amount      int64  `json:"amount"`
	OrderID     string `json:"orderId"`
	OrderInfo   string `json:"orderInfo"`
	RedirectURL string `json:"redirectUrl"`
	IPNURL      string `json:"ipnUrl"`
	RequestType string `json:"requestType"`
	ExtraData   string `json:"extraData"`
	Language    string `json:"lang"`
	Signature   string `json:"signature"`
}

type createResponse struct {
	PartnerCode string `json:"partnerCode"`
	RequestID   string `json:"requestId"`
	OrderID     string `json:"orderId"`
	Amount      int64  `json:"amount"`
	ResponseMS  int64  `json:"responseTime"`
	Message     string `json:"message"`
	ResultCode  int    `json:"resultCode"`
	PayURL      string `json:"payUrl"`
	Deeplink    string `json:"deeplink"`
	QRCodeURL   string `json:"qrCodeUrl"`
	ShortLink   string `json:"shortLink"`
	MiniApp     string `json:"deeplinkMiniApp"`
	Signature   string `json:"signature"`
	UserFee     int64  `json:"userFee"`
}

func (client Client) Create(ctx context.Context, input application.MoMoCreateRequest) (application.MoMoCreateResponse, error) {
	if input.AmountMinor < 1000 || input.AmountMinor > 50_000_000 {
		return application.MoMoCreateResponse{}, ErrInvalidResponse
	}
	ipnURL := client.publicURL + "/api/v1/payment-webhooks/momo/" + input.CallbackToken
	request := createRequest{PartnerCode: input.PartnerCode, StoreID: input.StoreID.String(), RequestID: input.RequestID, Amount: input.AmountMinor, OrderID: input.MoMoOrderID, OrderInfo: input.OrderInfo, RedirectURL: client.redirectURL, IPNURL: ipnURL, RequestType: "captureWallet", ExtraData: "", Language: "vi"}
	request.Signature = sign(input.SecretKey, createCanonical(input.AccessKey, request))
	body, err := json.Marshal(request)
	if err != nil {
		return application.MoMoCreateResponse{}, fmt.Errorf("encode MoMo request: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, client.createURL, bytes.NewReader(body))
	if err != nil {
		return application.MoMoCreateResponse{}, fmt.Errorf("build MoMo request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json; charset=UTF-8")
	response, err := client.httpClient.Do(httpRequest)
	if err != nil {
		return application.MoMoCreateResponse{}, &application.MoMoGatewayError{}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		final := response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != http.StatusRequestTimeout && response.StatusCode != http.StatusTooManyRequests
		return application.MoMoCreateResponse{}, &application.MoMoGatewayError{Final: final}
	}
	var decoded createResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, 16<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return application.MoMoCreateResponse{}, &application.MoMoGatewayError{}
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || decoded.PartnerCode != input.PartnerCode || decoded.RequestID != input.RequestID || decoded.OrderID != input.MoMoOrderID || decoded.Amount != input.AmountMinor {
		return application.MoMoCreateResponse{}, &application.MoMoGatewayError{}
	}
	if decoded.Signature == "" || !validSignature(input.SecretKey, createResponseCanonical(input.AccessKey, decoded), decoded.Signature) {
		return application.MoMoCreateResponse{}, &application.MoMoGatewayError{}
	}
	if decoded.ResultCode == 0 && (decoded.PayURL == "" || !domain.ValidActionURL(decoded.PayURL, client.sandbox)) {
		return application.MoMoCreateResponse{}, &application.MoMoGatewayError{}
	}
	if !validDeeplink(decoded.Deeplink) || !validDeeplink(decoded.MiniApp) || !validQRCode(decoded.QRCodeURL) || !domain.ValidActionURL(decoded.ShortLink, client.sandbox) {
		return application.MoMoCreateResponse{}, ErrInvalidResponse
	}
	classification := domain.ClassifyMoMoResult(decoded.ResultCode)
	final, retryable := classification == domain.MoMoResultFinal, classification == domain.MoMoResultRetryable
	return application.MoMoCreateResponse{ResultCode: decoded.ResultCode, Message: boundedMessage(decoded.Message), PayURL: decoded.PayURL, Deeplink: decoded.Deeplink, QRCodeURL: decoded.QRCodeURL, Final: final, Retryable: retryable}, nil
}

type ipnBody struct {
	PartnerCode string `json:"partnerCode"`
	OrderID     string `json:"orderId"`
	RequestID   string `json:"requestId"`
	Amount      int64  `json:"amount"`
	OrderInfo   string `json:"orderInfo"`
	OrderType   string `json:"orderType"`
	TransID     int64  `json:"transId"`
	ResultCode  int    `json:"resultCode"`
	Message     string `json:"message"`
	PayType     string `json:"payType"`
	ResponseMS  int64  `json:"responseTime"`
	ExtraData   string `json:"extraData"`
	Signature   string `json:"signature"`
	PartnerUser string `json:"partnerUserId,omitempty"`
	StoreID     string `json:"storeId,omitempty"`
	Payment     string `json:"paymentOption,omitempty"`
	UserFee     int64  `json:"userFee,omitempty"`
	Promotion   any    `json:"promotionInfo,omitempty"`
}

func (client Client) VerifyIPN(raw []byte, credentials application.MoMoCredentials) (domain.MoMoIPN, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value ipnBody
	if err := decoder.Decode(&value); err != nil {
		return domain.MoMoIPN{}, ErrInvalidResponse
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || value.PartnerCode != credentials.PartnerCode || value.Signature == "" {
		return domain.MoMoIPN{}, ErrInvalidResponse
	}
	if !validSignature(credentials.SecretKey, ipnCanonical(credentials.AccessKey, value), value.Signature) {
		return domain.MoMoIPN{}, ErrInvalidResponse
	}
	if value.Amount < 0 || value.TransID < 0 || (value.ResultCode == 0 && value.TransID == 0) || value.ResponseMS <= 0 || len(value.OrderID) > 200 || len(value.RequestID) > 50 || len(value.OrderInfo) > 255 || len(value.Message) > 255 || len(value.ExtraData) > 1000 {
		return domain.MoMoIPN{}, ErrInvalidResponse
	}
	return domain.MoMoIPN{PartnerCode: value.PartnerCode, OrderID: value.OrderID, RequestID: value.RequestID, AmountMinor: value.Amount, OrderInfo: value.OrderInfo, OrderType: value.OrderType, Transaction: value.TransID, ResultCode: value.ResultCode, Message: value.Message, PayType: value.PayType, ResponseMS: value.ResponseMS, ExtraData: value.ExtraData}, nil
}

func createCanonical(accessKey string, value createRequest) string {
	return "accessKey=" + accessKey + "&amount=" + strconv.FormatInt(value.Amount, 10) + "&extraData=" + value.ExtraData + "&ipnUrl=" + value.IPNURL + "&orderId=" + value.OrderID + "&orderInfo=" + value.OrderInfo + "&partnerCode=" + value.PartnerCode + "&redirectUrl=" + value.RedirectURL + "&requestId=" + value.RequestID + "&requestType=" + value.RequestType
}

func ipnCanonical(accessKey string, value ipnBody) string {
	return "accessKey=" + accessKey + "&amount=" + strconv.FormatInt(value.Amount, 10) + "&extraData=" + value.ExtraData + "&message=" + value.Message + "&orderId=" + value.OrderID + "&orderInfo=" + value.OrderInfo + "&orderType=" + value.OrderType + "&partnerCode=" + value.PartnerCode + "&payType=" + value.PayType + "&requestId=" + value.RequestID + "&responseTime=" + strconv.FormatInt(value.ResponseMS, 10) + "&resultCode=" + strconv.Itoa(value.ResultCode) + "&transId=" + strconv.FormatInt(value.TransID, 10)
}

func createResponseCanonical(accessKey string, value createResponse) string {
	return "accessKey=" + accessKey + "&amount=" + strconv.FormatInt(value.Amount, 10) + "&message=" + value.Message + "&orderId=" + value.OrderID + "&partnerCode=" + value.PartnerCode + "&payUrl=" + value.PayURL + "&requestId=" + value.RequestID + "&responseTime=" + strconv.FormatInt(value.ResponseMS, 10) + "&resultCode=" + strconv.Itoa(value.ResultCode)
}

func sign(secret, value string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func validSignature(secret, canonical, provided string) bool {
	providedBytes, err := hex.DecodeString(provided)
	expectedBytes, _ := hex.DecodeString(sign(secret, canonical))
	return err == nil && hmac.Equal(providedBytes, expectedBytes)
}

func boundedMessage(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 255 {
		return value[:255]
	}
	return value
}

func validateDeploymentURL(value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || len(value) > 2048 {
		return nil, errors.New("invalid HTTPS URL")
	}
	return parsed, nil
}

func validDeeplink(value string) bool {
	if value == "" {
		return true
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "momo" && parsed.User == nil && len(value) <= 2048
}

func validQRCode(value string) bool {
	if len(value) > 4096 {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character > 0x7e {
			return false
		}
	}
	return true
}

func NewHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
}
