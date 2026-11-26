// Package oauth holds what the gateway knows about OAuth mail providers (Google,
// Microsoft): their endpoints, scopes and mail servers, and the token requests
// (code exchange with PKCE, refresh, revoke). The flow itself — app settings,
// state, creating upstreams, caching tokens — is service.OAuth.
//
// A provider registers itself from an init function (google.go, microsoft.go).
package oauth

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sync"

	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// Provider is one OAuth mail provider.
type Provider struct {
	// Name is the provider's URL segment: /oauth/{name}/callback, /oauth-apps/{name}.
	Name string
	// Kind is the upstream kind its mailboxes get (upstream.KindGmail, ...).
	Kind string

	AuthURL   string
	TokenURL  string
	RevokeURL string // empty: the provider cannot revoke (see Revoke)
	Scopes    []string
	// AuthParams are extra authorization parameters (access_type=offline,
	// prompt=consent). login_hint is added by the flow.
	AuthParams map[string]string

	// IMAP and SMTP are the mail servers new upstreams of this provider use
	// (Username and Password are ignored). An empty SMTP host means mail is sent
	// another way (the Gmail API).
	IMAP upstream.Endpoint
	SMTP upstream.Endpoint
	// APIBase is the provider's REST API root (https://gmail.googleapis.com),
	// handed to the upstream as Config.APIBase.
	APIBase string

	// Email returns the address of the mailbox the access token belongs to. c
	// goes through the upstream's proxy.
	Email func(ctx context.Context, c *http.Client, p *Provider, accessToken string) (string, error)
	// WithTenant adapts the endpoints to the app's tenant (Microsoft:
	// login.microsoftonline.com/{tenant}/...); it gets a clone. Nil: no tenants.
	WithTenant func(p *Provider, tenant string) *Provider
	// Revoke ends the grant. Nil posts token=<refresh token> to RevokeURL, or
	// does nothing when RevokeURL is empty.
	Revoke func(ctx context.Context, c *http.Client, p *Provider, tok *Token) error
}

// Clone returns a copy that can be changed (tests point endpoints at stand-ins).
func (p *Provider) Clone() *Provider {
	c := *p
	c.Scopes = slices.Clone(p.Scopes)
	c.AuthParams = make(map[string]string, len(p.AuthParams))
	for k, v := range p.AuthParams {
		c.AuthParams[k] = v
	}
	return &c
}

var (
	regMu     sync.RWMutex
	providers = map[string]*Provider{}
)

// Register makes a provider available; call it from an init function.
func Register(p *Provider) {
	regMu.Lock()
	defer regMu.Unlock()
	providers[p.Name] = p
}

// All returns a copy of every registered provider, by name.
func All() map[string]*Provider {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make(map[string]*Provider, len(providers))
	for k, p := range providers {
		out[k] = p.Clone()
	}
	return out
}

// Errors of token requests.
var (
	// ErrInvalidGrant: the provider refused the code or refresh token
	// (invalid_grant) — the user revoked access, the token expired (Google apps
	// in "Testing" status lose refresh tokens after 7 days) or the password
	// changed. The upstream needs re-authorisation.
	ErrInvalidGrant = errors.New("oauth: invalid_grant")
	// ErrInvalidClient: the client id or secret is wrong (or the secret expired).
	ErrInvalidClient = errors.New("oauth: invalid_client")
	// ErrNoRefreshToken: the provider granted no refresh token.
	ErrNoRefreshToken = errors.New("oauth: no refresh token granted")
)
