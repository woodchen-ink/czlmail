package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/crypto"
	"github.com/woodchen-ink/czlmail/gateway/pkg/netx"
	"github.com/woodchen-ink/czlmail/gateway/service/oauth"
)

// OAuth flow errors. The callback reports them to the browser by Msg; see
// OAuthCallbackError.
var (
	ErrOAuthState           = errors.New("oauth state unknown, expired or from another session")
	ErrOAuthDenied          = errors.New("oauth authorisation denied")
	ErrOAuthExchange        = errors.New("oauth code exchange failed")
	ErrOAuthAccountMismatch = errors.New("oauth account is not the upstream's address")
	ErrOAuthKindMismatch    = errors.New("upstream is not of this provider")
)

// Flow limits.
const (
	oauthStateTTL        = 10 * time.Minute
	oauthMaxPending      = 20 // per admin session
	oauthHTTPTimeout     = 30 * time.Second
	oauthRefreshEarly    = 5 * time.Minute
	oauthRevokeTimeout   = 30 * time.Second
	oauthCallbackTimeout = time.Minute
)

// OAuth adds and re-authorises OAuth upstreams (Gmail, Microsoft): app settings
// per provider, the authorisation-code flow with PKCE, and the access tokens
// the upstreams sign in with. Every request to the provider goes through the
// upstream's proxy.
type OAuth struct {
	db      *gorm.DB
	ring    *crypto.Keyring
	ups     *Upstreams
	mgr     *UpstreamManager
	baseURL string
	log     *slog.Logger
	now     func() time.Time

	mu        sync.Mutex
	providers map[string]*oauth.Provider
	pending   map[string]*oauthPending // by state
	tokens    map[string]*tokenEntry   // by accountId
}

type oauthPending struct {
	provider  string
	sessionID uint
	actorID   uint
	ownerID   uint   // whose upstream it becomes
	reauth    string // upstream public id, "" for a new one
	name      string
	proxy     *ProxyInput
	verifier  string
	expires   time.Time
}

// NewOAuth returns the OAuth service with every registered provider, and wires
// itself into the upstream service and manager (tokens, revoke on delete).
func NewOAuth(db *gorm.DB, ring *crypto.Keyring, ups *Upstreams, mgr *UpstreamManager, baseURL string, log *slog.Logger) *OAuth {
	o := &OAuth{db: db, ring: ring, ups: ups, mgr: mgr, baseURL: strings.TrimRight(baseURL, "/"), log: log, now: time.Now,
		providers: oauth.All(), pending: map[string]*oauthPending{}, tokens: map[string]*tokenEntry{}}
	ups.oauth = o
	mgr.oauth = o
	return o
}

// SetProvider replaces a provider's definition (tests point it at stand-ins).
// Upstreams started earlier keep the old one until restarted.
func (o *OAuth) SetProvider(p *oauth.Provider) {
	o.mu.Lock()
	o.providers[p.Name] = p.Clone()
	o.mu.Unlock()
}

func (o *OAuth) provider(name string) (*oauth.Provider, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	p, ok := o.providers[name]
	if !ok {
		return nil, false
	}
	return p.Clone(), true
}

