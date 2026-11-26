package oauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// Token is what the gateway stores (sealed) for an OAuth upstream.
type Token struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	Expiry       time.Time `json:"expiry"`
	Scope        string    `json:"scope,omitempty"`
}

// Client is a provider with the app credentials the admin entered.
type Client struct {
	Provider     *Provider
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

func (c *Client) config() *oauth2.Config {
	return &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		RedirectURL:  c.RedirectURL,
		Scopes:       c.Provider.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  c.Provider.AuthURL,
			TokenURL: c.Provider.TokenURL,
			// Both Google and Microsoft take the credentials in the form; auto
			// detection would send a failing request first.
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
}

// AuthCodeURL is the authorization URL for state, the PKCE verifier and an
// optional login hint.
func (c *Client) AuthCodeURL(state, verifier, loginHint string) string {
	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(verifier)}
	for k, v := range c.Provider.AuthParams {
		opts = append(opts, oauth2.SetAuthURLParam(k, v))
	}
	if loginHint != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", loginHint))
	}
	return c.config().AuthCodeURL(state, opts...)
}

// NewVerifier returns a PKCE code verifier.
func NewVerifier() string { return oauth2.GenerateVerifier() }

// Exchange trades an authorization code for tokens. hc carries the proxy.
func (c *Client) Exchange(ctx context.Context, hc *http.Client, code, verifier string) (*Token, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, hc)
	t, err := c.config().Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, classify(err)
	}
	if t.RefreshToken == "" {
		return nil, ErrNoRefreshToken
	}
	return fromOAuth2(t, ""), nil
}

// Refresh gets a new access token. The provider may rotate the refresh token
// (Microsoft does); the returned Token keeps the old one when it does not.
func (c *Client) Refresh(ctx context.Context, hc *http.Client, refreshToken string) (*Token, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, hc)
	t, err := c.config().TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken}).Token()
	if err != nil {
		return nil, classify(err)
	}
	return fromOAuth2(t, refreshToken), nil
}

// Revoke ends the grant behind tok, best effort.
func (c *Client) Revoke(ctx context.Context, hc *http.Client, tok *Token) error {
	if c.Provider.Revoke != nil {
		return c.Provider.Revoke(ctx, hc, c.Provider, tok)
	}
	if c.Provider.RevokeURL == "" {
		return nil
	}
	t := tok.RefreshToken
	if t == "" {
		t = tok.AccessToken
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Provider.RevokeURL, strings.NewReader(url.Values{"token": {t}}.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	// 400 invalid_token: already revoked or expired, which is the goal.
	if res.StatusCode/100 != 2 && res.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("oauth: revoke: %s: %s", res.Status, body)
	}
	return nil
}

func fromOAuth2(t *oauth2.Token, refresh string) *Token {
	out := &Token{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, Expiry: t.Expiry.UTC()}
	if out.RefreshToken == "" {
		out.RefreshToken = refresh
	}
	if s, ok := t.Extra("scope").(string); ok {
		out.Scope = s
	}
	if out.Expiry.IsZero() {
		out.Expiry = time.Now().Add(time.Hour).UTC()
	}
	return out
}

// classify maps the provider's error codes to ErrInvalidGrant / ErrInvalidClient;
// anything else (network, 5xx, temporarily_unavailable) is returned as is and
// is retried by the caller.
func classify(err error) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		switch re.ErrorCode {
		case "invalid_grant":
			return fmt.Errorf("%w: %s", ErrInvalidGrant, re.ErrorDescription)
		case "invalid_client", "unauthorized_client":
			return fmt.Errorf("%w: %s", ErrInvalidClient, re.ErrorDescription)
		}
	}
	return err
}
