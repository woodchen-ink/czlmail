package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream/microsoft"
)

// Microsoft 365 and Outlook.com: IMAP and SMTP with XOAUTH2 against Exchange
// Online. The app is registered in Microsoft Entra for "any organizational
// directory and personal Microsoft accounts" (tenant "common"); the oauth_apps
// tenant narrows it. The address comes from the id_token; Microsoft has no
// per-token revocation endpoint, so deleting an upstream only drops the local
// token (the user removes the app at https://account.live.com/consent/Manage or
// https://myapps.microsoft.com).
func init() {
	auth, token := microsoft.Endpoints("")
	Register(&Provider{
		Name:     "microsoft",
		Kind:     upstream.KindMicrosoft,
		AuthURL:  auth,
		TokenURL: token,
		Scopes:   microsoft.Scopes,
		// Let a browser signed in to several accounts pick the right one; the
		// flow's login_hint preselects it on re-authorisation.
		AuthParams: map[string]string{"prompt": "select_account"},
		IMAP:       upstream.Endpoint{Host: microsoft.IMAPHost, Port: microsoft.IMAPPort, TLS: imapc.TLSImplicit},
		SMTP:       upstream.Endpoint{Host: microsoft.SMTPHost, Port: microsoft.SMTPPort, TLS: imapc.TLSStartTLS},
		// Exchange files every message submitted over SMTP in Sent Items.
		SentCopy:   upstream.SentCopyServer,
		Email:      microsoftEmail,
		Label:      "Outlook",
		OwnAddress: microsoft.GeneratedAddress,
		Mailbox: func(tok *Token, login string) (string, bool) {
			claims, _ := microsoft.ParseIDToken(tok.IDToken)
			return microsoft.MailboxAddress(claims, login)
		},
		WithTenant: microsoftTenant,
		ClassifyError: func(status int, body []byte) error {
			te := microsoft.ParseTokenError(status, body)
			switch te.Class() {
			case microsoft.TokenGrantGone:
				return fmt.Errorf("%w: %w", ErrInvalidGrant, te)
			case microsoft.TokenBadClient:
				return fmt.Errorf("%w: %w", ErrInvalidClient, te)
			}
			return te
		},
		AdminConsentURL: func(clientID, redirectURI, tenant string) string {
			return microsoft.AdminConsentLink(clientID, redirectURI, tenant, AdminConsentState)
		},
	})
}

// microsoftEmail reads the address from the id_token of the code exchange. The
// access token is for outlook.office.com and opaque to the gateway; Graph's
// userinfo would need a second token for another audience.
func microsoftEmail(_ context.Context, _ *http.Client, _ *Provider, tok *Token) (string, error) {
	if tok.IDToken == "" {
		return "", errors.New("microsoft: no id_token (openid scope missing)")
	}
	claims, err := microsoft.ParseIDToken(tok.IDToken)
	if err != nil {
		return "", err
	}
	if m := claims.Mailbox(); m != "" {
		return m, nil
	}
	return "", errors.New("microsoft: id_token has no email address (the account signs in with a phone number)")
}

func microsoftTenant(p *Provider, tenant string) *Provider {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return p
	}
	p.AuthURL, p.TokenURL = microsoft.Endpoints(tenant)
	return p
}
