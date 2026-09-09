package social

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestFacebookExchangeValidatesAppSubjectScopeAndProof(t *testing.T) {
	const appID = "app-id"
	const secret = "app-secret"
	const accessToken = "provider-access-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/access_token":
			if err := r.ParseForm(); err != nil || r.Form.Get("code") != "provider-code" || r.Form.Get("code_verifier") != "pkce-verifier" {
				t.Errorf("unexpected token request: %#v", r.Form)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"access_token":%q,"token_type":"bearer"}`, accessToken)
		case "/debug_token":
			if r.URL.Query().Get("input_token") != accessToken || r.URL.Query().Get("access_token") != appID+"|"+secret {
				t.Errorf("unexpected debug request")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"data":{"app_id":%q,"user_id":"facebook-user","is_valid":true,"expires_at":%d,"scopes":["public_profile","email"]}}`, appID, time.Now().Add(time.Hour).Unix())
		case "/me":
			proof := hmac.New(sha256.New, []byte(secret))
			_, _ = proof.Write([]byte(accessToken))
			if r.URL.Query().Get("access_token") != accessToken || r.URL.Query().Get("appsecret_proof") != hex.EncodeToString(proof.Sum(nil)) || r.URL.Query().Get("fields") != "id,email" {
				t.Errorf("unexpected profile request")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"facebook-user","email":"member@example.com"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	facebook := &Facebook{config: oauth2.Config{ClientID: appID, ClientSecret: secret, RedirectURL: "https://api.example/callback", Endpoint: oauth2.Endpoint{TokenURL: server.URL + "/oauth/access_token"}}, appID: appID, appSecret: secret, graphURL: server.URL, client: server.Client()}
	profile, err := facebook.Exchange(context.Background(), "provider-code", "pkce-verifier", "")
	if err != nil {
		t.Fatal(err)
	}
	if profile.Subject != "facebook-user" || profile.Email != "member@example.com" || profile.EmailVerified {
		t.Fatalf("profile = %#v", profile)
	}
}

func TestFacebookRejectsMissingRequiredScope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/oauth/access_token") {
			_, _ = w.Write([]byte(`{"access_token":"token","token_type":"bearer"}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"data":{"app_id":"app","user_id":"user","is_valid":true,"expires_at":%d,"scopes":[]}}`, time.Now().Add(time.Hour).Unix())
	}))
	defer server.Close()
	facebook := &Facebook{config: oauth2.Config{ClientID: "app", ClientSecret: "secret", Endpoint: oauth2.Endpoint{TokenURL: server.URL + "/oauth/access_token"}}, appID: "app", appSecret: "secret", graphURL: server.URL, client: server.Client()}
	if _, err := facebook.Exchange(context.Background(), "code", "verifier", ""); err == nil {
		t.Fatal("Facebook token without public_profile scope accepted")
	}
}
