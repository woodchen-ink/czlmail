package gatewaytest

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imaptest"
	"github.com/woodchen-ink/czlmail/gateway/pkg/netx/netxtest"
	"github.com/woodchen-ink/czlmail/gateway/pkg/smtptest"
)

type upstreamCap struct {
	Kind        string  `json:"kind"`
	Status      string  `json:"status"`
	Code        *int    `json:"code"`
	ReauthURL   *string `json:"reauthUrl"`
	InitialSync *struct {
		Done, Total int
	} `json:"initialSync"`
}

type sessionDoc struct {
	State           string            `json:"state"`
	PrimaryAccounts map[string]string `json:"primaryAccounts"`
	Accounts        map[string]struct {
		Name                string                     `json:"name"`
		IsPersonal          bool                       `json:"isPersonal"`
		AccountCapabilities map[string]json.RawMessage `json:"accountCapabilities"`
	} `json:"accounts"`
}

func (g *Gateway) session(t *testing.T, cred Credentials) sessionDoc {
	t.Helper()
	req, _ := http.NewRequest("GET", g.URL+"/jmap/session", nil)
	req.SetBasicAuth(cred.Email, cred.AppPassword)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var s sessionDoc
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func upstreamOf(t *testing.T, s sessionDoc, acct string) upstreamCap {
	t.Helper()
	a, ok := s.Accounts[acct]
	if !ok {
		t.Fatalf("account %s not in Session", acct)
	}
	var u upstreamCap
	if err := json.Unmarshal(a.AccountCapabilities["https://czl.net/jmap/upstream"], &u); err != nil {
		t.Fatalf("upstream capability: %v", err)
	}
	return u
}

// eventSource streams StateChange events of a user.
func (g *Gateway) eventSource(t *testing.T, ctx context.Context, cred Credentials) <-chan map[string]map[string]string {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, "GET", g.URL+"/jmap/eventsource?types=*&closeafter=no&ping=0", nil)
	req.SetBasicAuth(cred.Email, cred.AppPassword)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("eventsource: %v %v", err, resp)
	}
	out := make(chan map[string]map[string]string, 16)
	go func() {
		defer resp.Body.Close()
		defer close(out)
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			data, ok := strings.CutPrefix(line, "data: ")
			if !ok {
				continue
			}
			var ev struct {
				Changed map[string]map[string]string `json:"changed"`
			}
			if json.Unmarshal([]byte(data), &ev) == nil && len(ev.Changed) > 0 {
				out <- ev.Changed
			}
		}
	}()
	return out
}

