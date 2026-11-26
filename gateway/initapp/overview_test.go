package initapp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
	"time"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/gateway/service"
)

type fakeUpstreams struct{ owner map[string]string } // account id → owner public id

func (f fakeUpstreams) ListUpstreams(_ context.Context, owner string) ([]service.UpstreamCard, error) {
	var out []service.UpstreamCard
	for id, o := range f.owner {
		if owner != "" && owner != o {
			continue
		}
		status := service.UpstreamOK
		if id == "m2" {
			status = service.UpstreamAuthRequired
		}
		out = append(out, service.UpstreamCard{ID: id, Kind: "imap", Name: id, Status: status})
	}
	return out, nil
}

type fakeSecrets struct{ at time.Time }

func (f fakeSecrets) OAuthSecrets(context.Context) ([]service.OAuthSecret, error) {
	return []service.OAuthSecret{{Provider: "microsoft", ExpiresAt: f.at}}, nil
}

type overviewResp struct {
	Scope         string                 `json:"scope"`
	Upstreams     []service.UpstreamCard `json:"upstreams"`
	StatusCounts  map[string]int         `json:"statusCounts"`
	OnlineClients int                    `json:"onlineClients"`
	Todos         []service.Todo         `json:"todos"`
	Trend         struct {
		Range   string                       `json:"range"`
		Total   []struct{ Requests int64 }   `json:"total"`
		Series  []struct{ AccountID string } `json:"series"`
		Summary struct {
			Requests    int64  `json:"requests"`
			RateLimited int64  `json:"rateLimited"`
			PushP95Ms   *int64 `json:"pushP95Ms"`
		} `json:"summary"`
	} `json:"trend"`
}

func todoKinds(todos []service.Todo) string {
	var k []string
	for _, t := range todos {
		k = append(k, t.Kind)
	}
	return strings.Join(k, ",")
}

func TestOverviewAndSystem(t *testing.T) {
	app, base := newGateway(t)
	token, _ := app.Auth.Accounts.IssueSetupToken()
	jar, _ := cookiejar.New(nil)
	owner := &admin{t: t, base: base, http: &http.Client{Jar: jar}}
	var me struct{ ID string }
	_ = json.Unmarshal(owner.do("POST", "/api/admin/setup", map[string]string{
		"token": token, "email": "owner@example.com", "password": "correct horse battery",
	}), &me)

	// A plain user with an app password and one mailbox of their own.
	owner.do("POST", "/api/admin/users", map[string]string{"email": "u@example.com", "password": "another long secret", "role": "user"})
	var users []struct{ ID, Email string }
	_ = json.Unmarshal(owner.do("GET", "/api/admin/users", nil), &users)
	userID := ""
	for _, u := range users {
		if u.Email == "u@example.com" {
			userID = u.ID
		}
	}
	app.Overview.Upstreams = fakeUpstreams{owner: map[string]string{"m1": me.ID, "m2": me.ID, "m3": userID}}
	app.Overview.Secrets = fakeSecrets{at: time.Now().Add(10 * 24 * time.Hour)}

	for range 5 {
		app.Metrics.UpstreamRequest("m1")
	}
	app.Metrics.RateLimited("m1")
	app.Metrics.PushLatency("m1", 300*time.Millisecond)
	app.Metrics.UpstreamRequest("m3")

	var ov overviewResp
	_ = json.Unmarshal(owner.do("GET", "/api/admin/overview", nil), &ov)
	if ov.Scope != "all" || len(ov.Upstreams) != 3 || ov.StatusCounts["ok"] != 2 || ov.StatusCounts["authRequired"] != 1 {
		t.Fatalf("admin overview: %+v", ov)
	}
	if got := todoKinds(ov.Todos); got != "reauth,oauthSecretExpiring,masterKeyBackup" {
		t.Fatalf("todos = %s", got)
	}
	if ov.Trend.Range != "24h" || len(ov.Trend.Total) != 24 || ov.Trend.Summary.Requests != 6 ||
		ov.Trend.Summary.RateLimited != 1 || ov.Trend.Summary.PushP95Ms == nil || len(ov.Trend.Series) != 2 {
		t.Fatalf("trend: %+v", ov.Trend)
	}
	_ = json.Unmarshal(owner.do("GET", "/api/admin/overview?scope=mine&range=7d", nil), &ov)
	if ov.Scope != "mine" || len(ov.Upstreams) != 2 || len(ov.Trend.Total) != 7*24 || ov.Trend.Summary.Requests != 5 {
		t.Fatalf("mine 7d: scope %s, %d upstreams, %d points, %d requests", ov.Scope, len(ov.Upstreams), len(ov.Trend.Total), ov.Trend.Summary.Requests)
	}

	// Confirming the master key backup clears that to-do.
	owner.do("POST", "/api/admin/system/master-key-backup", map[string]string{})
	_ = json.Unmarshal(owner.do("GET", "/api/admin/overview", nil), &ov)
	if strings.Contains(todoKinds(ov.Todos), "masterKeyBackup") {
		t.Fatalf("backup to-do still there: %s", todoKinds(ov.Todos))
	}

	// System page and task center.
	var sys struct {
		Version   string `json:"version"`
		Database  struct{ OK bool }
		MasterKey struct {
			Version           uint32     `json:"version"`
			BackupConfirmedAt *time.Time `json:"backupConfirmedAt"`
		} `json:"masterKey"`
		Env []struct{ Name, Value string }
	}
	_ = json.Unmarshal(owner.do("GET", "/api/admin/system", nil), &sys)
	if sys.Version != "2026.12.0" || !sys.Database.OK || sys.MasterKey.Version != 1 || sys.MasterKey.BackupConfirmedAt == nil {
		t.Fatalf("system: %+v", sys)
	}
	for _, e := range sys.Env {
		if e.Name == "GATEWAY_MASTER_KEY" && e.Value != "set" {
			t.Fatalf("master key shown: %q", e.Value)
		}
	}
	var run struct {
		OK     bool   `json:"ok"`
		Manual bool   `json:"manual"`
		Result string `json:"result"`
	}
	_ = json.Unmarshal(owner.do("POST", "/api/admin/system/tasks/metrics_flush/run", map[string]string{}), &run)
	if !run.OK || !run.Manual {
		t.Fatalf("run: %+v", run)
	}
	var tasks []struct {
		Name    string
		LastRun *struct{ OK bool } `json:"lastRun"`
	}
	_ = json.Unmarshal(owner.do("GET", "/api/admin/system/tasks", nil), &tasks)
	flushed := false
	for _, task := range tasks {
		flushed = flushed || (task.Name == "metrics_flush" && task.LastRun != nil)
	}
	if !flushed {
		t.Fatalf("tasks: %+v", tasks)
	}

	// The plain user sees only their own mailbox and none of the admin to-dos.
	ujar, _ := cookiejar.New(nil)
	user := &admin{t: t, base: base, http: &http.Client{Jar: ujar}}
	user.do("POST", "/api/admin/login", map[string]string{"email": "u@example.com", "password": "another long secret"})
	pw := struct{ Password string }{}
	_ = json.Unmarshal(user.do("POST", "/api/admin/app-passwords", map[string]string{"name": "Phone"}), &pw)

	// One EventSource stream: online for the user. It needs a mailbox to open, so it
	// only counts when the JMAP layer accepts it; until then the count is 0.
	c := (&jmap.Client{SessionEndpoint: base + "/jmap/session"}).WithBasicAuth("u@example.com", pw.Password)
	stream, err := c.HttpClient.Get(base + "/jmap/eventsource?types=*&closeafter=no&ping=0")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	wantOnline := 0
	if stream.StatusCode == http.StatusOK {
		wantOnline = 1
	}

	_ = json.Unmarshal(user.do("GET", "/api/admin/overview", nil), &ov)
	if ov.Scope != "mine" || len(ov.Upstreams) != 1 || ov.Upstreams[0].ID != "m3" || len(ov.Todos) != 0 ||
		ov.Trend.Summary.Requests != 1 || ov.OnlineClients != wantOnline {
		t.Fatalf("user overview: %+v (online want %d)", ov, wantOnline)
	}
	for _, path := range []string{"/api/admin/overview?scope=all", "/api/admin/system", "/api/admin/system/tasks"} {
		if st := user.status("GET", path); st != http.StatusForbidden {
			t.Fatalf("%s as user: %d", path, st)
		}
	}
	if st := owner.status("GET", "/api/admin/overview?range=1y"); st != http.StatusBadRequest {
		t.Fatalf("bad range: %d", st)
	}
}

