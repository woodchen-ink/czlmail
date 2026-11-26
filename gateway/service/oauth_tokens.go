package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/crypto"
	"github.com/woodchen-ink/czlmail/gateway/pkg/netx"
	"github.com/woodchen-ink/czlmail/gateway/service/oauth"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// tokenEntry caches one upstream's token. Its mutex serialises refreshes, so
// concurrent connections that find the token stale wait for one refresh.
type tokenEntry struct {
	mu  sync.Mutex
	tok *oauth.Token
}

func tokenAAD(accountID string) crypto.AAD {
	return crypto.AAD{Table: "upstreams", RowID: accountID, Column: "oauth_token_sealed"}
}

func (o *OAuth) sealToken(accountID string, tok *oauth.Token) (string, error) {
	b, err := json.Marshal(tok)
	if err != nil {
		return "", err
	}
	return o.ring.Seal(crypto.PurposeUpstreamSecret, tokenAAD(accountID), b)
}

func (o *OAuth) openToken(accountID, sealed string) (*oauth.Token, error) {
	if sealed == "" {
		return nil, oauth.ErrInvalidGrant
	}
	b, err := o.ring.Open(crypto.PurposeUpstreamSecret, tokenAAD(accountID), sealed)
	if err != nil {
		return nil, err
	}
	var t oauth.Token
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

// saveToken stores a new token set for an existing upstream. It holds the
// cache entry's lock, so a refresh of the old grant running meanwhile cannot
// write its token over the new one afterwards.
func (o *OAuth) saveToken(row *model.Upstream, tok *oauth.Token) error {
	sealed, err := o.sealToken(row.PublicID, tok)
	if err != nil {
		return err
	}
	e := o.entry(row.PublicID)
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := o.db.Model(&model.Upstream{}).Where("id = ?", row.ID).Update("oauth_token_sealed", sealed).Error; err != nil {
		return err
	}
	row.OAuthTokenSealed = sealed
	t := *tok
	e.tok = &t
	return nil
}

func (o *OAuth) entry(accountID string) *tokenEntry {
	o.mu.Lock()
	defer o.mu.Unlock()
	e := o.tokens[accountID]
	if e == nil {
		e = &tokenEntry{}
		o.tokens[accountID] = e
	}
	return e
}

func (o *OAuth) forgetToken(accountID string) {
	o.mu.Lock()
	delete(o.tokens, accountID)
	o.mu.Unlock()
}

// appChanged drops every cached token, so the next refresh uses the new app.
// Upstreams that stopped on the old app's invalid_client are restarted.
func (o *OAuth) appChanged(provider string) {
	p, ok := o.provider(provider)
	if !ok {
		return
	}
	o.mu.Lock()
	clear(o.tokens)
	o.mu.Unlock()
	var rows []model.Upstream
	if err := o.db.Where("kind = ? AND status = ?", p.Kind, "authRequired").Find(&rows).Error; err != nil {
		return
	}
	for i := range rows {
		row := &rows[i]
		o.mgr.background(func() { o.mgr.Restart(row) })
	}
}

// accessTokenFunc is upstream.Config.AccessToken for an OAuth upstream: the
// cached access token, refreshed through dialer when it has less than five
// minutes left. A refused refresh (invalid_grant, invalid_client, no app) is an
// upstream.KindAuth error, so the runner stops at authRequired; anything else
// is KindUnreachable and retried.
func (o *OAuth) accessTokenFunc(accountID string, dialer netx.Dialer) func(ctx context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		e := o.entry(accountID)
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.tok == nil {
			var row model.Upstream
			if err := o.db.Select("id", "public_id", "kind", "oauth_token_sealed").Where("public_id = ?", accountID).First(&row).Error; err != nil {
				return "", &upstream.Error{Kind: upstream.KindUnreachable, Op: "oauth token", Err: err}
			}
			tok, err := o.openToken(accountID, row.OAuthTokenSealed)
			if err != nil {
				return "", &upstream.Error{Kind: upstream.KindAuth, Op: "oauth token", Err: err}
			}
			e.tok = tok
		}
		if e.tok.AccessToken != "" && o.now().Add(oauthRefreshEarly).Before(e.tok.Expiry) {
			return e.tok.AccessToken, nil
		}
		tok, err := o.refresh(ctx, accountID, e.tok, dialer)
		if err != nil {
			return "", err
		}
		e.tok = tok
		return tok.AccessToken, nil
	}
}

