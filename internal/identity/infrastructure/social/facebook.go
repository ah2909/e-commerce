package social

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ah2909/e-commerce/internal/identity/application"
	"golang.org/x/oauth2"
)

type Facebook struct {
	config    oauth2.Config
	appID     string
	appSecret string
	graphURL  string
	client    *http.Client
}

func NewFacebook(client *http.Client, appID, appSecret, redirectURL, graphVersion string) (*Facebook, error) {
	if client == nil || appID == "" || appSecret == "" || redirectURL == "" || graphVersion == "" || strings.ContainsAny(graphVersion, "/?#") {
		return nil, errors.New("incomplete Facebook configuration")
	}
	graphURL := "https://graph.facebook.com/" + graphVersion
	return &Facebook{
		config: oauth2.Config{ClientID: appID, ClientSecret: appSecret, RedirectURL: redirectURL, Endpoint: oauth2.Endpoint{AuthURL: "https://www.facebook.com/" + graphVersion + "/dialog/oauth", TokenURL: graphURL + "/oauth/access_token"}, Scopes: []string{"email", "public_profile"}},
		appID:  appID, appSecret: appSecret, graphURL: graphURL, client: client,
	}, nil
}

func (facebook *Facebook) AuthorizationURL(state, _, codeChallenge string) string {
	return facebook.config.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"))
}

func (facebook *Facebook) Exchange(ctx context.Context, code, verifier, _ string) (application.SocialProfile, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, facebook.client)
	token, err := facebook.config.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", verifier))
	if err != nil {
		return application.SocialProfile{}, classifyProviderError(err)
	}
	debugURL, _ := url.Parse(facebook.graphURL + "/debug_token")
	query := debugURL.Query()
	query.Set("input_token", token.AccessToken)
	query.Set("access_token", facebook.appID+"|"+facebook.appSecret)
	debugURL.RawQuery = query.Encode()
	var debug struct {
		Data struct {
			AppID     string   `json:"app_id"`
			UserID    string   `json:"user_id"`
			IsValid   bool     `json:"is_valid"`
			ExpiresAt int64    `json:"expires_at"`
			Scopes    []string `json:"scopes"`
		} `json:"data"`
	}
	if err := facebook.getJSON(ctx, debugURL.String(), &debug); err != nil {
		return application.SocialProfile{}, err
	}
	if !debug.Data.IsValid || debug.Data.AppID != facebook.appID || debug.Data.UserID == "" || debug.Data.ExpiresAt <= time.Now().Unix() || !contains(debug.Data.Scopes, "public_profile") {
		return application.SocialProfile{}, application.ErrSocialProviderRejected
	}
	profileURL, _ := url.Parse(facebook.graphURL + "/me")
	query = profileURL.Query()
	query.Set("fields", "id,email")
	query.Set("access_token", token.AccessToken)
	proof := hmac.New(sha256.New, []byte(facebook.appSecret))
	_, _ = proof.Write([]byte(token.AccessToken))
	query.Set("appsecret_proof", hex.EncodeToString(proof.Sum(nil)))
	profileURL.RawQuery = query.Encode()
	var profile struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	if err := facebook.getJSON(ctx, profileURL.String(), &profile); err != nil {
		return application.SocialProfile{}, err
	}
	if profile.ID != debug.Data.UserID {
		return application.SocialProfile{}, application.ErrSocialProviderRejected
	}
	return application.SocialProfile{Subject: profile.ID, Email: profile.Email, EmailVerified: false}, nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (facebook *Facebook) getJSON(ctx context.Context, endpoint string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	response, err := facebook.client.Do(request)
	if err != nil {
		return classifyProviderError(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode != http.StatusRequestTimeout && response.StatusCode != http.StatusTooManyRequests && response.StatusCode < http.StatusInternalServerError {
			return application.ErrSocialProviderRejected
		}
		return application.ErrSocialProviderUnavailable
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(target); err != nil {
		return application.ErrSocialProviderRejected
	}
	return nil
}
