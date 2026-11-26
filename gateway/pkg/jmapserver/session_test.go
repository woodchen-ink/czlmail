package jmapserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/core"

	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
)

func TestSessionThroughGoJMAP(t *testing.T) {
	e := newEnv(t, 0)
	c := e.client("alice")
	if err := c.Authenticate(); err != nil {
		t.Fatal(err)
	}
	s := c.Session
	base := e.ts.URL
	if s.APIURL != base+"/jmap/api" ||
		s.DownloadURL != base+"/jmap/download/{accountId}/{blobId}/{name}?type={type}" ||
		s.UploadURL != base+"/jmap/upload/{accountId}/" ||
		s.EventSourceURL != base+"/jmap/eventsource?types={types}&closeafter={closeafter}&ping={ping}" {
		t.Fatalf("urls: %+v", s)
	}
	if s.Username != "alice@example.com" || s.State == "" {
		t.Fatalf("username/state: %q %q", s.Username, s.State)
	}
	coreCap, ok := s.Capabilities[jmap.CoreURI].(*core.Core)
	if !ok {
		t.Fatal("no core capability")
	}
	if coreCap.MaxCallsInRequest != 16 || coreCap.MaxObjectsInGet != 500 || coreCap.MaxObjectsInSet != 500 ||
		coreCap.MaxSizeUpload != 50_000_000 || coreCap.MaxConcurrentRequests != 4 || coreCap.MaxSizeRequest != 10_000_000 {
		t.Fatalf("core limits: %+v", coreCap)
	}
	if len(s.Accounts) != 2 || !s.Accounts["a1"].IsPersonal || s.PrimaryAccounts[testCap] != "a1" {
		t.Fatalf("accounts: %+v primary %+v", s.Accounts, s.PrimaryAccounts)
	}
	var up jmapserver.UpstreamInfo
	if err := json.Unmarshal(s.Accounts["a1"].RawCapabilities[jmapserver.UpstreamCapability], &up); err != nil {
		t.Fatal(err)
	}
	if up.Kind != "imap" || up.Status != "ok" || up.Code != nil || up.ReauthURL != nil || up.InitialSync != nil {
		t.Fatalf("upstream capability: %+v", up)
	}
}

func TestSessionStateFollowsUpstreamStatus(t *testing.T) {
	e := newEnv(t, 0)
	state := func() string {
		c := e.client("alice")
		if err := c.Authenticate(); err != nil {
			t.Fatal(err)
		}
		return c.Session.State
	}
	first := state()
	if again := state(); again != first {
		t.Fatalf("state not stable: %s then %s", first, again)
	}
	e.accounts.set("usr_alice", aliceSession(jmapserver.UpstreamAuthRequired))
	changed := state()
	if changed == first {
		t.Fatal("state did not change with the upstream status")
	}

	// API responses carry the same sessionState.
	_, _, body := e.post(`{"using":["urn:ietf:params:jmap:core"],"methodCalls":[["Core/echo",{},"0"]]}`)
	var r struct {
		SessionState string `json:"sessionState"`
	}
	_ = json.Unmarshal(body, &r)
	if r.SessionState != changed {
		t.Fatalf("sessionState %q, session %q", r.SessionState, changed)
	}
}

func TestSessionNoUpstream(t *testing.T) {
	e := newEnv(t, 0)
	status, ctype, body := e.get("/jmap/session", "bob")
	if status != http.StatusForbidden || ctype != "application/problem+json" {
		t.Fatalf("got %d %s %s", status, ctype, body)
	}
	p := problemOf(t, body)
	if p["type"] != "https://czl.net/jmap/gateway/no-upstream" || p["code"] != 40301.0 || p["msg"] != "no_upstream" ||
		p["adminUrl"] != e.ts.URL+"/admin/" {
		t.Fatalf("problem: %v", p)
	}
}

func TestUnauthorized(t *testing.T) {
	e := newEnv(t, 0)
	for _, path := range []string{"/jmap/session", "/jmap/eventsource", "/jmap/download/a1/u1/x"} {
		req, _ := http.NewRequest("GET", e.ts.URL+path, nil)
		req.SetBasicAuth("alice", "wrong")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") == "" {
			t.Fatalf("%s: %d %v", path, resp.StatusCode, resp.Header)
		}
	}
	if err := e.client("mallory").Authenticate(); err == nil {
		t.Fatal("go-jmap authenticated with bad credentials")
	}
}

func TestWellKnownRedirect(t *testing.T) {
	e := newEnv(t, 0)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(e.ts.URL + "/.well-known/jmap")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != e.ts.URL+"/jmap/session" {
		t.Fatalf("%d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
}
