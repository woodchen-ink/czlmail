// Package oauthtest is an httptest stand-in for an OAuth 2.0 provider (Google or
// Microsoft): an authorization endpoint that approves at once, and a token endpoint
// for authorization_code and refresh_token grants. Access tokens it issues are what
// the fake Gmail IMAP server and Gmail API accept. Test code only.
package oauthtest

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

// Server is the fake provider. Its zero value is not usable; call New.
type Server struct {
	*httptest.Server

	// ClientID and ClientSecret, when set, must match the client's credentials
	// (form fields or HTTP Basic); otherwise the token endpoint answers invalid_client.
	ClientID     string
	ClientSecret string
	// ExpiresIn is the lifetime of issued access tokens. Default one hour.
	ExpiresIn time.Duration
	// AuthorizeUser is who the authorization endpoint signs in. Default "user@example.test".
	AuthorizeUser string

	mu       sync.Mutex
	codes    map[string]codeGrant
	refresh  map[string]string // refresh token -> user
	access   map[string]accessGrant
	fail     []string // error codes for the next token requests, in order
	failAll  string
	requests []url.Values
}

type codeGrant struct {
	user      string
	redirect  string
	challenge string // S256 PKCE challenge, empty if none
}

type accessGrant struct {
	user    string
	expires time.Time
}

// Paths served by Server.
const (
	AuthorizePath = "/authorize"
	TokenPath     = "/token"
)

// New starts the provider; it is closed when the test ends.
func New(t testing.TB) *Server {
	s := &Server{
		codes:   map[string]codeGrant{},
		refresh: map[string]string{},
		access:  map[string]accessGrant{},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+AuthorizePath, s.authorize)
	mux.HandleFunc("POST "+TokenPath, s.token)
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// AuthURL is the authorization endpoint.
func (s *Server) AuthURL() string { return s.URL + AuthorizePath }

// TokenURL is the token endpoint.
func (s *Server) TokenURL() string { return s.URL + TokenPath }

// AddRefreshToken registers a refresh token for user and returns it.
func (s *Server) AddRefreshToken(user string) string {
	tok := "rt-" + randHex(12)
	s.mu.Lock()
	s.refresh[tok] = user
	s.mu.Unlock()
	return tok
}

// IssueAccessToken returns a valid access token for user without a token request.
func (s *Server) IssueAccessToken(user string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issueLocked(user)
}

// RevokeRefreshToken makes later refreshes with tok fail with invalid_grant, as when
// the user removes the app's access. Access tokens already issued keep working.
func (s *Server) RevokeRefreshToken(tok string) {
	s.mu.Lock()
	delete(s.refresh, tok)
	s.mu.Unlock()
}

// ExpireAccessTokens makes every issued access token invalid, as after its lifetime.
func (s *Server) ExpireAccessTokens() {
	s.mu.Lock()
	clear(s.access)
	s.mu.Unlock()
}

// FailNext makes the next token request fail with errCode (e.g. "invalid_grant",
// "temporarily_unavailable"); call it repeatedly to queue several failures.
func (s *Server) FailNext(errCode string) {
	s.mu.Lock()
	s.fail = append(s.fail, errCode)
	s.mu.Unlock()
}

// FailAll makes every token request fail with errCode until called with "".
func (s *Server) FailAll(errCode string) {
	s.mu.Lock()
	s.failAll = errCode
	s.mu.Unlock()
}

// Validate reports the user an access token belongs to. It implements the token
// check the fake IMAP server and Gmail API use.
func (s *Server) Validate(accessToken string) (user string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.access[accessToken]
	if !ok || time.Now().After(g.expires) {
		return "", false
	}
	return g.user, true
}

// Requests returns the form of every token request so far.
func (s *Server) Requests() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]url.Values(nil), s.requests...)
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect, err := url.Parse(q.Get("redirect_uri"))
	if err != nil || redirect.Scheme == "" || q.Get("response_type") != "code" {
		http.Error(w, "bad authorization request", http.StatusBadRequest)
		return
	}
	user := s.AuthorizeUser
	if user == "" {
		user = "user@example.test"
	}
	code := "code-" + randHex(12)
	g := codeGrant{user: user, redirect: q.Get("redirect_uri")}
	if q.Get("code_challenge") != "" {
		if q.Get("code_challenge_method") != "S256" {
			http.Error(w, "only S256 PKCE", http.StatusBadRequest)
			return
		}
		g.challenge = q.Get("code_challenge")
	}
	s.mu.Lock()
	s.codes[code] = g
	s.mu.Unlock()

	back := redirect.Query()
	back.Set("code", code)
	if st := q.Get("state"); st != "" {
		back.Set("state", st)
	}
	redirect.RawQuery = back.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r.PostForm)

	if s.ClientID != "" {
		id, secret, ok := r.BasicAuth()
		if !ok {
			id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
		}
		if id != s.ClientID || secret != s.ClientSecret {
			tokenError(w, http.StatusUnauthorized, "invalid_client")
			return
		}
	}
	if s.failAll != "" {
		tokenError(w, http.StatusBadRequest, s.failAll)
		return
	}
	if len(s.fail) > 0 {
		code := s.fail[0]
		s.fail = s.fail[1:]
		tokenError(w, http.StatusBadRequest, code)
		return
	}

	var user, newRefresh string
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		g, ok := s.codes[r.PostForm.Get("code")]
		delete(s.codes, r.PostForm.Get("code"))
		if !ok || g.redirect != r.PostForm.Get("redirect_uri") || !pkceOK(g.challenge, r.PostForm.Get("code_verifier")) {
			tokenError(w, http.StatusBadRequest, "invalid_grant")
			return
		}
		user = g.user
		newRefresh = "rt-" + randHex(12)
		s.refresh[newRefresh] = user
	case "refresh_token":
		u, ok := s.refresh[r.PostForm.Get("refresh_token")]
		if !ok {
			tokenError(w, http.StatusBadRequest, "invalid_grant")
			return
		}
		user = u
	default:
		tokenError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}

	resp := map[string]any{
		"access_token": s.issueLocked(user),
		"token_type":   "Bearer",
		"expires_in":   int(s.expiresIn().Seconds()),
		"scope":        r.PostForm.Get("scope"),
	}
	if newRefresh != "" {
		resp["refresh_token"] = newRefresh
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) issueLocked(user string) string {
	tok := "at-" + randHex(16)
	s.access[tok] = accessGrant{user: user, expires: time.Now().Add(s.expiresIn())}
	return tok
}

func (s *Server) expiresIn() time.Duration {
	if s.ExpiresIn > 0 {
		return s.ExpiresIn
	}
	return time.Hour
}

func pkceOK(challenge, verifier string) bool {
	if challenge == "" {
		return true
	}
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:]) == challenge
}

func tokenError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":             code,
		"error_description": "oauthtest: " + code,
	})
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
