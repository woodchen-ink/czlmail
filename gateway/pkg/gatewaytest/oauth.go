package gatewaytest

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/gateway/pkg/gmailtest"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/pkg/oauthtest"
	"github.com/woodchen-ink/czlmail/gateway/service/oauth"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// Credentials of the OAuth app UseFakeGoogle registers.
const (
	TestClientID     = "test-client.apps.googleusercontent.com"
	TestClientSecret = "test-client-secret"
)

// UseFakeGoogle points the gateway's Google provider at the fakes (OAuth
// endpoints, Gmail IMAP in plaintext, Gmail API), allows loopback hosts and
// stores the OAuth app as the owner would. The provider signs in mail's account.
func (g *Gateway) UseFakeGoogle(t testing.TB, mail *gmailtest.Server, api *gmailtest.API, provider *oauthtest.Server) {
	t.Helper()
	provider.ClientID, provider.ClientSecret = TestClientID, TestClientSecret
	provider.AuthorizeUser = mail.Account()
	p := oauth.All()["google"]
	p.AuthURL, p.TokenURL, p.RevokeURL = provider.AuthURL(), provider.TokenURL(), provider.RevokeURL()
	host, port, _ := net.SplitHostPort(mail.Addr())
	n := 0
	for _, c := range port {
		n = n*10 + int(c-'0')
	}
	p.IMAP = upstream.Endpoint{Host: host, Port: n, TLS: imapc.TLSNone}
	p.APIBase = api.URL
	g.App.OAuth.SetProvider(p)
	g.AllowPrivateNetworks(t)
	g.Owner(t).Do(t, "PUT", "/api/admin/oauth-apps/google", map[string]any{"clientId": TestClientID, "clientSecret": TestClientSecret}, nil)
}

// AuthorizeOAuth starts an OAuth flow for cred's user with start (the body of
// POST /upstreams/oauth/{provider}/start), follows the redirects as a browser
// would and returns the query the gateway sent the browser back to
// /admin/mailboxes/ with (added, reauthorized or oauthError + code).
func (g *Gateway) AuthorizeOAuth(t testing.TB, cred Credentials, provider string, start map[string]any) url.Values {
	t.Helper()
	var out struct{ URL string }
	g.UserAdmin(t, cred).Do(t, "POST", "/api/admin/upstreams/oauth/"+provider+"/start", start, &out)
	return g.UserAdmin(t, cred).Browse(t, out.URL)
}

// ReauthorizeOAuth runs POST /upstreams/{id}/reauth and the browser redirects.
func (g *Gateway) ReauthorizeOAuth(t testing.TB, cred Credentials, id string) url.Values {
	t.Helper()
	var out struct{ URL string }
	g.UserAdmin(t, cred).Do(t, "POST", "/api/admin/upstreams/"+id+"/reauth", nil, &out)
	return g.UserAdmin(t, cred).Browse(t, out.URL)
}

// Browse opens u with the admin's cookies, follows redirects and returns the
// query of the first redirect to the gateway's /admin/ pages.
func (a *Admin) Browse(t testing.TB, u string) url.Values {
	t.Helper()
	var landed *url.URL
	c := &http.Client{Jar: a.http.Jar, CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		if strings.HasPrefix(req.URL.String(), a.base+"/admin/") {
			landed = req.URL
			return http.ErrUseLastResponse
		}
		return nil
	}}
	res, err := c.Get(u)
	if err != nil {
		t.Fatalf("gatewaytest: browse %s: %v", u, err)
	}
	res.Body.Close()
	if landed == nil {
		t.Fatalf("gatewaytest: browse %s ended at %s (HTTP %d), not the admin", u, res.Request.URL, res.StatusCode)
	}
	return landed.Query()
}

// AddGmailUpstream connects a fake Gmail account to the user through the OAuth
// flow, waits for its first sync and returns its accountId.
func (g *Gateway) AddGmailUpstream(t testing.TB, cred Credentials, mail *gmailtest.Server, api *gmailtest.API, provider *oauthtest.Server) jmap.ID {
	t.Helper()
	g.UseFakeGoogle(t, mail, api, provider)
	q := g.AuthorizeOAuth(t, cred, "google", map[string]any{"email": mail.Account(), "proxy": map[string]any{"mode": "none"}})
	id := q.Get("added")
	if id == "" {
		t.Fatalf("gatewaytest: Gmail OAuth flow: %v", q)
	}
	g.WaitUpstream(t, cred, id, "ok", 30*time.Second)
	return jmap.ID(id)
}
