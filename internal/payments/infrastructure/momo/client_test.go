package momo

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ah2909/e-commerce/internal/payments/application"
	"github.com/ah2909/e-commerce/internal/payments/domain"
	"github.com/ah2909/e-commerce/internal/shared/ids"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestCreateSignsAndValidatesBoundResponse(t *testing.T) {
	input := application.MoMoCreateRequest{MoMoCredentials: application.MoMoCredentials{PartnerCode: "MOMOTESTPARTNER", AccessKey: "test-access-key", SecretKey: "test-secret-key-123456", CallbackToken: strings.Repeat("a", 43)}, AmountMinor: 125000, MoMoOrderID: "MM0123456789abcdef0123456789abcdef", RequestID: "01234567-89ab-4cde-8fab-0123456789ab", OrderInfo: "Order 01234567-89ab-4cde-8fab-0123456789ab", StoreID: ids.StoreID("11111111-1111-4111-8111-111111111111")}
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://test-payment.momo.vn/v2/gateway/api/create" {
			t.Fatalf("request URL = %s", request.URL)
		}
		var sent createRequest
		if err := json.NewDecoder(request.Body).Decode(&sent); err != nil {
			t.Fatal(err)
		}
		if sent.IPNURL != "https://payments.example.test/api/v1/payment-webhooks/momo/"+input.CallbackToken || sent.Signature != sign(input.SecretKey, createCanonical(input.AccessKey, sent)) {
			t.Fatalf("unsigned or misrouted request = %#v", sent)
		}
		response := createResponse{PartnerCode: sent.PartnerCode, RequestID: sent.RequestID, OrderID: sent.OrderID, Amount: sent.Amount, ResponseMS: time.Now().UnixMilli(), Message: "Successful.", ResultCode: 0, PayURL: "https://test-payment.momo.vn/v2/gateway/pay?t=opaque", Deeplink: "momo://app?action=pay", QRCodeURL: "000201010212MOMOTEST"}
		response.Signature = sign(input.SecretKey, createResponseCanonical(input.AccessKey, response))
		raw, _ := json.Marshal(response)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(raw))), Header: make(http.Header)}, nil
	})
	client, err := NewClient(&http.Client{Transport: transport}, "https://test-payment.momo.vn", "https://shop.example.test/payments/momo/return", "https://payments.example.test")
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Create(context.Background(), input)
	if err != nil || result.PayURL == "" || result.Deeplink == "" || result.QRCodeURL == "" || result.ResultCode != 0 {
		t.Fatalf("Create() = %#v, %v", result, err)
	}
}