// status sends a request and returns only the HTTP status.
func (a *admin) status(method, path string) int {
	a.t.Helper()
	req, _ := http.NewRequest(method, a.base+path, nil)
	res, err := a.http.Do(req)
	if err != nil {
		a.t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

// The overview lists real upstreams (service.Upstreams) with their live status.
func TestOverviewRealUpstreams(t *testing.T) {
	app, base := newGateway(t)
	token, _ := app.Auth.Accounts.IssueSetupToken()
	jar, _ := cookiejar.New(nil)
	owner := &admin{t: t, base: base, http: &http.Client{Jar: jar}}
	owner.do("POST", "/api/admin/setup", map[string]string{"token": token, "email": "owner@example.com", "password": "correct horse battery"})
	owner.do("PATCH", "/api/admin/settings", map[string]any{"security.allow_private_networks": true})
	// Nothing listens on port 1: the upstream ends up unreachable.
	var up struct{ ID string }
	_ = json.Unmarshal(owner.do("POST", "/api/admin/upstreams", map[string]any{
		"name": "Local", "email": "me@example.com",
		"imap":  map[string]any{"host": "127.0.0.1", "port": 1, "tls": "plain", "username": "me", "password": "secret"},
		"proxy": map[string]any{"mode": "none"},
	}), &up)

	var ov overviewResp
	deadline := time.Now().Add(10 * time.Second)
	for {
		_ = json.Unmarshal(owner.do("GET", "/api/admin/overview", nil), &ov)
		if len(ov.Upstreams) == 1 && ov.Upstreams[0].Status == service.UpstreamUnreachable {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("overview upstreams: %+v", ov.Upstreams)
		}
		time.Sleep(50 * time.Millisecond)
	}
	c := ov.Upstreams[0]
	if c.ID != up.ID || c.Name != "Local" || c.OwnerEmail != "owner@example.com" || c.Proxy != nil ||
		c.LastError == nil || c.LastError.Code != 50210 || c.LastError.Msg != "unreachable" {
		t.Fatalf("card: %+v", c)
	}

	// Removing the upstream removes its card.
	owner.do("DELETE", "/api/admin/upstreams/"+up.ID, nil)
	_ = json.Unmarshal(owner.do("GET", "/api/admin/overview", nil), &ov)
	if len(ov.Upstreams) != 0 {
		t.Fatalf("after delete: %+v", ov.Upstreams)
	}
}
