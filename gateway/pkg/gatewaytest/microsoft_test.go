package gatewaytest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/gmailtest"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/pkg/oauthtest"
	"github.com/woodchen-ink/czlmail/gateway/service/oauth"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream/microsoft"
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
		Kind     string `json:"kind"`
		Email    string `json:"email"`
		SentCopy string `json:"sentCopy"`
		IMAP     struct {
			Username string `json:"username"`
		} `json:"imap"`
	}
	gw.UserAdmin(t, credA).Do(t, "GET", "/api/admin/upstreams/"+idA, nil, &row)
	// Exchange keeps its own Sent copy: preset, no test send needed.
	if row.Kind != "microsoft" || row.Email != "adele@contoso.test" || row.IMAP.Username != "adele@contoso.test" || row.SentCopy != "server" {
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

// microsoftIdentities returns the email addresses of the account's identities.
func microsoftIdentities(t *testing.T, gw *Gateway, cred Credentials, acct string) []string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"using":       []string{"urn:ietf:params:jmap:core", "urn:ietf:params:jmap:submission"},
		"methodCalls": []any{[]any{"Identity/get", map[string]any{"accountId": acct}, "0"}},
	})
	req, _ := http.NewRequest("POST", gw.URL+"/jmap/api", bytes.NewReader(body))
	req.SetBasicAuth(cred.Email, cred.AppPassword)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var calls [][]json.RawMessage
	if err := json.Unmarshal(raw, &struct {
		MethodResponses *[][]json.RawMessage `json:"methodResponses"`
	}{&calls}); err != nil || len(calls) != 1 || len(calls[0]) != 3 {
		t.Fatalf("Identity/get: %s", raw)
	}
	var args struct {
		List []struct {
			Email string `json:"email"`
		} `json:"list"`
	}
	if err := json.Unmarshal(calls[0][1], &args); err != nil {
		t.Fatalf("Identity/get: %s", raw)
	}
	var emails []string
	for _, i := range args.List {
		emails = append(emails, i.Email)
	}
	return emails
}

type microsoftRow struct {
	Name           string `json:"name"`
	Email          string `json:"email"`
	AddressPending bool   `json:"addressPending"`
	IMAP           struct {
		Username string `json:"username"`
	} `json:"imap"`
}

