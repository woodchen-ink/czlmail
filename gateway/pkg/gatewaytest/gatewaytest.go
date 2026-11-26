// Package gatewaytest runs the whole gateway in-process behind httptest and hands
// tests a go-jmap client, the same JMAP client czlmail uses. Test code only.
//
// A protocol test reads:
//
//	gw := gatewaytest.Start(t)
//	cred := gw.NewUser(t)                                       // user + app password
//	gw.AddIMAPUpstream(t, cred, imaptest.Dovecot(t).NewUser(t)) // (#39)
//	c := gw.Client(t, cred)                                     // authenticated go-jmap client
//
// The gateway is built with initapp.NewApp, as Serve builds it, and users go through
// the real admin API: owner setup link, user creation, app password. Upstream
// registration goes through the admin API too (Gmail: the full OAuth flow, see
// UseFakeGoogle).
package gatewaytest

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"git.sr.ht/~rockorager/go-jmap"
	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/config"
	"github.com/woodchen-ink/czlmail/gateway/database"
	"github.com/woodchen-ink/czlmail/gateway/initapp"
)

// Owner credentials of every test gateway (admin login, not JMAP).
const (
	OwnerEmail    = "owner@gatewaytest.invalid"
	OwnerPassword = "correct horse battery staple"
)

// Gateway is one running gateway with its own data directory.
type Gateway struct {
	*httptest.Server
	App     *initapp.App
	DataDir string
	DB      *gorm.DB

	mu    sync.Mutex
	owner *Admin            // signed in on first NewUser
	users map[string]*Admin // by email, see UserAdmin
}

// Start runs a gateway on a fresh data directory; it stops when the test ends.
// Its logs are printed only if the test fails.
func Start(t testing.TB) *Gateway {
	t.Helper()
	return StartWith(t, initapp.Options{})
}

// StartWith is Start with app options (shorter waits for sending tests).
func StartWith(t testing.TB, opts initapp.Options) *Gateway {
	t.Helper()
	// Cleanups run in reverse: App.Close (background work, EventSource streams),
	// then the HTTP server (waits for requests in flight), then the database, and
	// only then is the data directory removed.
	g := &Gateway{DataDir: t.TempDir()}
	db, err := database.Open(g.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close(db) })
	g.DB = db
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.App.Handler.ServeHTTP(w, r)
	}))
	t.Cleanup(g.Close)

	key := make([]byte, config.MasterKeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	logs := &lockedBuffer{}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("gateway logs:\n%s", logs.String())
		}
	})
	cfg := &config.Config{MasterKey: key, BaseURL: g.URL, DataDir: g.DataDir}
	g.App, err = initapp.NewAppWith(cfg, db, slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})), "test", opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.App.Close)
	return g
}

// SessionURL is the JMAP session endpoint czlmail would be given.
func (g *Gateway) SessionURL() string { return g.URL + "/.well-known/jmap" }

// Credentials sign a JMAP client in: the gateway user and one of its app passwords.
type Credentials struct {
	Email       string
	Password    string // admin password, for Login
	AppPassword string
}

// NewUser creates a gateway user through the admin API and gives it an app password.
// The first call also sets up the owner from a setup token.
func (g *Gateway) NewUser(t testing.TB) Credentials {
	t.Helper()
	owner := g.Owner(t)
	email := "u" + randHex(6) + "@gatewaytest.invalid"
	pass := "pw-" + randHex(12)
	owner.Do(t, "POST", "/api/admin/users", map[string]string{"email": email, "name": "Test User", "password": pass}, nil)
	user := g.Login(t, email, pass)
	var ap struct{ Password string }
	user.Do(t, "POST", "/api/admin/app-passwords", map[string]string{"name": "gatewaytest"}, &ap)
	return Credentials{Email: email, Password: pass, AppPassword: ap.Password}
}

