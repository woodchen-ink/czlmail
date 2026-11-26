package oauthtest

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

type tokenResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
}

func post(t *testing.T, s *Server, form url.Values) (int, tokenResp) {
	t.Helper()
	resp, err := http.PostForm(s.TokenURL(), form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out tokenResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, out
}

func TestRefreshAndInvalidGrant(t *testing.T) {
	s := New(t)
	rt := s.AddRefreshToken("u@example.test")

	code, out := post(t, s, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}})
	if code != 200 || out.AccessToken == "" {
		t.Fatalf("refresh: %d %+v", code, out)
	}
	if user, ok := s.Validate(out.AccessToken); !ok || user != "u@example.test" {
		t.Fatalf("Validate = %q %v", user, ok)
	}

	s.FailNext("temporarily_unavailable")
	if code, out := post(t, s, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}}); code != 400 || out.Error != "temporarily_unavailable" {
		t.Fatalf("FailNext: %d %+v", code, out)
	}

	s.RevokeRefreshToken(rt)
	if code, out := post(t, s, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}}); code != 400 || out.Error != "invalid_grant" {
		t.Fatalf("revoked: %d %+v", code, out)
	}

	s.ExpireAccessTokens()
	if _, ok := s.Validate(out.AccessToken); ok {
		t.Fatal("expired token still valid")
	}
}

func TestAuthorizationCodeWithPKCE(t *testing.T) {
	s := New(t)
	s.ClientID, s.ClientSecret = "cid", "secret"
	verifier := "verifier-verifier-verifier-verifier-0123456789"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	redirect := "http://localhost/callback"

	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	q := url.Values{"response_type": {"code"}, "redirect_uri": {redirect}, "state": {"st"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	resp, err := noFollow.Get(s.AuthURL() + "?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("state") != "st" || loc.Query().Get("code") == "" {
		t.Fatalf("redirect %s", loc)
	}

	form := url.Values{"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")},
		"redirect_uri": {redirect}, "code_verifier": {verifier}, "client_id": {"cid"}, "client_secret": {"secret"}}
	code, out := post(t, s, form)
	if code != 200 || out.RefreshToken == "" {
		t.Fatalf("exchange: %d %+v", code, out)
	}
	// A code works once.
	if code, out := post(t, s, form); out.Error != "invalid_grant" {
		t.Fatalf("reused code: %d %+v", code, out)
	}
	form.Set("client_secret", "wrong")
	if code, out := post(t, s, form); code != 401 || out.Error != "invalid_client" {
		t.Fatalf("bad client: %d %+v", code, out)
	}
}
