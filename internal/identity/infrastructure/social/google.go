package social

import (
	"context"
	"errors"
	"net/http"

	"github.com/ah2909/e-commerce/internal/identity/application"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type Google struct {
	config   oauth2.Config
	verifier *oidc.IDTokenVerifier
	client   *http.Client
}

func NewGoogle(ctx context.Context, client *http.Client, clientID, clientSecret, redirectURL string) (*Google, error) {
	if client == nil || clientID == "" || clientSecret == "" || redirectURL == "" {
		return nil, errors.New("incomplete Google configuration")
	}
	providerCtx := oidc.ClientContext(ctx, client)
	provider, err := oidc.NewProvider(providerCtx, "https://accounts.google.com")
	if err != nil {
		return nil, err
	}
	return &Google{
		config:   oauth2.Config{ClientID: clientID, ClientSecret: clientSecret, RedirectURL: redirectURL, Endpoint: provider.Endpoint(), Scopes: []string{oidc.ScopeOpenID, "email"}},
		verifier: provider.Verifier(&oidc.Config{ClientID: clientID}),
		client:   client,
	}, nil
}

func (google *Google) AuthorizationURL(state, nonce, codeChallenge string) string {
	return google.config.AuthCodeURL(state,
		oauth2.SetAuthURLParam("nonce", nonce),
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"))
}

func (google *Google) Exchange(ctx context.Context, code, verifier, nonce string) (application.SocialProfile, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, google.client)
	token, err := google.config.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", verifier))
	if err != nil {
		return application.SocialProfile{}, classifyProviderError(err)
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || raw == "" {
		return application.SocialProfile{}, application.ErrSocialProviderRejected
	}
	verified, err := google.verifier.Verify(ctx, raw)
	if err != nil {
		return application.SocialProfile{}, classifyClaimsError(err)
	}
	var claims struct {
		Nonce         string `json:"nonce"`
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := verified.Claims(&claims); err != nil || claims.Nonce != nonce || verified.Subject == "" {
		return application.SocialProfile{}, application.ErrSocialProviderRejected
	}
	return application.SocialProfile{Subject: verified.Subject, Email: claims.Email, EmailVerified: claims.EmailVerified}, nil
}
