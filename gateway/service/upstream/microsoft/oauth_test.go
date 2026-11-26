package microsoft_test

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/woodchen-ink/czlmail/gateway/pkg/oauthtest"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream/microsoft"
)

func TestClassifyTokenError(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   microsoft.TokenClass
	}{
		{"refresh token expired", 400, `{"error":"invalid_grant","error_description":"AADSTS700082: The refresh token has expired due to inactivity.\r\nTrace ID: x","error_codes":[700082]}`, microsoft.TokenGrantGone},
		{"password changed", 400, `{"error":"invalid_grant","error_codes":[50173]}`, microsoft.TokenGrantGone},
		{"MFA now required", 400, `{"error":"interaction_required","error_codes":[50076],"suberror":"basic_action"}`, microsoft.TokenGrantGone},
		{"consent withdrawn", 400, `{"error":"invalid_grant","error_description":"AADSTS65001: The user or administrator has not consented"}`, microsoft.TokenGrantGone},
		{"grant code by number only", 400, `{"error":"invalid_request","error_codes":[70008]}`, microsoft.TokenGrantGone},
		{"client secret expired", 401, `{"error":"invalid_client","error_codes":[7000222]}`, microsoft.TokenBadClient},
		{"wrong client secret", 401, `{"error":"invalid_client","error_codes":[7000215]}`, microsoft.TokenBadClient},
		{"throttled", 429, `{"error":"temporarily_unavailable"}`, microsoft.TokenThrottled},
		{"server down", 503, `<html>Service Unavailable</html>`, microsoft.TokenRetry},
		{"unknown request error", 400, `{"error":"invalid_request","error_codes":[90014]}`, microsoft.TokenRetry},
	}
	for _, c := range cases {
		err := microsoft.ParseTokenError(c.status, []byte(c.body))
		if got := err.Class(); got != c.want {
			t.Errorf("%s: class %v, want %v (%v)", c.name, got, c.want, err)
		}
		if strings.Contains(err.Error(), "Trace ID") {
			t.Errorf("%s: error keeps the trace lines: %q", c.name, err.Error())
		}
	}
}

// Against the oauthtest stand-in answering the way login.microsoftonline.com does.
func TestClassifyTokenErrorFromEndpoint(t *testing.T) {
	p := oauthtest.New(t)
	rt := p.AddRefreshToken(mailbox)
	refresh := func() (int, []byte) {
		resp, err := http.PostForm(p.TokenURL(), url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}})
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}

	p.FailNextWith(oauthtest.Failure{Code: "invalid_grant", AADSTS: 50173})
	te := microsoft.ParseTokenError(refresh())
	if te.Class() != microsoft.TokenGrantGone || te.AADSTS() != 50173 {
		t.Errorf("revoked grant: %v (AADSTS %d)", te, te.AADSTS())
	}

	p.FailNextWith(oauthtest.Failure{Status: 503, Code: "temporarily_unavailable", AADSTS: 90033})
	if err := microsoft.ParseTokenError(refresh()); err.Class() != microsoft.TokenRetry {
		t.Errorf("outage: %v", err)
	}

	p.RevokeRefreshToken(rt)
	if err := microsoft.ParseTokenError(refresh()); err.Class() != microsoft.TokenGrantGone {
		t.Errorf("revoked refresh token: %v", err)
	}
}

func TestAdminConsentLink(t *testing.T) {
	link := microsoft.AdminConsentLink("11111111-2222-3333-4444-555555555555", "https://mail.example.test/oauth/microsoft/callback", "common", "st")
	u, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "login.microsoftonline.com" || u.Path != "/organizations/v2.0/adminconsent" {
		t.Errorf("endpoint %s", link)
	}
	q := u.Query()
	if q.Get("client_id") != "11111111-2222-3333-4444-555555555555" || q.Get("state") != "st" ||
		q.Get("redirect_uri") != "https://mail.example.test/oauth/microsoft/callback" {
		t.Errorf("query %v", q)
	}
	if q.Get("scope") != "https://outlook.office.com/IMAP.AccessAsUser.All https://outlook.office.com/SMTP.Send" {
		t.Errorf("scope %q", q.Get("scope"))
	}
	// A single-tenant app: the admin consents in that tenant.
	link = microsoft.AdminConsentLink("c", "https://mail.example.test/cb", "contoso.onmicrosoft.com", "")
	if !strings.HasPrefix(link, "https://login.microsoftonline.com/contoso.onmicrosoft.com/v2.0/adminconsent?") {
		t.Errorf("tenant link %s", link)
	}
}

