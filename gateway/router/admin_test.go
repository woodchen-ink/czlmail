package router

import (
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

	"github.com/woodchen-ink/czlmail/gateway/database"
	"github.com/woodchen-ink/czlmail/gateway/handler"
	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/service"
)

type apiResp struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func newTestGateway(t *testing.T, web ...http.Handler) (*httptest.Server, *service.Accounts, *service.BasicAuthenticator, func() []model.AuditLog) {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close(db) })
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sets, _ := service.LoadSettings(db, nil)
	lim := service.NewRateLimiter()
	inval := service.NewInvalidations()
	clientIP := func(r *http.Request) string { host, _, _ := strings.Cut(r.RemoteAddr, ":"); return host }
	acc := service.NewAccounts(db, sets, lim, inval, log)
	audit := service.NewAudit(db)
	basic := service.NewBasicAuthenticator(db, lim, inval, clientIP, log)
	srv := httptest.NewServer(New(Deps{
		Log: log, ClientIP: clientIP,
		Health: &handler.Health{DB: db, Version: "test", Log: log},
		Admin:  &handler.Admin{Accounts: acc, Settings: sets, Audit: audit, ClientIP: clientIP, Log: log},
		AdminWeb: func() http.Handler {
			if len(web) > 0 {
				return web[0]
			}
			return nil
		}(),
	}))
	t.Cleanup(srv.Close)
	audits := func() []model.AuditLog {
		var rows []model.AuditLog
		db.Order("id").Find(&rows)
		return rows
	}
	return srv, acc, basic, audits
}

type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func newClient(t *testing.T, base string) *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: t, base: base, http: &http.Client{Jar: jar}}
}

func (c *client) csrf() string {
	u, _ := url.Parse(c.base)
	for _, ck := range c.http.Jar.Cookies(u) {
		if ck.Name == "csrf_token" {
			return ck.Value
		}
	}
	return ""
}

func (c *client) do(method, path string, body any, withCSRF bool) (int, apiResp, http.Header) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if withCSRF {
		req.Header.Set("X-CSRF-Token", c.csrf())
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var r apiResp
	raw, _ := io.ReadAll(res.Body)
	if err := json.Unmarshal(raw, &r); err != nil {
		c.t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, raw)
	}
	return res.StatusCode, r, res.Header
}

func TestAdminFlow(t *testing.T) {
	srv, acc, basic, audits := newTestGateway(t)
	c := newClient(t, srv.URL)

	if _, r, _ := c.do("GET", "/api/admin/setup", nil, false); !strings.Contains(string(r.Data), `"required":true`) {
		t.Fatalf("setup status %s", r.Data)
	}
	if st, r, _ := c.do("GET", "/api/admin/me", nil, false); st != 401 || r.Code != 40100 {
		t.Fatalf("me before sign-in: %d %+v", st, r)
	}
	token, _ := acc.IssueSetupToken()
	setup := map[string]string{"token": token, "email": "Owner@Example.com", "password": "correct horse battery"}
	if st, r, _ := c.do("POST", "/api/admin/setup", setup, false); st != 200 || r.Code != 200 {
		t.Fatalf("setup: %d %+v", st, r)
	}
	if st, r, _ := c.do("POST", "/api/admin/setup", setup, false); st != 409 || r.Code != 40901 {
		t.Fatalf("setup again: %d %+v", st, r)
	}

	// Signed in by setup. Writes without the CSRF header are refused.
	if st, r, _ := c.do("POST", "/api/admin/app-passwords", map[string]string{"name": "Laptop"}, false); st != 403 || r.Code != 40300 {
		t.Fatalf("no csrf: %d %+v", st, r)
	}
	st, r, _ := c.do("POST", "/api/admin/app-passwords", map[string]string{"name": "Laptop"}, true)
	var created struct {
		ID, Prefix, Password string
	}
	json.Unmarshal(r.Data, &created)
	if st != 200 || !strings.HasPrefix(created.Password, created.Prefix) {
		t.Fatalf("create app password: %d %s", st, r.Data)
	}
	_, r, _ = c.do("GET", "/api/admin/app-passwords", nil, false)
	if strings.Contains(string(r.Data), created.Password) || !strings.Contains(string(r.Data), created.ID) {
		t.Fatalf("list shows the password or misses it: %s", r.Data)
	}

	req := httptest.NewRequest("GET", "/jmap/session", nil)
	req.SetBasicAuth("owner@example.com", created.Password)
	if _, err := basic.Authenticate(req); err != nil {
		t.Fatalf("basic with new app password: %v", err)
	}

	// Form posts are refused outright.
	res, _ := c.http.Post(srv.URL+"/api/admin/login", "application/x-www-form-urlencoded", strings.NewReader("email=a"))
	if res.StatusCode != 415 {
		t.Fatalf("form post: %d", res.StatusCode)
	}
	res.Body.Close()

	// Sign out, then wrong passwords until 429.
	if st, _, _ := c.do("POST", "/api/admin/logout", map[string]string{}, true); st != 200 {
		t.Fatalf("logout: %d", st)
	}
	if st, _, _ := c.do("GET", "/api/admin/me", nil, false); st != 401 {
		t.Fatalf("me after logout: %d", st)
	}
	var hdr http.Header
	for range service.LimitLoginUser.Burst + 1 {
		st, r, hdr = c.do("POST", "/api/admin/login", map[string]string{"email": "owner@example.com", "password": "wrong password"}, false)
	}
	if st != 429 || r.Code != 42900 || hdr.Get("Retry-After") == "" {
		t.Fatalf("rate limit: %d %+v %v", st, r, hdr)
	}

	// Every write was audited, with secrets masked.
	rows := audits()
	if len(rows) < 6 {
		t.Fatalf("audit rows: %d", len(rows))
	}
	for _, a := range rows {
		for _, secret := range []string{"correct horse battery", "wrong password", token, created.Password} {
			if strings.Contains(a.Params, secret) {
				t.Fatalf("audit %s %s leaks a secret: %s", a.Method, a.Path, a.Params)
			}
		}
	}
	first := rows[0]
	if first.Path != "/api/admin/setup" || first.ActorEmail != "owner@example.com" || first.Code != 200 ||
		!strings.Contains(first.Params, `"password":"***"`) || !strings.Contains(first.Params, `"token":"***"`) {
		t.Fatalf("setup audit %+v", first)
	}
	last := rows[len(rows)-1]
	if last.Status != 429 || last.Code != 42900 || last.ActorID != "" {
		t.Fatalf("rate-limited login audit %+v", last)
	}
}
