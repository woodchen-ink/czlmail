package gatewaytest

import (
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imaptest"
	"github.com/woodchen-ink/czlmail/gateway/pkg/smtptest"
)

// Upstream is an upstream as the admin API returns it (the fields tests use).
type Upstream struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	IsDefault bool   `json:"isDefault"`
	Paused    bool   `json:"paused"`
	Status    struct {
		State       string `json:"state"`
		Code        *int   `json:"code"`
		Detail      string `json:"detail"`
		InitialSync *struct {
			Done  int `json:"done"`
			Total int `json:"total"`
		} `json:"initialSync"`
	} `json:"status"`
}

// AllowPrivateNetworks turns on security.allow_private_networks: the test servers
// listen on loopback.
func (g *Gateway) AllowPrivateNetworks(t testing.TB) {
	t.Helper()
	g.Owner(t).Do(t, "PATCH", "/api/admin/settings", map[string]any{"security.allow_private_networks": true}, nil)
}

// IMAPUpstreamBody is the create request for a Dovecot user (plaintext, no proxy),
// with SMTP at Mailpit when it is configured.
func IMAPUpstreamBody(mbox *imaptest.User) map[string]any {
	host, port := splitAddr(mbox.Server.Addr)
	body := map[string]any{
		"name":  mbox.Name,
		"email": mbox.Name + "@imaptest.invalid",
		"imap":  map[string]any{"host": host, "port": port, "tls": "plain", "username": mbox.Name, "password": mbox.Password},
		"proxy": map[string]any{"mode": "none"},
	}
	if addr := os.Getenv(smtptest.EnvSMTPAddr); addr != "" {
		h, p := splitAddr(addr)
		body["smtp"] = map[string]any{"host": h, "port": p, "tls": "plain", "username": mbox.Name, "password": "any"}
	}
	return body
}

func splitAddr(addr string) (string, int) {
	h, p, _ := net.SplitHostPort(addr)
	n, _ := strconv.Atoi(p)
	return h, n
}

// AddIMAPUpstream connects a Dovecot mailbox to the user as a JMAP account, waits
// for its first sync and returns its accountId.
func (g *Gateway) AddIMAPUpstream(t testing.TB, cred Credentials, mbox *imaptest.User) jmap.ID {
	t.Helper()
	g.AllowPrivateNetworks(t)
	id := g.CreateUpstream(t, cred, IMAPUpstreamBody(mbox))
	g.WaitUpstream(t, cred, id, "ok", 30*time.Second)
	return jmap.ID(id)
}

// CreateUpstream posts an upstream for cred's user and returns its id without waiting.
func (g *Gateway) CreateUpstream(t testing.TB, cred Credentials, body map[string]any) string {
	t.Helper()
	var u Upstream
	g.UserAdmin(t, cred).Do(t, "POST", "/api/admin/upstreams", body, &u)
	return u.ID
}

// GetUpstream reads one upstream through the admin API.
func (g *Gateway) GetUpstream(t testing.TB, cred Credentials, id string) Upstream {
	t.Helper()
	var u Upstream
	g.UserAdmin(t, cred).Do(t, "GET", "/api/admin/upstreams/"+id, nil, &u)
	return u
}

// WaitUpstream waits until the upstream reports state.
func (g *Gateway) WaitUpstream(t testing.TB, cred Credentials, id, state string, timeout time.Duration) Upstream {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		u := g.GetUpstream(t, cred, id)
		if u.Status.State == state {
			return u
		}
		if time.Now().After(deadline) {
			t.Fatalf("gatewaytest: upstream %s is %q (%s), want %q", id, u.Status.State, u.Status.Detail, state)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// UserAdmin returns an admin API client signed in as cred's user (cached).
func (g *Gateway) UserAdmin(t testing.TB, cred Credentials) *Admin {
	t.Helper()
	g.mu.Lock()
	a := g.users[cred.Email]
	g.mu.Unlock()
	if a != nil {
		return a
	}
	a = g.Login(t, cred.Email, cred.Password)
	g.mu.Lock()
	if g.users == nil {
		g.users = map[string]*Admin{}
	}
	g.users[cred.Email] = a
	g.mu.Unlock()
	return a
}