// A personal Microsoft account registered with a Gmail address: it signs in as
// that address, but its mailbox (and so its From) is outlook_<hex>@outlook.com,
// which only its mail shows. Until it is found the name says "(Outlook)", so it
// is not confused with the Gmail mailbox of the same address.
func TestMicrosoftExternalAddressAccount(t *testing.T) {
	f := newMicrosoftFixture(t)
	f.idp.TenantID = microsoft.PersonalTenant
	const login, own = "wood@gmail.test", "outlook_0123456789abcdef@outlook.com"
	mail := gmailtest.New(t, gmailtest.Options{Account: login, Tokens: f.idp})
	if _, _, err := mail.Add(gmailtest.Inbox, []byte("From: Outlook Team <no-reply@microsoft.test>\r\nTo: "+own+"\r\nSubject: Welcome\r\n\r\nhi\r\n")); err != nil {
		t.Fatal(err)
	}
	cred := f.gw.NewUser(t)
	// The user's Gmail mailbox, named by its address.
	var user struct{ ID uint }
	f.gw.DB.Raw("SELECT id FROM users WHERE email = ?", cred.Email).Scan(&user)
	if err := f.gw.DB.Create(&model.Upstream{PublicID: "gmailtwin" + randHex(4), UserID: user.ID, Kind: model.UpstreamKindGmail, Name: login, Email: login, Paused: true}).Error; err != nil {
		t.Fatal(err)
	}

	id := f.add(t, cred, mail)
	a := f.gw.UserAdmin(t, cred)
	deadline := time.Now().Add(30 * time.Second)
	var row microsoftRow
	for {
		a.Do(t, "GET", "/api/admin/upstreams/"+id, nil, &row)
		if !row.AddressPending || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if row.AddressPending || row.Email != own || row.Name != own || row.IMAP.Username != login {
		t.Fatalf("after the first sync: %+v", row)
	}
	if ids := microsoftIdentities(t, f.gw, cred, id); len(ids) != 1 || ids[0] != own {
		t.Errorf("identities %v", ids)
	}

	// Re-authorisation matches the sign-in name, not the (now different) address.
	var reauth struct{ URL string }
	a.Do(t, "POST", "/api/admin/upstreams/"+id+"/reauth", nil, &reauth)
	if q := f.authorize(t, a, reauth.URL); q.Get("reauthorized") != id {
		t.Errorf("reauth: %v", q)
	}
	// So does adding the same account again.
	var start struct{ URL string }
	a.Do(t, "POST", "/api/admin/upstreams/oauth/microsoft/start", map[string]any{}, &start)
	if q := f.authorize(t, a, start.URL); q.Get("reauthorized") != id {
		t.Errorf("same account again: %v", q)
	}
}

// Without mail that shows its address, the mailbox stays pending under a
// labelled name until the admin sets the address.
func TestMicrosoftExternalAddressPending(t *testing.T) {
	f := newMicrosoftFixture(t)
	f.idp.TenantID = microsoft.PersonalTenant
	mail := gmailtest.New(t, gmailtest.Options{Account: "wood@gmail.test", Tokens: f.idp})
	cred := f.gw.NewUser(t)
	id := f.add(t, cred, mail)
	a := f.gw.UserAdmin(t, cred)
	time.Sleep(time.Second) // the scan runs after the first sync
	var row microsoftRow
	a.Do(t, "GET", "/api/admin/upstreams/"+id, nil, &row)
	if !row.AddressPending || row.Email != "wood@gmail.test" || row.Name != "wood@gmail.test (Outlook)" {
		t.Fatalf("pending: %+v", row)
	}
	a.Do(t, "PATCH", "/api/admin/upstreams/"+id, map[string]any{"email": "outlook_fedcba9876543210@outlook.com", "name": "Outlook"}, &row)
	if row.AddressPending || row.Email != "outlook_fedcba9876543210@outlook.com" || row.Name != "Outlook" || row.IMAP.Username != "wood@gmail.test" {
		t.Fatalf("after PATCH: %+v", row)
	}
	if ids := microsoftIdentities(t, f.gw, cred, id); len(ids) != 1 || ids[0] != "outlook_fedcba9876543210@outlook.com" {
		t.Errorf("identities %v", ids)
	}
}

// Two Microsoft mailboxes of one user, added one after the other in the same
// browser session: each signs in to IMAP with its own grant. The fake IMAP
// servers accept a token only for their own account, so a token used for the
// wrong mailbox fails the sign-in.
func TestMicrosoftTwoMailboxesOwnTokens(t *testing.T) {
	f := newMicrosoftFixture(t)
	f.idp.TenantID = "9188040d-6c67-4c5b-b112-36a304b66dad" // personal accounts
	mailA := gmailtest.New(t, gmailtest.Options{Account: "first@outlook.test", Tokens: f.idp})
	mailB := gmailtest.New(t, gmailtest.Options{Account: "second@outlook.test", Tokens: f.idp})
	cred := f.gw.NewUser(t)
	idA := f.add(t, cred, mailA)
	idB := f.add(t, cred, mailB)
	if idA == idB {
		t.Fatal("second account renewed the first one")
	}

	// Every reconnect refreshes (ExpiresIn is below the refresh margin) and
	// signs in again; both stay ok, many times over.
	for range 3 {
		mailA.DropConnections()
		mailB.DropConnections()
		time.Sleep(300 * time.Millisecond)
		f.gw.WaitUpstream(t, cred, idA, "ok", 30*time.Second)
		f.gw.WaitUpstream(t, cred, idB, "ok", 30*time.Second)
	}
	for _, id := range []string{idA, idB} {
		var row struct {
			Email string `json:"email"`
			SMTP  struct {
				Host string `json:"host"`
			} `json:"smtp"`
		}
		f.gw.UserAdmin(t, cred).Do(t, "GET", "/api/admin/upstreams/"+id, nil, &row)
		if row.SMTP.Host != "127.0.0.1" {
			t.Errorf("%s: test SMTP server replaced by %q", row.Email, row.SMTP.Host)
		}
	}
}
