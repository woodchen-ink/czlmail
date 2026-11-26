package router

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/woodchen-ink/czlmail/gateway/database"
	"github.com/woodchen-ink/czlmail/gateway/handler"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
	"github.com/woodchen-ink/czlmail/gateway/service"
)

func TestHealthAndNotFound(t *testing.T) {
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close(db)
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	h := New(Deps{
		Log:      log,
		ClientIP: func(r *http.Request) string { return r.RemoteAddr },
		Health:   &handler.Health{DB: db, Version: "2026.12.0", Log: log},
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	var body struct {
		Code int `json:"code"`
		Data struct {
			Status  string `json:"status"`
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || body.Data.Status != "ok" || body.Data.Version != "2026.12.0" {
		t.Fatalf("healthz: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("missing X-Request-Id")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown path: %d", rec.Code)
	}
}

func TestJMAPMounted(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close(db)
	clientIP := func(r *http.Request) string { return r.RemoteAddr }
	basic := service.NewBasicAuthenticator(db, service.NewRateLimiter(), service.NewInvalidations(), clientIP, log)
	j, err := jmapserver.New(jmapserver.Options{
		BaseURL: "https://gw.example.com", DataDir: t.TempDir(),
		Auth: basic, Accounts: service.NewUpstreamAccounts(db), Log: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	info := jmapserver.GatewayInfo{Version: "2026.12.0", ProtocolVersion: 1, AdminURL: "https://gw.example.com/admin/"}
	h := New(Deps{
		Log: log, ClientIP: func(r *http.Request) string { return r.RemoteAddr },
		JMAP: j, Czlmail: &handler.CzlmailConfig{Gateway: info},
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/.well-known/jmap", nil))
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "https://gw.example.com/jmap/session" {
		t.Fatalf("well-known jmap: %d %v", rec.Code, rec.Header())
	}
	for _, r := range []*http.Request{
		httptest.NewRequest("GET", "/jmap/session", nil),
		httptest.NewRequest("POST", "/jmap/api", nil),
		httptest.NewRequest("POST", "/jmap/upload/a1/", nil),
		httptest.NewRequest("GET", "/jmap/download/a1/u1/x", nil),
		httptest.NewRequest("GET", "/jmap/eventsource", nil),
	} {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s: %d", r.Method, r.URL.Path, rec.Code)
		}
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/.well-known/czlmail.json", nil))
	want := `{"version":1,"gateway":{"name":"czlmail 网关","adminUrl":"https://gw.example.com/admin/","protocolVersion":1,"version":"2026.12.0"}}` + "\n"
	if rec.Code != 200 || rec.Body.String() != want {
		t.Fatalf("czlmail.json: %d %s", rec.Code, rec.Body)
	}
}