func (o *OAuth) providerNames() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	names := make([]string, 0, len(o.providers))
	for n := range o.providers {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// providerForKind returns the provider whose upstreams have kind.
func (o *OAuth) providerForKind(kind string) (*oauth.Provider, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, p := range o.providers {
		if p.Kind == kind {
			return p.Clone(), true
		}
	}
	return nil, false
}

// ProviderOfKind returns the name of the provider whose upstreams have kind.
func (o *OAuth) ProviderOfKind(kind string) (string, bool) {
	p, ok := o.providerForKind(kind)
	if !ok {
		return "", false
	}
	return p.Name, true
}

// IsOAuthKind reports whether upstreams of kind sign in with OAuth.
func (o *OAuth) IsOAuthKind(kind string) bool {
	_, ok := o.providerForKind(kind)
	return ok
}

// OAuthStart begins adding (or re-authorising) an OAuth upstream.
type OAuthStart struct {
	Owner     string // admins: whose upstream (user public id); default the actor
	Name      string // account name; default the address
	LoginHint string // the address, preselected on the provider's sign-in page
	Proxy     *ProxyInput
	Reauth    string // upstream public id to re-authorise
}

// Start returns the provider's authorisation URL. The state is bound to the
// admin session p, used once and valid for ten minutes.
func (o *OAuth) Start(p *AdminPrincipal, provider string, in OAuthStart) (string, error) {
	client, err := o.client(provider)
	if err != nil {
		return "", err
	}
	pend := &oauthPending{provider: provider, sessionID: p.SessionID, actorID: p.User.ID, name: in.Name, proxy: in.Proxy,
		verifier: oauth.NewVerifier(), expires: o.now().Add(oauthStateTTL)}
	hint := in.LoginHint
	if in.Reauth != "" {
		up, err := o.ups.find(&p.User, in.Reauth)
		if err != nil {
			return "", err
		}
		if up.Kind != client.Provider.Kind {
			return "", ErrOAuthKindMismatch
		}
		pend.reauth, pend.ownerID, pend.proxy, hint = up.PublicID, up.UserID, nil, up.Email
	} else {
		owner, err := o.ups.owner(&p.User, in.Owner)
		if err != nil {
			return "", err
		}
		pend.ownerID = owner.ID
		if in.Proxy != nil {
			// Refuse a private proxy now rather than after the user signed in.
			draft, err := o.draftRow(pend)
			if err != nil {
				return "", err
			}
			if err := o.ups.checkHosts(context.Background(), draft); err != nil {
				return "", err
			}
		}
	}
	state := newToken(32)
	o.mu.Lock()
	now := o.now()
	n := 0
	for k, v := range o.pending {
		switch {
		case now.After(v.expires):
			delete(o.pending, k)
		case v.sessionID == p.SessionID:
			n++
		}
	}
	if n >= oauthMaxPending {
		o.mu.Unlock()
		return "", &RateLimitError{RetryAfter: oauthStateTTL}
	}
	o.pending[state] = pend
	o.mu.Unlock()
	return client.AuthCodeURL(state, pend.verifier, hint), nil
}

// take removes and returns the pending flow of state, if it is still valid.
func (o *OAuth) take(state, provider string, sessionID uint) (*oauthPending, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	pend := o.pending[state]
	if pend == nil || pend.provider != provider {
		return nil, ErrOAuthState
	}
	if pend.sessionID != sessionID {
		// Not deleted: another session must not be able to burn a victim's state.
		return nil, ErrOAuthState
	}
	delete(o.pending, state)
	if o.now().After(pend.expires) {
		return nil, ErrOAuthState
	}
	return pend, nil
}

// OAuthResult is a finished flow: the upstream, and whether it is new.
type OAuthResult struct {
	AccountID string
	Created   bool
}

// Callback finishes the flow the provider redirected back with (query q). p is
// the admin session of the browser, nil when it has none.
func (o *OAuth) Callback(ctx context.Context, p *AdminPrincipal, provider string, q url.Values) (*OAuthResult, error) {
	if p == nil {
		return nil, ErrOAuthState
	}
	pend, err := o.take(q.Get("state"), provider, p.SessionID)
	if err != nil {
		return nil, err
	}
	if pend.actorID != p.User.ID {
		return nil, ErrOAuthState
	}
	if e := q.Get("error"); e != "" {
		return nil, fmt.Errorf("%w: %s", ErrOAuthDenied, e)
	}
	client, err := o.client(provider)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, oauthCallbackTimeout)
	defer cancel()

	var row *model.Upstream
	if pend.reauth != "" {
		if row, err = o.ups.find(&p.User, pend.reauth); err != nil {
			return nil, err
		}
	} else if row, err = o.draftRow(pend); err != nil {
		return nil, err
	}
	dialer, err := o.mgr.dialer(row)
	if err != nil {
		return nil, err
	}
	hc := netx.HTTPClient(dialer, oauthHTTPTimeout)
	tok, err := client.Exchange(ctx, hc, q.Get("code"), pend.verifier)
	if err != nil {
		o.log.Warn("oauth code exchange", "provider", provider, "err", err)
		return nil, fmt.Errorf("%w: %w", ErrOAuthExchange, err)
	}
	email, err := client.Provider.Email(ctx, hc, client.Provider, tok.AccessToken)
	if err != nil {
		o.log.Warn("oauth mailbox address", "provider", provider, "err", err)
		return nil, fmt.Errorf("%w: %w", ErrOAuthExchange, err)
	}
	email = strings.ToLower(email)

	if pend.reauth == "" {
		// The same address again only renews the token: accountId stays.
		var existing model.Upstream
		err := o.db.Where("user_id = ? AND kind = ? AND lower(email) = ?", pend.ownerID, client.Provider.Kind, email).First(&existing).Error
		switch {
		case err == nil:
			row = &existing
			pend.reauth = existing.PublicID
		case !errors.Is(err, gorm.ErrRecordNotFound):
			return nil, err
		}
	}
	if pend.reauth != "" {
		if !strings.EqualFold(row.Email, email) {
			return nil, ErrOAuthAccountMismatch
		}
		if err := o.saveToken(row, tok); err != nil {
			return nil, err
		}
		o.mgr.Restart(row)
		return &OAuthResult{AccountID: row.PublicID}, nil
	}

	pv := client.Provider
	row.Email, row.Name = email, pend.name
	if row.Name == "" {
		row.Name = email
	}
	row.IMAPHost, row.IMAPPort, row.IMAPTLS, row.IMAPUsername = pv.IMAP.Host, pv.IMAP.Port, pv.IMAP.TLS, email
	row.SMTPHost, row.SMTPPort, row.SMTPTLS = pv.SMTP.Host, pv.SMTP.Port, pv.SMTP.TLS
	if row.OAuthTokenSealed, err = o.sealToken(row.PublicID, tok); err != nil {
		return nil, err
	}
	var owner model.User
	if err := o.db.First(&owner, pend.ownerID).Error; err != nil {
		return nil, err
	}
	if err := o.ups.insert(&owner, row); err != nil {
		return nil, err
	}
	o.mgr.Start(row)
	return &OAuthResult{AccountID: row.PublicID, Created: true}, nil
}

// draftRow is the upstream a pending flow would create, enough to dial its proxy.
func (o *OAuth) draftRow(pend *oauthPending) (*model.Upstream, error) {
	p, ok := o.provider(pend.provider)
	if !ok {
		return nil, ErrOAuthProviderNotFound
	}
	row := &model.Upstream{PublicID: NewAccountID(), UserID: pend.ownerID, Kind: p.Kind, ProxyMode: model.ProxyFollow}
	if pend.proxy != nil {
		if err := o.ups.apply(row, UpstreamInput{Proxy: pend.proxy}); err != nil {
			return nil, err
		}
	}
	return row, nil
}