func TestEndpoints(t *testing.T) {
	a, tok := microsoft.Endpoints("")
	if a != "https://login.microsoftonline.com/common/oauth2/v2.0/authorize" || tok != "https://login.microsoftonline.com/common/oauth2/v2.0/token" {
		t.Errorf("common: %s %s", a, tok)
	}
	if a, _ := microsoft.Endpoints("organizations"); a != "https://login.microsoftonline.com/organizations/oauth2/v2.0/authorize" {
		t.Errorf("organizations: %s", a)
	}
}

func idToken(claims string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256"}`)) + "." + enc([]byte(claims)) + ".sig"
}

func TestParseIDToken(t *testing.T) {
	c, err := microsoft.ParseIDToken(idToken(`{"email":"Adele@Contoso.test","preferred_username":"adele@contoso.onmicrosoft.test","tid":"t1"}`))
	if err != nil || c.Mailbox() != "adele@contoso.test" || c.Personal() {
		t.Errorf("work account: %+v %v", c, err)
	}
	c, _ = microsoft.ParseIDToken(idToken(`{"preferred_username":"adele@contoso.test","tid":"t1"}`))
	if c.Mailbox() != "adele@contoso.test" {
		t.Errorf("UPN fallback %q", c.Mailbox())
	}
	c, _ = microsoft.ParseIDToken(idToken(`{"email":"me@outlook.test","tid":"` + microsoft.PersonalTenant + `"}`))
	if !c.Personal() {
		t.Error("personal account not recognised")
	}
	c, _ = microsoft.ParseIDToken(idToken(`{"preferred_username":"+15550100"}`))
	if c.Mailbox() != "" {
		t.Errorf("phone sign-in name taken as mailbox: %q", c.Mailbox())
	}
	if _, err := microsoft.ParseIDToken("not-a-jwt"); err == nil {
		t.Error("malformed token accepted")
	}
}

func TestMailboxAddress(t *testing.T) {
	work := microsoft.IDClaims{TenantID: "t1"}
	personal := microsoft.IDClaims{TenantID: microsoft.PersonalTenant}
	cases := []struct {
		claims    microsoft.IDClaims
		login     string
		confirmed bool
	}{
		{work, "adele@contoso.test", true},
		{work, "adele@gmail.com", true}, // a work account's sign-in name is its address
		{personal, "me@outlook.com", true},
		{personal, "me@hotmail.co.uk", true},
		{personal, "me@live.cn", true},
		{personal, "me@msn.com", true},
		{personal, "user@gmail.com", false},
		{personal, "me@example.org", false},
		{personal, "me@outlook.example.org", false},
		{personal, "me@hotmail.com.au", true},
	}
	for _, c := range cases {
		addr, ok := microsoft.MailboxAddress(c.claims, c.login)
		if addr != c.login || ok != c.confirmed {
			t.Errorf("%s (tid %s): %q %v", c.login, c.claims.TenantID, addr, ok)
		}
	}
}

func TestGeneratedAddress(t *testing.T) {
	for addr, want := range map[string]bool{
		"outlook_0123456789ABCDEF@outlook.com": true,
		"outlook_a1b2c3d4e5f6a7b8@outlook.com": true,
		"outlook_team@outlook.com":             false,
		"outlook_0123456789abcdef@hotmail.com": false,
		"me@outlook.com":                       false,
	} {
		if got := microsoft.GeneratedAddress(addr); got != want {
			t.Errorf("%s: %v", addr, got)
		}
	}
}