func TestCreateRejectsTamperedResponse(t *testing.T) {
	input := application.MoMoCreateRequest{MoMoCredentials: application.MoMoCredentials{PartnerCode: "MOMOTESTPARTNER", AccessKey: "test-access-key", SecretKey: "test-secret-key-123456", CallbackToken: strings.Repeat("a", 43)}, AmountMinor: 1000, MoMoOrderID: "MM0123456789abcdef0123456789abcdef", RequestID: "01234567-89ab-4cde-8fab-0123456789ab", StoreID: ids.StoreID("11111111-1111-4111-8111-111111111111")}
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"partnerCode":"MOMOTESTPARTNER","requestId":"01234567-89ab-4cde-8fab-0123456789ab","orderId":"MM0123456789abcdef0123456789abcdef","amount":1000,"responseTime":1,"message":"Successful.","resultCode":0,"payUrl":"https://test-payment.momo.vn/pay","signature":"00"}`)), Header: make(http.Header)}, nil
	})
	client, _ := NewClient(&http.Client{Transport: transport}, "https://test-payment.momo.vn", "https://shop.example.test/return", "https://payments.example.test")
	if _, err := client.Create(context.Background(), input); err == nil {
		t.Fatal("Create() accepted a tampered response")
	}
}

func TestVerifyIPNAcceptsOfficialShapeAndRejectsTampering(t *testing.T) {
	credentials := application.MoMoCredentials{PartnerCode: "MOMOTESTPARTNER", AccessKey: "test-access-key", SecretKey: "test-secret-key-123456"}
	value := ipnBody{PartnerCode: credentials.PartnerCode, OrderID: "MM0123456789abcdef0123456789abcdef", RequestID: "01234567-89ab-4cde-8fab-0123456789ab", Amount: 125000, OrderInfo: "Order test", OrderType: "momo_wallet", TransID: 4088878653, ResultCode: 0, Message: "Successful.", PayType: "qr", ResponseMS: time.Now().UnixMilli(), ExtraData: ""}
	value.Signature = sign(credentials.SecretKey, ipnCanonical(credentials.AccessKey, value))
	raw, _ := json.Marshal(value)
	client := Client{}
	got, err := client.VerifyIPN(raw, credentials)
	if err != nil || got.Transaction != value.TransID || got.AmountMinor != value.Amount {
		t.Fatalf("VerifyIPN() = %#v, %v", got, err)
	}
	value.Amount++
	tampered, _ := json.Marshal(value)
	if _, err := client.VerifyIPN(tampered, credentials); err == nil {
		t.Fatal("VerifyIPN() accepted tampered amount")
	}
}

func TestResultClassification(t *testing.T) {
	for _, test := range []struct {
		code  int
		class domain.MoMoResultClass
	}{{0, domain.MoMoResultSuccess}, {10, domain.MoMoResultRetryable}, {1000, domain.MoMoResultRetryable}, {7000, domain.MoMoResultRetryable}, {1001, domain.MoMoResultFinal}} {
		if got := domain.ClassifyMoMoResult(test.code); got != test.class {
			t.Fatalf("ClassifyMoMoResult(%d) = %v", test.code, got)
		}
	}
}

func TestVerifyIPNRejectsSuccessfulResultWithoutTransaction(t *testing.T) {
	credentials := application.MoMoCredentials{PartnerCode: "MOMO", AccessKey: "access", SecretKey: "secret"}
	value := ipnBody{PartnerCode: credentials.PartnerCode, OrderID: "order", RequestID: "01991e94-53f6-7aa0-8773-697ecf61f7a1", Amount: 1000, OrderInfo: "Order", OrderType: "momo_wallet", ResultCode: 0, Message: "Successful.", PayType: "qr", ResponseMS: time.Now().UnixMilli()}
	value.Signature = sign(credentials.SecretKey, ipnCanonical(credentials.AccessKey, value))
	raw, _ := json.Marshal(value)
	if _, err := (Client{}).VerifyIPN(raw, credentials); err == nil {
		t.Fatal("VerifyIPN() accepted success without transaction ID")
	}
}

func TestNewClientRejectsMoMoOriginPort(t *testing.T) {
	if _, err := NewClient(http.DefaultClient, "https://test-payment.momo.vn:8443", "https://shop.example/return", "https://api.example"); err == nil {
		t.Fatal("NewClient() accepted an arbitrary MoMo origin port")
	}
}

func TestCreateClassifiesHTTPFailures(t *testing.T) {
	for _, test := range []struct {
		status int
		final  bool
	}{{http.StatusBadRequest, true}, {http.StatusUnauthorized, true}, {http.StatusRequestTimeout, false}, {http.StatusTooManyRequests, false}, {http.StatusInternalServerError, false}, {http.StatusBadGateway, false}} {
		client := Client{httpClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		})}, createURL: "https://test-payment.momo.vn/v2/gateway/api/create", redirectURL: "https://shop.example/return", publicURL: "https://api.example", sandbox: true}
		_, err := client.Create(context.Background(), application.MoMoCreateRequest{MoMoCredentials: application.MoMoCredentials{PartnerCode: "MOMO", AccessKey: "access", SecretKey: "secret", CallbackToken: strings.Repeat("a", 43)}, AmountMinor: 1000, MoMoOrderID: "MM01991e9453f67aa08773697ecf61f7a1", RequestID: "01991e94-53f6-7aa0-8773-697ecf61f7a1", StoreID: ids.StoreID("11111111-1111-4111-8111-111111111111")})
		var gatewayErr *application.MoMoGatewayError
		if !errors.As(err, &gatewayErr) || gatewayErr.Final != test.final {
			t.Fatalf("Create() status %d error = %#v", test.status, err)
		}
	}
}
