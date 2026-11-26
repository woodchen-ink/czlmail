package gatewaytest

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/pkg/gmailtest"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/pkg/oauthtest"
	"github.com/woodchen-ink/czlmail/gateway/service/oauth"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// microsoftFixture is a gateway whose Microsoft provider points at an oauthtest
// identity platform. The mailboxes are fake IMAP servers that accept its tokens
// over XOAUTH2 (gmailtest: the engine runs plain IMAP against it, as it does
// against Exchange).
type microsoftFixture struct {
	gw  *Gateway
	idp *oauthtest.Server
}

func newMicrosoftFixture(t *testing.T) *microsoftFixture {
	t.Helper()
	gw := Start(t)
	gw.AllowPrivateNetworks(t)
	idp := oauthtest.New(t)
	idp.ClientID, idp.ClientSecret, idp.TenantID = "app-id", "app-secret", "tenant-1"
	// Shorter than the gateway's 5-minute refresh margin: every sign-in refreshes.
	idp.ExpiresIn = time.Minute
	expires := time.Now().AddDate(0, 0, 10).Format(time.DateOnly)
	gw.Owner(t).Do(t, "PUT", "/api/admin/oauth-apps/microsoft",
		map[string]any{"clientId": "app-id", "clientSecret": "app-secret", "secretExpiresAt": expires}, nil)
	return &microsoftFixture{gw: gw, idp: idp}
}

// point makes new Microsoft upstreams use mail as their IMAP server. Upstreams
// keep the server they were created with.
func (f *microsoftFixture) point(mail *gmailtest.Server) {
	p := oauth.All()["microsoft"]
	p.AuthURL, p.TokenURL = f.idp.AuthURL(), f.idp.TokenURL()
	host, port := splitAddr(mail.Addr())
	p.IMAP = upstream.Endpoint{Host: host, Port: port, TLS: imapc.TLSNone}
	// Never the real smtp.office365.com; nothing here sends.
	p.SMTP = upstream.Endpoint{Host: "127.0.0.1", Port: 1, TLS: imapc.TLSNone}
	f.gw.App.OAuth.SetProvider(p)
}

var noRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// authorize runs the browser part: open the authorization URL (the fake signs
// in idp.AuthorizeUser), follow its redirect to the gateway's callback with the
// admin's cookies, and return the callback's redirect query.
func (f *microsoftFixture) authorize(t *testing.T, a *Admin, authURL string) url.Values {
	t.Helper()
	c := &http.Client{Jar: a.http.Jar, CheckRedirect: noRedirect}
	res, err := c.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	cb := res.Header.Get("Location")
	if !strings.HasPrefix(cb, f.gw.URL+"/oauth/microsoft/callback?") {
		t.Fatalf("provider redirected to %q", cb)
	}
	res, err = c.Get(cb)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	loc, _ := url.Parse(res.Header.Get("Location"))
	if res.StatusCode != http.StatusFound || loc == nil || loc.Path != "/admin/mailboxes/" {
		t.Fatalf("callback answered %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	return loc.Query()
}

// add connects mail to cred's user through the OAuth flow and waits for the
// first sync.
func (f *microsoftFixture) add(t *testing.T, cred Credentials, mail *gmailtest.Server) string {
	t.Helper()
	f.point(mail)
	f.idp.AuthorizeUser = mail.Account()
	a := f.gw.UserAdmin(t, cred)
	var start struct{ URL string }
	a.Do(t, "POST", "/api/admin/upstreams/oauth/microsoft/start", map[string]any{}, &start)
	q := f.authorize(t, a, start.URL)
	id := q.Get("added")
	if id == "" {
		t.Fatalf("callback query %v", q)
	}
	f.gw.WaitUpstream(t, cred, id, "ok", 30*time.Second)
	return id
}

func TestMicrosoftOAuthUpstream(t *testing.T) {
	f := newMicrosoftFixture(t)
	gw := f.gw

	// App settings: the secret expires in 10 days, so the admin is reminded; the
	// admin consent link carries the app and the callback.
	var apps []struct {
		Provider        string `json:"provider"`
		Configured      bool   `json:"configured"`
		RedirectURI     string `json:"redirectUri"`
		SecretState     string `json:"secretState"`
		AdminConsentURL string `json:"adminConsentUrl"`
	}
	gw.Owner(t).Do(t, "GET", "/api/admin/oauth-apps", nil, &apps)
	for _, app := range apps {
		if app.Provider != "microsoft" {
			continue
		}
		if !app.Configured || app.SecretState != "expiring" || app.RedirectURI != gw.URL+"/oauth/microsoft/callback" {
			t.Errorf("app %+v", app)
		}
		consent, _ := url.Parse(app.AdminConsentURL)
		if consent == nil || consent.Path != "/organizations/v2.0/adminconsent" || consent.Query().Get("client_id") != "app-id" ||
			consent.Query().Get("redirect_uri") != app.RedirectURI {
			t.Errorf("admin consent link %q", app.AdminConsentURL)
		}
	}

	mailA := gmailtest.New(t, gmailtest.Options{Account: "adele@contoso.test", Tokens: f.idp})
	mailB := gmailtest.New(t, gmailtest.Options{Account: "me@outlook.test", Tokens: f.idp})
	if _, _, err := mailA.Add(gmailtest.Inbox, []byte("From: a@b.test\r\nTo: adele@contoso.test\r\nSubject: hi\r\n\r\nbody\r\n")); err != nil {
		t.Fatal(err)
	}
	credA, credB := gw.NewUser(t), gw.NewUser(t)

	// The authorization request asks for the Exchange scopes with PKCE and lets
	// the user pick the account.
	f.point(mailA)
	var start struct{ URL string }
	gw.UserAdmin(t, credA).Do(t, "POST", "/api/admin/upstreams/oauth/microsoft/start", map[string]any{}, &start)
	au, _ := url.Parse(start.URL)
	aq := au.Query()
	if !strings.Contains(aq.Get("scope"), "https://outlook.office.com/IMAP.AccessAsUser.All") || !strings.Contains(aq.Get("scope"), "offline_access") ||
		aq.Get("code_challenge_method") != "S256" || aq.Get("prompt") != "select_account" {
		t.Errorf("authorization request %v", aq)
	}

	idA := f.add(t, credA, mailA)
	idB := f.add(t, credB, mailB)
	var row struct {
		Kind  string `json:"kind"`
		Email string `json:"email"`
		IMAP  struct {
			Username string `json:"username"`
		} `json:"imap"`
	}
	gw.UserAdmin(t, credA).Do(t, "GET", "/api/admin/upstreams/"+idA, nil, &row)
	if row.Kind != "microsoft" || row.Email != "adele@contoso.test" || row.IMAP.Username != "adele@contoso.test" {
		t.Errorf("upstream %+v", row)
	}

	// The user withdraws the grant: only that mailbox stops, at authRequired.
	f.idp.RevokeUser(mailA.Account())
	mailA.DropConnections()
	gw.WaitUpstream(t, credA, idA, "authRequired", 60*time.Second)
	mailB.DropConnections()
	time.Sleep(2 * time.Second)
	gw.WaitUpstream(t, credB, idB, "ok", 30*time.Second)

	// Re-authorisation with another account is refused and changes nothing.
	a := gw.UserAdmin(t, credA)
	var reauth struct{ URL string }
	a.Do(t, "POST", "/api/admin/upstreams/"+idA+"/reauth", nil, &reauth)
	if u, _ := url.Parse(reauth.URL); u.Query().Get("login_hint") != "adele@contoso.test" {
		t.Errorf("reauth URL %s", reauth.URL)
	}
	f.idp.AuthorizeUser = "someone@contoso.test"
	if q := f.authorize(t, a, reauth.URL); q.Get("oauthError") != "oauth_account_mismatch" {
		t.Errorf("wrong account: %v", q)
	}

	// The right account brings the mailbox back under the same accountId.
	a.Do(t, "POST", "/api/admin/upstreams/"+idA+"/reauth", nil, &reauth)
	f.idp.AuthorizeUser = mailA.Account()
	if q := f.authorize(t, a, reauth.URL); q.Get("reauthorized") != idA {
		t.Errorf("reauth: %v", q)
	}
	gw.WaitUpstream(t, credA, idA, "ok", 60*time.Second)
}

// An expired client secret (AADSTS7000222) stops the mailboxes at authRequired;
// a new secret in the app settings restarts them.
func TestMicrosoftClientSecretExpired(t *testing.T) {
	f := newMicrosoftFixture(t)
	mail := gmailtest.New(t, gmailtest.Options{Account: "adele@contoso.test", Tokens: f.idp})
	cred := f.gw.NewUser(t)
	id := f.add(t, cred, mail)

	f.idp.ClientSecret = "rotated"
	mail.DropConnections()
	f.gw.WaitUpstream(t, cred, id, "authRequired", 60*time.Second)

	f.gw.Owner(t).Do(t, "PUT", "/api/admin/oauth-apps/microsoft", map[string]any{"clientId": "app-id", "clientSecret": "rotated"}, nil)
	f.gw.WaitUpstream(t, cred, id, "ok", 60*time.Second)
}

func TestMicrosoftAdminConsentCallback(t *testing.T) {
	gw := Start(t)
	c := &http.Client{CheckRedirect: noRedirect}
	get := func(query string) url.Values {
		t.Helper()
		res, err := c.Get(gw.URL + "/oauth/microsoft/callback?" + query)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		loc, _ := url.Parse(res.Header.Get("Location"))
		if res.StatusCode != http.StatusFound || loc.Path != "/admin/mailboxes/" {
			t.Fatalf("%s: %d %s", query, res.StatusCode, res.Header.Get("Location"))
		}
		return loc.Query()
	}
	if q := get("admin_consent=True&tenant=t1&state=" + oauth.AdminConsentState); q.Get("adminConsent") != "granted" {
		t.Errorf("granted: %v", q)
	}
	if q := get("error=access_denied&error_description=AADSTS65004&state=" + oauth.AdminConsentState); q.Get("oauthError") != "oauth_denied" || q.Get("code") != "40330" {
		t.Errorf("declined: %v", q)
	}
	// Without the admin consent state it is an ordinary callback: no session.
	if q := get("admin_consent=True&state=other"); q.Get("oauthError") != "invalid_oauth_state" {
		t.Errorf("foreign state: %v", q)
	}
}