func TestUpstreamLifecycle(t *testing.T) {
	srv := imaptest.Dovecot(t)
	smtptest.New(t)
	mbox := srv.NewUser(t)
	box := mbox.Dial(t)
	box.AppendSample("INBOX", "before the gateway")
	gw := Start(t)
	cred := gw.NewUser(t)
	user := gw.UserAdmin(t, cred)

	// Loopback servers are refused until private networks are allowed.
	if code, raw := user.Call(t, "POST", "/api/admin/upstreams", IMAPUpstreamBody(mbox), nil); code != 40035 {
		t.Fatalf("private network: %d %s", code, raw)
	}
	gw.AllowPrivateNetworks(t)

	// Connection test: IMAP capabilities and SMTP AUTH, nothing saved.
	var rep struct {
		IMAP struct {
			OK           bool            `json:"ok"`
			Capabilities map[string]bool `json:"capabilities"`
		} `json:"imap"`
		SMTP struct {
			OK bool `json:"ok"`
		} `json:"smtp"`
	}
	user.Do(t, "POST", "/api/admin/upstreams/test", IMAPUpstreamBody(mbox), &rep)
	if !rep.IMAP.OK || !rep.IMAP.Capabilities["qresync"] || !rep.IMAP.Capabilities["xguid"] || !rep.SMTP.OK {
		t.Fatalf("test report %+v", rep)
	}
	bad := IMAPUpstreamBody(mbox)
	bad["imap"].(map[string]any)["password"] = "wrong"
	var badRep struct {
		IMAP struct {
			OK   bool `json:"ok"`
			Code int  `json:"code"`
		} `json:"imap"`
	}
	user.Do(t, "POST", "/api/admin/upstreams/test", bad, &badRep)
	if badRep.IMAP.OK || badRep.IMAP.Code != 40110 {
		t.Fatalf("wrong password test %+v", badRep)
	}

	acct := string(gw.AddIMAPUpstream(t, cred, mbox))
	s := gw.session(t, cred)
	if u := upstreamOf(t, s, acct); u.Kind != "imap" || u.Status != "ok" || u.Code != nil || u.InitialSync != nil {
		t.Fatalf("upstream %+v", u)
	}
	if s.PrimaryAccounts["urn:ietf:params:jmap:mail"] != acct || s.PrimaryAccounts["urn:ietf:params:jmap:submission"] != acct {
		t.Fatalf("primary %v", s.PrimaryAccounts)
	}
	var mailCap struct {
		MaxMailboxesPerEmail int `json:"maxMailboxesPerEmail"`
	}
	json.Unmarshal(s.Accounts[acct].AccountCapabilities["urn:ietf:params:jmap:mail"], &mailCap)
	if mailCap.MaxMailboxesPerEmail != 1 || !s.Accounts[acct].IsPersonal {
		t.Fatalf("mail capability %+v", mailCap)
	}

	// Push: a message delivered elsewhere reaches the client as a StateChange.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := gw.eventSource(t, ctx, cred)
	time.Sleep(300 * time.Millisecond)
	start := time.Now()
	box.AppendSample("INBOX", "pushed")
	waitEvent(t, events, acct, "Email", 5*time.Second)
	t.Logf("delivery to StateChange: %v", time.Since(start))

	// A second mailbox with a wrong password: only it needs re-authorisation.
	other := srv.NewUser(t)
	other.Dial(t)
	body := IMAPUpstreamBody(other)
	body["imap"].(map[string]any)["password"] = "wrong"
	badID := gw.CreateUpstream(t, cred, body)
	gw.WaitUpstream(t, cred, badID, "authRequired", 20*time.Second)
	s = gw.session(t, cred)
	if u := upstreamOf(t, s, badID); u.Status != "authRequired" || u.Code == nil || *u.Code != 40110 || u.ReauthURL == nil || !strings.HasSuffix(*u.ReauthURL, "/admin/upstreams/"+badID) {
		t.Fatalf("bad upstream %+v", u)
	}
	if u := upstreamOf(t, s, acct); u.Status != "ok" {
		t.Fatalf("good upstream affected: %+v", u)
	}
	// The Session changed, so the stream was closed: reconnect, as clients do.
	if _, open := <-events; open {
		for range events {
		}
	}
	events = gw.eventSource(t, ctx, cred)
	time.Sleep(300 * time.Millisecond)
	box.AppendSample("INBOX", "still syncing")
	waitEvent(t, events, acct, "Email", 5*time.Second)
	// Fixing the password recovers it.
	user.Do(t, "PATCH", "/api/admin/upstreams/"+badID, map[string]any{"imap": IMAPUpstreamBody(other)["imap"]}, nil)
	gw.WaitUpstream(t, cred, badID, "ok", 20*time.Second)

	// Pause and resume; the Session state follows.
	before := gw.session(t, cred).State
	user.Do(t, "POST", "/api/admin/upstreams/"+acct+"/pause", nil, nil)
	s = gw.session(t, cred)
	if u := upstreamOf(t, s, acct); u.Status != "paused" || *u.Code != 42310 || s.State == before {
		t.Fatalf("paused %+v state %s", u, s.State)
	}
	user.Do(t, "POST", "/api/admin/upstreams/"+acct+"/resume", nil, nil)
	gw.WaitUpstream(t, cred, acct, "ok", 20*time.Second)

	// Default switch.
	user.Do(t, "POST", "/api/admin/upstreams/"+badID+"/default", nil, nil)
	if p := gw.session(t, cred).PrimaryAccounts["urn:ietf:params:jmap:mail"]; p != badID {
		t.Fatalf("primary after switch %s", p)
	}

	// Delete: the account leaves the Session, its index file is gone.
	user.Do(t, "DELETE", "/api/admin/upstreams/"+acct, nil, nil)
	if _, ok := gw.session(t, cred).Accounts[acct]; ok {
		t.Fatal("deleted account still in Session")
	}
	if _, err := os.Stat(filepath.Join(gw.DataDir, "upstreams", acct+".sqlite")); !os.IsNotExist(err) {
		t.Fatalf("index file left: %v", err)
	}
	if p := gw.session(t, cred).PrimaryAccounts["urn:ietf:params:jmap:mail"]; p != badID {
		t.Fatalf("primary after delete %s", p)
	}
}

func waitEvent(t *testing.T, events <-chan map[string]map[string]string, acct, typ string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				t.Fatal("event stream closed")
			}
			if ev[acct][typ] != "" {
				return
			}
		case <-deadline:
			t.Fatalf("no %s change for %s within %v", typ, acct, timeout)
		}
	}
}

func TestUpstreamProxyOutage(t *testing.T) {
	srv := imaptest.Dovecot(t)
	direct, proxied := srv.NewUser(t), srv.NewUser(t)
	direct.Dial(t)
	proxied.Dial(t)
	gw := Start(t)
	cred := gw.NewUser(t)
	directID := gw.AddIMAPUpstream(t, cred, direct)

	px := netxtest.Start(t, "", "")
	body := IMAPUpstreamBody(proxied)
	body["proxy"] = map[string]any{"mode": "custom", "url": "socks5://" + px.Addr()}
	id := gw.CreateUpstream(t, cred, body)
	gw.WaitUpstream(t, cred, id, "ok", 20*time.Second)
	if px.Connects.Load() == 0 {
		t.Fatal("proxy not used")
	}

	px.Stop() // drops the watcher's connection too
	u := gw.WaitUpstream(t, cred, id, "unreachable", 20*time.Second)
	if u.Status.Code == nil || *u.Status.Code != 50210 {
		t.Fatalf("unreachable %+v", u.Status)
	}
	s := gw.session(t, cred)
	if st := upstreamOf(t, s, id).Status; st != "unreachable" {
		t.Fatalf("session status %s", st)
	}
	if st := upstreamOf(t, s, string(directID)).Status; st != "ok" {
		t.Fatalf("direct upstream affected: %s", st)
	}
}
