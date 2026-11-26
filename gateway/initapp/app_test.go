package initapp_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/gateway/config"
	"github.com/woodchen-ink/czlmail/gateway/database"
	"github.com/woodchen-ink/czlmail/gateway/initapp"
)

// newGateway serves the fully assembled gateway, as Serve does.
func newGateway(t *testing.T) (*initapp.App, string) {
	t.Helper()
	// Cleanups run in reverse: app, HTTP server, database, then the directory.
	dir := t.TempDir()
	db, err := database.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close(db) })
	var app *initapp.App
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { app.Handler.ServeHTTP(w, r) }))
	t.Cleanup(ts.Close)
	cfg := &config.Config{MasterKey: bytes.Repeat([]byte{7}, config.MasterKeySize), BaseURL: ts.URL, DataDir: dir}
	app, err = initapp.NewApp(cfg, db, slog.New(slog.NewJSONHandler(io.Discard, nil)), "2026.12.0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Close) // runs before ts.Close, so open streams do not block it
	return app, ts.URL
}

// admin is a signed-in admin API client.
type admin struct {
	t    *testing.T
	base string
	http *http.Client
}

func (a *admin) do(method, path string, body any) json.RawMessage {
	a.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, a.base+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	u, _ := url.Parse(a.base)
	for _, c := range a.http.Jar.Cookies(u) {
		if c.Name == "csrf_token" {
			req.Header.Set("X-CSRF-Token", c.Value)
		}
	}
	res, err := a.http.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	defer res.Body.Close()
	var r struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	raw, _ := io.ReadAll(res.Body)
	if json.Unmarshal(raw, &r) != nil || r.Code != 200 {
		a.t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, raw)
	}
	return r.Data
}

func TestEndToEndSetupAppPasswordSession(t *testing.T) {
	app, base := newGateway(t)

	// Owner setup, then an app password for czlmail.
	token, err := app.Auth.Accounts.IssueSetupToken()
	if err != nil || token == "" {
		t.Fatalf("setup token: %q %v", token, err)
	}
	jar, _ := cookiejar.New(nil)
	adm := &admin{t: t, base: base, http: &http.Client{Jar: jar}}
	adm.do("POST", "/api/admin/setup", map[string]string{
		"token": token, "email": "owner@example.com", "name": "Owner", "password": "correct horse battery",
	})
	var pw struct{ ID, Password string }
	_ = json.Unmarshal(adm.do("POST", "/api/admin/app-passwords", map[string]string{"name": "Laptop"}), &pw)

	// go-jmap signs in with Basic: valid credentials, but no mailbox yet.
	c := (&jmap.Client{SessionEndpoint: base + "/.well-known/jmap"}).WithBasicAuth("owner@example.com", pw.Password)
	if err := c.Authenticate(); err == nil {
		t.Fatal("session without upstreams should fail")
	}
	resp, err := c.HttpClient.Get(base + "/jmap/session")
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Type string `json:"type"`
		Code int    `json:"code"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&p)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || p.Type != "https://czl.net/jmap/gateway/no-upstream" || p.Code != 40301 {
		t.Fatalf("session: %d %+v", resp.StatusCode, p)
	}

	// The admin password is never accepted for JMAP.
	bad := (&jmap.Client{SessionEndpoint: base + "/jmap/session"}).WithBasicAuth("owner@example.com", "correct horse battery")
	if r, err := bad.HttpClient.Get(base + "/jmap/session"); err != nil || r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("admin password: %v %v", r, err)
	}

	// An open EventSource stream ends when its app password is revoked.
	stream, err := c.HttpClient.Get(base + "/jmap/eventsource?types=*&closeafter=no&ping=0")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	if stream.StatusCode != 200 {
		t.Fatalf("eventsource: %d", stream.StatusCode)
	}
	ended := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(stream.Body)
		for sc.Scan() {
		}
		close(ended)
	}()
	adm.do("DELETE", "/api/admin/app-passwords/"+pw.ID, nil)
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("eventsource still open after revoking the app password")
	}
	if r, err := c.HttpClient.Get(base + "/jmap/session"); err != nil || r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("session after revoke: %v %v", r, err)
	}

	// Well-known files.
	r, err := http.Get(base + "/.well-known/czlmail.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(raw), `"adminUrl":"`+base+`/admin/"`) || !strings.Contains(string(raw), `"version":"2026.12.0"`) {
		t.Fatalf("czlmail.json: %s", raw)
	}
}