// tokenRejected is upstream.Config.TokenRejected: the cached access token is
// dropped when it is the one the server refused, so the next call refreshes.
func (o *OAuth) tokenRejected(accountID string) func(token string) {
	return func(token string) {
		e := o.entry(accountID)
		e.mu.Lock()
		if e.tok != nil && e.tok.AccessToken == token {
			e.tok.AccessToken = ""
		}
		e.mu.Unlock()
	}
}

func (o *OAuth) refresh(ctx context.Context, accountID string, old *oauth.Token, dialer netx.Dialer) (*oauth.Token, error) {
	refreshToken := old.RefreshToken
	var row model.Upstream
	if err := o.db.Where("public_id = ?", accountID).First(&row).Error; err != nil {
		return nil, &upstream.Error{Kind: upstream.KindUnreachable, Op: "oauth refresh", Err: err}
	}
	p, ok := o.providerForKind(row.Kind)
	if !ok {
		return nil, &upstream.Error{Kind: upstream.KindAuth, Op: "oauth refresh", Err: ErrOAuthProviderNotFound}
	}
	client, err := o.client(p.Name)
	if err != nil {
		kind := upstream.KindUnreachable
		if errors.Is(err, ErrOAuthAppNotConfigured) {
			kind = upstream.KindAuth
		}
		return nil, &upstream.Error{Kind: kind, Op: "oauth refresh", Err: err}
	}
	tok, err := client.Refresh(ctx, netx.HTTPClient(dialer, oauthHTTPTimeout), refreshToken)
	if err != nil {
		kind := upstream.KindUnreachable
		if errors.Is(err, oauth.ErrInvalidGrant) || errors.Is(err, oauth.ErrInvalidClient) {
			kind = upstream.KindAuth
		}
		o.log.Warn("oauth refresh", "account", accountID, "err", err)
		return nil, &upstream.Error{Kind: kind, Op: "oauth refresh", Err: err}
	}
	tok.Account = old.Account
	if a := tok.AccountFromIDToken(); a != nil {
		tok.Account = a
	}
	o.log.Info("oauth refreshed", "account", accountID, "imap_user", row.IMAPUsername, "token_account", tok.Account.String())
	sealed, err := o.sealToken(accountID, tok)
	if err != nil {
		return nil, err
	}
	if err := o.db.Model(&model.Upstream{}).Where("id = ?", row.ID).Update("oauth_token_sealed", sealed).Error; err != nil {
		return nil, &upstream.Error{Kind: upstream.KindUnreachable, Op: "oauth refresh", Err: err}
	}
	return tok, nil
}

// revoke ends the grant of a deleted upstream, best effort and in the background.
func (o *OAuth) revoke(row model.Upstream) {
	o.forgetToken(row.PublicID)
	p, ok := o.providerForKind(row.Kind)
	if !ok || row.OAuthTokenSealed == "" {
		return
	}
	// Google revokes the whole grant of the Google account; another user's
	// upstream of the same address still lives on it.
	var others int64
	if err := o.db.Model(&model.Upstream{}).Where("kind = ? AND lower(email) = ? AND id <> ?", row.Kind, strings.ToLower(row.Email), row.ID).Count(&others).Error; err != nil || others > 0 {
		return
	}
	tok, err := o.openToken(row.PublicID, row.OAuthTokenSealed)
	if err != nil {
		return
	}
	client, err := o.client(p.Name)
	if err != nil {
		return
	}
	dialer, err := o.mgr.dialer(&row)
	if err != nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), oauthRevokeTimeout)
		defer cancel()
		if err := client.Revoke(ctx, netx.HTTPClient(dialer, oauthRevokeTimeout), tok); err != nil {
			o.log.Warn("oauth revoke", "account", row.PublicID, "err", err)
		}
	}()
}

// logStart records which account an OAuth upstream's stored grant belongs to
// next to the name it signs in with, once per start.
func (o *OAuth) logStart(row *model.Upstream) {
	tok, err := o.openToken(row.PublicID, row.OAuthTokenSealed)
	if err != nil {
		o.log.Warn("oauth upstream start", "account", row.PublicID, "imap_user", row.IMAPUsername, "err", err)
		return
	}
	o.log.Info("oauth upstream start", "account", row.PublicID, "kind", row.Kind, "email", row.Email,
		"imap_user", row.IMAPUsername, "smtp_user", row.SMTPUsername, "token_account", tok.Account.String())
}