// Owner returns the owner's admin client, running owner setup on first use.
func (g *Gateway) Owner(t testing.TB) *Admin {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.owner != nil {
		return g.owner
	}
	token, err := g.App.Auth.Accounts.IssueSetupToken()
	if err != nil {
		t.Fatalf("gatewaytest: setup token: %v", err)
	}
	a := g.newAdmin()
	a.Do(t, "POST", "/api/admin/setup", map[string]string{
		"token": token, "email": OwnerEmail, "name": "Owner", "password": OwnerPassword,
	}, nil)
	g.owner = a
	return a
}

// Login signs in to the admin API with an email and admin password.
func (g *Gateway) Login(t testing.TB, email, password string) *Admin {
	t.Helper()
	a := g.newAdmin()
	a.Do(t, "POST", "/api/admin/login", map[string]string{"email": email, "password": password}, nil)
	return a
}

func (g *Gateway) newAdmin() *Admin {
	jar, _ := cookiejar.New(nil)
	return &Admin{base: g.URL, http: &http.Client{Jar: jar}}
}

// Admin is a signed-in admin API client (cookie session + CSRF header).
type Admin struct {
	base string
	http *http.Client
}

// Do calls the admin API, fails the test unless it answers code 200, and decodes
// data into out when out is non-nil.
func (a *Admin) Do(t testing.TB, method, path string, body, out any) {
	t.Helper()
	code, raw := a.Call(t, method, path, body, out)
	if code != 200 {
		t.Fatalf("gatewaytest: %s %s: %s", method, path, raw)
	}
}

// Call is Do that returns the response code instead of failing on errors.
func (a *Admin) Call(t testing.TB, method, path string, body, out any) (int, string) {
	t.Helper()
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
		t.Fatalf("gatewaytest: %s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	var r struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &r) != nil {
		t.Fatalf("gatewaytest: %s %s: %d %s", method, path, res.StatusCode, raw)
	}
	if r.Code == 200 && out != nil {
		if err := json.Unmarshal(r.Data, out); err != nil {
			t.Fatalf("gatewaytest: %s %s: decode %s: %v", method, path, r.Data, err)
		}
	}
	return r.Code, string(raw)
}

// Client returns a go-jmap client signed in as cred with the Session fetched.
func (g *Gateway) Client(t testing.TB, cred Credentials) *jmap.Client {
	t.Helper()
	c := g.unauthenticated(cred)
	if err := c.Authenticate(); err != nil {
		st, p := g.SessionProblem(t, cred)
		t.Fatalf("gatewaytest: JMAP session: %v (HTTP %d, %+v)", err, st, p)
	}
	return c
}

func (g *Gateway) unauthenticated(cred Credentials) *jmap.Client {
	return (&jmap.Client{SessionEndpoint: g.SessionURL()}).WithBasicAuth(cred.Email, cred.AppPassword)
}

// Problem is an RFC 7807 problem document as the gateway returns it.
type Problem struct {
	Type   string `json:"type"`
	Status int    `json:"status"`
	Code   int    `json:"code"`
	Detail string `json:"detail"`
}

// Problem types the gateway uses.
const ProblemNoUpstream = "https://czl.net/jmap/gateway/no-upstream"

// SessionProblem fetches the Session with cred and returns the HTTP status and the
// problem document, if any (zero Problem on 200).
func (g *Gateway) SessionProblem(t testing.TB, cred Credentials) (int, Problem) {
	t.Helper()
	resp, err := g.unauthenticated(cred).HttpClient.Get(g.SessionURL())
	if err != nil {
		t.Fatalf("gatewaytest: GET session: %v", err)
	}
	defer resp.Body.Close()
	var p Problem
	if resp.StatusCode != http.StatusOK {
		_ = json.NewDecoder(resp.Body).Decode(&p)
	}
	return resp.StatusCode, p
}

// Do sends one JMAP request and fails the test on transport or request-level errors.
func Do(t testing.TB, c *jmap.Client, req *jmap.Request) *jmap.Response {
	t.Helper()
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("gatewaytest: JMAP request: %v", err)
	}
	return resp
}

// lockedBuffer collects logs from the gateway's goroutines.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
