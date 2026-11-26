package microsoft_test

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/pkg/oauthtest"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream/microsoft"
)

func TestClassifyTokenError(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   upstream.ErrorKind
	}{
		{"refresh token expired", 400, `{"error":"invalid_grant","error_description":"AADSTS700082: The refresh token has expired due to inactivity.\r\nTrace ID: x","error_codes":[700082]}`, upstream.KindAuth},
		{"password changed", 400, `{"error":"invalid_grant","error_codes":[50173]}`, upstream.KindAuth},
		{"MFA now required", 400, `{"error":"interaction_required","error_codes":[50076],"suberror":"basic_action"}`, upstream.KindAuth},
		{"consent withdrawn", 400, `{"error":"invalid_grant","error_description":"AADSTS65001: The user or administrator has not consented"}`, upstream.KindAuth},
		{"grant code by number only", 400, `{"error":"invalid_request","error_codes":[70008]}`, upstream.KindAuth},
		{"client secret expired", 401, `{"error":"invalid_client","error_codes":[7000222]}`, upstream.KindUnreachable},
		{"wrong client secret", 401, `{"error":"invalid_client","error_codes":[7000215]}`, upstream.KindUnreachable},
		{"throttled", 429, `{"error":"temporarily_unavailable"}`, upstream.KindThrottled},
		{"server down", 503, `<html>Service Unavailable</html>`, upstream.KindUnreachable},
		{"unknown request error", 400, `{"error":"invalid_request","error_codes":[90014]}`, upstream.KindUnreachable},
	}
	for _, c := range cases {
		err := microsoft.ClassifyTokenError(c.status, []byte(c.body))
		if err.Kind != c.want {
			t.Errorf("%s: kind %v, want %v (%v)", c.name, err.Kind, c.want, err)
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
	status, body := refresh()
	err := microsoft.ClassifyTokenError(status, body)
	te := microsoft.ParseTokenError(status, body)
	if err.Kind != upstream.KindAuth || te.AADSTS() != 50173 {
		t.Errorf("revoked grant: %v (AADSTS %d)", err, te.AADSTS())
	}

	p.FailNextWith(oauthtest.Failure{Status: 503, Code: "temporarily_unavailable", AADSTS: 90033})
	if err := microsoft.ClassifyTokenError(refresh()); err.Kind != upstream.KindUnreachable {
		t.Errorf("outage: %v", err)
	}

	p.RevokeRefreshToken(rt)
	if err := microsoft.ClassifyTokenError(refresh()); err.Kind != upstream.KindAuth {
		t.Errorf("revoked refresh token: %v", err)
	}
}

func TestAdminConsentLink(t *testing.T) {
	link := microsoft.AdminConsentLink("11111111-2222-3333-4444-555555555555", "https://mail.example.test/oauth/microsoft/callback", "st")
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
}

func TestAuthCodeParams(t *testing.T) {
	if v := microsoft.AuthCodeParams(mailbox); v.Get("login_hint") != mailbox || v.Has("prompt") {
		t.Errorf("re-auth %v", v)
	}
	if v := microsoft.AuthCodeParams(""); v.Get("prompt") != "select_account" {
		t.Errorf("first sign-in %v", v)
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

func TestSecretState(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		exp  time.Time
		want microsoft.SecretExpiry
	}{
		{time.Time{}, microsoft.SecretUnknown},
		{now.AddDate(0, 6, 0), microsoft.SecretOK},
		{now.Add(30 * 24 * time.Hour), microsoft.SecretExpiring},
		{now.Add(time.Hour), microsoft.SecretExpiring},
		{now, microsoft.SecretExpired},
		{now.AddDate(0, 0, -1), microsoft.SecretExpired},
	}
	for _, c := range cases {
		if got := microsoft.SecretState(c.exp, now); got != c.want {
			t.Errorf("expiry %v: %s, want %s", c.exp, got, c.want)
		}
	}
}
