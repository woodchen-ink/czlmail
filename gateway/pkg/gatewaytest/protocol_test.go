package gatewaytest

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/mail"
	"git.sr.ht/~rockorager/go-jmap/mail/mailbox"

	"github.com/woodchen-ink/czlmail/gateway/pkg/gmailtest"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imaptest"
	"github.com/woodchen-ink/czlmail/gateway/pkg/oauthtest"
)

// Runs today: the harness starts a gateway and reaches it over HTTP.
func TestGatewayStarts(t *testing.T) {
	gw := Start(t)
	resp, err := http.Get(gw.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"ok"`) {
		t.Fatalf("healthz: %s %s", resp.Status, body)
	}
}

// Dovecot smoke: one IMAP upstream shows up as a mail account with standard roles.
// Runs up to the upstream step; that skips until #39 lands; needs docker-compose.test.yml.
func TestProtocolDovecotSession(t *testing.T) {
	mbox := imaptest.Dovecot(t).NewUser(t)
	gw := Start(t)
	cred := gw.NewUser(t)
	requireNoUpstream(t, gw, cred)
	acct := gw.AddIMAPUpstream(t, cred, mbox)
	c := gw.Client(t, cred)
	requireMailAccount(t, c, acct, mailbox.RoleInbox, mailbox.RoleDrafts, mailbox.RoleSent, mailbox.RoleTrash)
}

// Gmail smoke: the fake Gmail upstream maps [Gmail]/All Mail to role "all".
// Runs up to the upstream step; that skips until #42 lands. No Docker needed.
func TestProtocolGmailSession(t *testing.T) {
	provider := oauthtest.New(t)
	gmail := gmailtest.New(t, gmailtest.Options{Account: "me@gmail.test", Tokens: provider})
	api := gmailtest.NewAPI(t, provider, gmail)
	gw := Start(t)
	cred := gw.NewUser(t)
	requireNoUpstream(t, gw, cred)
	acct := gw.AddGmailUpstream(t, cred, gmail, api, provider.TokenURL(), provider.AddRefreshToken("me@gmail.test"))
	c := gw.Client(t, cred)
	requireMailAccount(t, c, acct, mailbox.RoleInbox, mailbox.RoleDrafts, mailbox.RoleSent, mailbox.RoleTrash, mailbox.RoleAll)
}

func requireMailAccount(t *testing.T, c *jmap.Client, acct jmap.ID, roles ...mailbox.Role) {
	t.Helper()
	a, ok := c.Session.Accounts[acct]
	if !ok {
		t.Fatalf("account %s not in Session", acct)
	}
	if _, ok := a.RawCapabilities[mail.URI]; !ok {
		t.Fatalf("account %s lacks %s", acct, mail.URI)
	}
	req := &jmap.Request{}
	req.Invoke(&mailbox.Get{Account: acct})
	resp := Do(t, c, req)
	got, ok := resp.Responses[0].Args.(*mailbox.GetResponse)
	if !ok {
		t.Fatalf("Mailbox/get: %#v", resp.Responses[0].Args)
	}
	have := map[mailbox.Role]bool{}
	for _, m := range got.List {
		have[m.Role] = true
	}
	for _, r := range roles {
		if !have[r] {
			t.Errorf("no mailbox with role %q", r)
		}
	}
}

// requireNoUpstream checks a valid app password without upstreams gets 403
// no-upstream for the Session, not 401: the client must not ask for a new password.
func requireNoUpstream(t *testing.T, gw *Gateway, cred Credentials) {
	t.Helper()
	st, p := gw.SessionProblem(t, cred)
	if st != http.StatusForbidden || p.Type != ProblemNoUpstream || p.Code != 40301 {
		t.Fatalf("session without upstreams: HTTP %d %+v, want 403 %s", st, p, ProblemNoUpstream)
	}
}

func TestNewUserGetsWorkingAppPassword(t *testing.T) {
	gw := Start(t)
	a, b := gw.NewUser(t), gw.NewUser(t)
	if a.Email == b.Email || a.AppPassword == "" {
		t.Fatalf("users %+v %+v", a, b)
	}
	requireNoUpstream(t, gw, a)
	requireNoUpstream(t, gw, b)

	// The admin password is not a JMAP credential.
	wrong := Credentials{Email: a.Email, AppPassword: a.Password}
	if st, _ := gw.SessionProblem(t, wrong); st != http.StatusUnauthorized {
		t.Fatalf("admin password for JMAP: HTTP %d, want 401", st)
	}
}
