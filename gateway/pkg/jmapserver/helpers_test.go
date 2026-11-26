package jmapserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"git.sr.ht/~rockorager/go-jmap"
	_ "git.sr.ht/~rockorager/go-jmap/core" // registers the core capability and Core/echo

	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
)

const testCap = "urn:test"

// basicAuth accepts name:pw<name> and returns "usr_<name>".
type basicAuth struct{}

func (basicAuth) Authenticate(r *http.Request) (string, error) {
	user, pass, ok := r.BasicAuth()
	if !ok || pass != "pw"+user {
		return "", errors.New("bad credentials")
	}
	return "usr_" + user, nil
}

// accounts is a mutable AccountSource.
type accounts struct {
	mu    sync.Mutex
	users map[string]*jmapserver.UserSession
}

func (a *accounts) UserSession(_ context.Context, userID string) (*jmapserver.UserSession, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if us, ok := a.users[userID]; ok {
		return us, nil
	}
	return &jmapserver.UserSession{Username: userID}, nil
}

func (a *accounts) set(userID string, us *jmapserver.UserSession) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.users[userID] = us
}

func upstream(status string) map[string]any {
	return map[string]any{
		jmapserver.UpstreamCapability: jmapserver.UpstreamInfo{
			Kind: "imap", Status: status, StatusSince: time.Date(2026, 11, 26, 8, 0, 0, 0, time.UTC),
		},
		testCap: struct{}{},
	}
}

func aliceSession(status string) *jmapserver.UserSession {
	return &jmapserver.UserSession{
		Username: "alice@example.com",
		Accounts: map[string]*jmapserver.Account{
			"a1": {Name: "alice@example.com", IsPersonal: true, AccountCapabilities: upstream(status)},
			"a2": {Name: "alice@gmail.com", IsPersonal: true, AccountCapabilities: upstream(jmapserver.UpstreamOK)},
		},
		PrimaryAccounts: map[string]string{testCap: "a1"},
	}
}

type env struct {
	t        *testing.T
	srv      *jmapserver.Server
	ts       *httptest.Server
	accounts *accounts
	dataDir  string
}

// newEnv serves a Server the way the gateway router mounts it, with user alice
// (two accounts) and user bob (none). Tests register their own methods.
func newEnv(t *testing.T, debounce time.Duration) *env {
	t.Helper()
	var handler http.Handler
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(ts.Close)

	acc := &accounts{users: map[string]*jmapserver.UserSession{"usr_alice": aliceSession(jmapserver.UpstreamOK)}}
	dir := t.TempDir()
	srv, err := jmapserver.New(jmapserver.Options{
		BaseURL: ts.URL, DataDir: dir, Auth: basicAuth{}, Accounts: acc,
		Log: slog.New(slog.NewJSONHandler(io.Discard, nil)), PushDebounce: debounce,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv.RegisterCapability(testCap, nil)
	t.Cleanup(srv.Close)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/jmap", srv.WellKnown)
	mux.HandleFunc("GET /jmap/session", srv.Session)
	mux.HandleFunc("POST /jmap/api", srv.API)
	mux.HandleFunc("POST /jmap/upload/{accountId}/{$}", srv.Upload)
	mux.HandleFunc("GET /jmap/download/{accountId}/{blobId}/{name}", srv.Download)
	mux.HandleFunc("GET /jmap/eventsource", srv.EventSource)
	handler = mux
	return &env{t: t, srv: srv, ts: ts, accounts: acc, dataDir: dir}
}

// client is a go-jmap client signed in as user, as czlmail uses it.
func (e *env) client(user string) *jmap.Client {
	c := &jmap.Client{SessionEndpoint: e.ts.URL + "/.well-known/jmap"}
	return c.WithBasicAuth(user, "pw"+user)
}

// post sends a raw JMAP request as alice and returns status, content type and body.
func (e *env) post(body string) (int, string, []byte) {
	e.t.Helper()
	req, _ := http.NewRequest("POST", e.ts.URL+"/jmap/api", bytes.NewBufferString(body))
	req.SetBasicAuth("alice", "pwalice")
	return e.do(req)
}

func (e *env) get(path, user string) (int, string, []byte) {
	e.t.Helper()
	req, _ := http.NewRequest("GET", e.ts.URL+path, nil)
	if user != "" {
		req.SetBasicAuth(user, "pw"+user)
	}
	return e.do(req)
}

func (e *env) do(req *http.Request) (int, string, []byte) {
	e.t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), b
}

// responses decodes methodResponses as [name, args, callId] triples.
func responses(t *testing.T, body []byte) []struct {
	Name   string
	Args   map[string]any
	CallID string
} {
	t.Helper()
	var r struct {
		MethodResponses [][3]json.RawMessage `json:"methodResponses"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	out := make([]struct {
		Name   string
		Args   map[string]any
		CallID string
	}, len(r.MethodResponses))
	for i, inv := range r.MethodResponses {
		_ = json.Unmarshal(inv[0], &out[i].Name)
		_ = json.Unmarshal(inv[1], &out[i].Args)
		_ = json.Unmarshal(inv[2], &out[i].CallID)
	}
	return out
}

func problemOf(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var p map[string]any
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode problem %s: %v", body, err)
	}
	return p
}
