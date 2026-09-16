package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ah2909/e-commerce/internal/platform/databaseguard"
	"github.com/ah2909/e-commerce/internal/shared/ids"
	"github.com/jackc/pgx/v5/pgxpool"
)

type settings struct {
	appURL       string
	storeHost    string
	storeID      ids.StoreID
	orderID      ids.OrderID
	ownerToken   string
	shopperToken string
	partnerCode  string
	accessKey    string
	secretKey    string
	expected     int64
	databaseURL  string
	idempotency  string
}

type attemptResponse struct {
	AttemptID string `json:"attempt_id"`
	State     string `json:"state"`
	PayURL    string `json:"pay_url"`
	Deeplink  string `json:"deeplink"`
	QRCodeURL string `json:"qr_code_url"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "MoMo sandbox UAT failed:", err)
		os.Exit(1)
	}
}

func run() error {
	config, err := loadSettings()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 45 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	configBody, _ := json.Marshal(map[string]any{"expected_version": config.expected, "enabled": true, "partner_code": config.partnerCode, "access_key": config.accessKey, "secret_key": config.secretKey})
	if _, err := request(ctx, client, config, http.MethodPut, "/api/v1/admin/payment-providers/momo", config.ownerToken, "", configBody, http.StatusOK); err != nil {
		return fmt.Errorf("configure sandbox merchant: %w", err)
	}
	raw, err := request(ctx, client, config, http.MethodPost, "/api/v1/orders/"+config.orderID.String()+"/payments/momo/attempts", config.shopperToken, config.idempotency, nil, http.StatusOK, http.StatusCreated)
	if err != nil {
		return fmt.Errorf("start sandbox payment: %w", err)
	}
	var attempt attemptResponse
	if err := json.Unmarshal(raw, &attempt); err != nil || attempt.AttemptID == "" || attempt.State != "action_required" || attempt.PayURL == "" {
		return errors.New("sandbox attempt response is invalid")
	}
	fmt.Println("MoMo sandbox attempt:", attempt.AttemptID)
	fmt.Println("Pay URL:", attempt.PayURL)
	if attempt.Deeplink != "" {
		fmt.Println("Deeplink:", attempt.Deeplink)
	}
	if attempt.QRCodeURL != "" {
		fmt.Println("QR payload:", attempt.QRCodeURL)
	}
	fmt.Println("Complete the payment in the MoMo Test app; waiting up to 5 minutes for the signed IPN.")
	if err := waitForPaid(ctx, client, config); err != nil {
		return err
	}
	if err := verifyDatabase(ctx, config, attempt.AttemptID); err != nil {
		return err
	}
	fmt.Println("MoMo sandbox UAT passed: the signed IPN settled exactly one payment and inventory consumption.")
	return nil
}

func loadSettings() (settings, error) {
	required := func(name string) (string, error) {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			return "", fmt.Errorf("%s is required", name)
		}
		return value, nil
	}
	var result settings
	var err error
	if result.appURL, err = required("MOMO_SANDBOX_APP_URL"); err != nil {
		return settings{}, err
	}
	if err := validateAppURL(result.appURL); err != nil {
		return settings{}, err
	}
	if apiBase, err := required("MOMO_API_BASE_URL"); err != nil || apiBase != "https://test-payment.momo.vn" {
		return settings{}, errors.New("MOMO_API_BASE_URL must be the exact MoMo sandbox origin")
	}
	result.appURL = strings.TrimRight(result.appURL, "/")
	if result.storeHost, err = required("MOMO_SANDBOX_STORE_HOST"); err != nil {
		return settings{}, err
	}
	storeID, err := required("MOMO_SANDBOX_STORE_ID")
	if err != nil {
		return settings{}, err
	}
	if result.storeID, err = ids.ParseStoreID(storeID); err != nil {
		return settings{}, errors.New("MOMO_SANDBOX_STORE_ID is invalid")
	}
	orderID, err := required("MOMO_SANDBOX_ORDER_ID")
	if err != nil {
		return settings{}, err
	}
	if result.orderID, err = ids.ParseOrderID(orderID); err != nil {
		return settings{}, errors.New("MOMO_SANDBOX_ORDER_ID is invalid")
	}
	for target, name := range map[*string]string{&result.ownerToken: "MOMO_SANDBOX_OWNER_TOKEN", &result.shopperToken: "MOMO_SANDBOX_SHOPPER_TOKEN", &result.partnerCode: "MOMO_SANDBOX_PARTNER_CODE", &result.accessKey: "MOMO_SANDBOX_ACCESS_KEY", &result.secretKey: "MOMO_SANDBOX_SECRET_KEY", &result.databaseURL: "MOMO_SANDBOX_DATABASE_URL"} {
		if *target, err = required(name); err != nil {
			return settings{}, err
		}
	}
	result.expected = 0
	if value := strings.TrimSpace(os.Getenv("MOMO_SANDBOX_CONFIG_VERSION")); value != "" {
		result.expected, err = strconv.ParseInt(value, 10, 64)
		if err != nil || result.expected < 0 {
			return settings{}, errors.New("MOMO_SANDBOX_CONFIG_VERSION must be a non-negative integer")
		}
	}
	result.idempotency = "momo-sandbox-" + result.orderID.String()
	return result, nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validateAppURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("MOMO_SANDBOX_APP_URL must be an HTTP(S) base URL")
	}
	if parsed.Scheme != "https" && !isLoopback(parsed.Hostname()) {
		return errors.New("MOMO_SANDBOX_APP_URL must use HTTPS except on loopback")
	}
	return nil
}

func request(ctx context.Context, client *http.Client, config settings, method, path, token, idempotency string, body []byte, accepted ...int) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, config.appURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Host = config.storeHost
	request.Header.Set("Authorization", "Bearer "+token)
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotency != "" {
		request.Header.Set("Idempotency-Key", idempotency)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	if err != nil {
		return nil, err
	}
	for _, status := range accepted {
		if response.StatusCode == status {
			return raw, nil
		}
	}
	return nil, fmt.Errorf("API returned HTTP %d", response.StatusCode)
}

func waitForPaid(ctx context.Context, client *http.Client, config settings) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	deadline := time.NewTimer(5 * time.Minute)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("timed out waiting for sandbox IPN settlement")
		case <-ticker.C:
			raw, err := request(ctx, client, config, http.MethodGet, "/api/v1/orders/"+config.orderID.String(), config.shopperToken, "", nil, http.StatusOK)
			if err != nil {
				continue
			}
			var order struct {
				State string `json:"state"`
			}
			if json.Unmarshal(raw, &order) == nil && order.State == "paid" {
				return nil
			}
		}
	}
}

func verifyDatabase(ctx context.Context, config settings, attemptID string) error {
	poolConfig, err := pgxpool.ParseConfig(config.databaseURL)
	if err != nil {
		return errors.New("MOMO_SANDBOX_DATABASE_URL is invalid")
	}
	if err := databaseguard.ValidateConfig(poolConfig.ConnConfig, os.Getenv("ALLOW_TEST_DATABASE_MUTATION")); err != nil {
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return err
	}
	defer pool.Close()
	var orderState, paymentState, attemptState string
	var receipts, confirmedReservations, ledgerEntries int
	err = pool.QueryRow(ctx, `SELECT o.state,p.state,a.state,(SELECT count(*) FROM payment_webhook_receipts w WHERE w.store_id=o.store_id AND w.attempt_id=a.attempt_id AND w.outcome='paid'),(SELECT count(*) FROM inventory_reservations r WHERE r.store_id=o.store_id AND r.order_id=o.order_id AND r.status='confirmed'),(SELECT count(*) FROM inventory_ledger_entries l WHERE l.store_id=o.store_id AND l.business_reference=o.order_id::text AND l.entry_type='reservation_confirmation') FROM orders o JOIN payments p ON p.store_id=o.store_id AND p.order_id=o.order_id JOIN payment_attempts a ON a.store_id=p.store_id AND a.payment_id=p.payment_id WHERE o.store_id=$1 AND o.order_id=$2 AND a.attempt_id=$3`, config.storeID.String(), config.orderID.String(), attemptID).Scan(&orderState, &paymentState, &attemptState, &receipts, &confirmedReservations, &ledgerEntries)
	if err != nil {
		return err
	}
	if orderState != "paid" || paymentState != "succeeded" || attemptState != "succeeded" || receipts != 1 || confirmedReservations < 1 || ledgerEntries != confirmedReservations {
		return fmt.Errorf("unexpected durable effects: order=%s payment=%s attempt=%s receipts=%d reservations=%d ledgers=%d", orderState, paymentState, attemptState, receipts, confirmedReservations, ledgerEntries)
	}
	return nil
}
