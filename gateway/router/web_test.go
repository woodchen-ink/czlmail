package router

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woodchen-ink/czlmail/gateway/handler"
)

func TestAdminWeb(t *testing.T) {
	root := t.TempDir()
	write := func(p, body string) {
		full := filepath.Join(root, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(body), 0o644)
	}
	write("index.html", "overview")
	write("mailboxes/index.html", "mailboxes")
	write("404.html", "missing")
	write("_next/static/app.js", "js")
	web, ok := handler.NewAdminWeb(root)
	if !ok {
		t.Fatal("web not installed")
	}
	srv, _, _, _ := newTestGateway(t, web)
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(p string) (int, string, string) {
		res, err := noFollow.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b), res.Header.Get("Location")
	}
	cases := []struct {
		path, body, loc string
		status          int
	}{
		{"/admin/", "overview", "", 200},
		{"/admin/mailboxes/", "mailboxes", "", 200},
		{"/admin", "", "/admin/", 308},
		{"/admin/mailboxes?x=1", "", "/admin/mailboxes/?x=1", 308},
		{"/admin/_next/static/app.js", "js", "", 200},
		{"/admin/nope/", "missing", "", 404},
		{"/setup?token=abc", "", "/admin/setup/?token=abc", 302},
		{"/", "", "/admin/", 302},
	}
	for _, c := range cases {
		st, body, loc := get(c.path)
		if st != c.status || (c.body != "" && body != c.body) || loc != c.loc {
			t.Errorf("%s: %d %q %q", c.path, st, body, loc)
		}
	}
	// The API never falls through to pages.
	if st, body, _ := get("/api/admin/nope"); st != 404 || !strings.Contains(body, "40400") {
		t.Errorf("api 404: %d %s", st, body)
	}

	missing, ok := handler.NewAdminWeb(filepath.Join(root, "absent"))
	if ok {
		t.Fatal("absent dir reported installed")
	}
	rec := httptest.NewRecorder()
	missing.ServeHTTP(rec, httptest.NewRequest("GET", "/admin/", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("absent: %d", rec.Code)
	}
}
