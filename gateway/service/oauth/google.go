package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// Google: IMAP with XOAUTH2 for reading, the Gmail API for sending. The one
// scope https://mail.google.com/ covers IMAP, users.messages.send,
// settings.sendAs.list and users.getProfile (the address); no openid scope.
// prompt=consent makes Google return a refresh token on every authorisation,
// also when the user granted access before.
func init() {
	Register(&Provider{
		Name:      "google",
		Kind:      upstream.KindGmail,
		AuthURL:   "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:  "https://oauth2.googleapis.com/token",
		RevokeURL: "https://oauth2.googleapis.com/revoke",
		Scopes:    []string{"https://mail.google.com/"},
		AuthParams: map[string]string{
			"access_type": "offline",
			"prompt":      "consent",
		},
		IMAP:    upstream.Endpoint{Host: "imap.gmail.com", Port: 993, TLS: imapc.TLSImplicit},
		APIBase: "https://gmail.googleapis.com",
		Email:   gmailProfileEmail,
	})
}

// gmailProfileEmail reads users.getProfile.
func gmailProfileEmail(ctx context.Context, c *http.Client, p *Provider, tok *Token) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.APIBase, "/")+"/gmail/v1/users/me/profile", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	res, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gmail profile: %s: %s", res.Status, body)
	}
	var v struct {
		EmailAddress string `json:"emailAddress"`
	}
	if err := json.Unmarshal(body, &v); err != nil || v.EmailAddress == "" {
		return "", fmt.Errorf("gmail profile: no address in %s", body)
	}
	return strings.ToLower(v.EmailAddress), nil
}
