package social

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

func TestGoogleAuthorizationUsesNonceAndS256PKCE(t *testing.T) {
	google := &Google{config: oauth2.Config{ClientID: "client", RedirectURL: "https://api.example/callback", Endpoint: oauth2.Endpoint{AuthURL: "https://accounts.example/auth"}, Scopes: []string{"openid", "email"}}}
	value := google.AuthorizationURL("state-value", "nonce-value", "challenge-value")
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("state") != "state-value" || query.Get("nonce") != "nonce-value" || query.Get("code_challenge") != "challenge-value" || query.Get("code_challenge_method") != "S256" || query.Get("scope") != "openid email" {
		t.Fatalf("authorization query = %v", query)
	}
}

func TestGoogleExchangeVerifiesIssuerAudienceTimeAndNonce(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	issuer := ""
	currentToken := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/keys":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "fixture-key", "use": "sig", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(privateKey.N.Bytes()), "e": "AQAB"}}})
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"access_token":"provider-token","token_type":"Bearer","id_token":%q}`, currentToken)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	issuer = server.URL
	verifier := oidc.NewVerifier(issuer, oidc.NewRemoteKeySet(context.Background(), server.URL+"/keys"), &oidc.Config{ClientID: "google-client"})
	google := &Google{config: oauth2.Config{ClientID: "google-client", ClientSecret: "secret", Endpoint: oauth2.Endpoint{TokenURL: server.URL + "/token"}}, verifier: verifier, client: server.Client()}
	now := time.Now()
	claims := map[string]any{"iss": issuer, "aud": "google-client", "sub": "google-user", "iat": now.Add(-time.Minute).Unix(), "exp": now.Add(time.Hour).Unix(), "nonce": "expected-nonce", "email": "member@example.com", "email_verified": true}
	currentToken = signedGoogleFixture(t, privateKey, claims)
	profile, err := google.Exchange(context.Background(), "code", "verifier", "expected-nonce")
	if err != nil || profile.Subject != "google-user" || profile.Email != "member@example.com" || !profile.EmailVerified {
		t.Fatalf("Exchange() = %#v, %v", profile, err)
	}
	for _, mutation := range []func(map[string]any){
		func(value map[string]any) { value["aud"] = "other-client" },
		func(value map[string]any) { value["exp"] = now.Add(-time.Hour).Unix() },
		func(value map[string]any) { value["nonce"] = "other-nonce" },
	} {
		invalid := make(map[string]any, len(claims))
		for key, value := range claims {
			invalid[key] = value
		}
		mutation(invalid)
		currentToken = signedGoogleFixture(t, privateKey, invalid)
		if _, err := google.Exchange(context.Background(), "code", "verifier", "expected-nonce"); err == nil {
			t.Fatalf("invalid claims accepted: %v", invalid)
		}
	}
}

func signedGoogleFixture(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "fixture-key", "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join([]string{unsigned, base64.RawURLEncoding.EncodeToString(signature)}, ".")
}
