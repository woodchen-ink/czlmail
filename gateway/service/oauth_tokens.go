package service

import (
	"context"
	"encoding/json"
	"errors"
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

// saveToken stores a new token set for an existing upstream and drops the cache.
func (o *OAuth) saveToken(row *model.Upstream, tok *oauth.Token) error {
	sealed, err := o.sealToken(row.PublicID, tok)
	if err != nil {
		return err
	}
	if err := o.db.Model(&model.Upstream{}).Where("id = ?", row.ID).Update("oauth_token_sealed", sealed).Error; err != nil {
		return err
	}
	row.OAuthTokenSealed = sealed
	o.forgetToken(row.PublicID)
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
		go o.mgr.Restart(&rows[i])
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
		tok, err := o.refresh(ctx, accountID, e.tok.RefreshToken, dialer)
		if err != nil {
			return "", err
		}
		e.tok = tok
		return tok.AccessToken, nil
	}
}

func (o *OAuth) refresh(ctx context.Context, accountID, refreshToken string, dialer netx.Dialer) (*oauth.Token, error) {
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
