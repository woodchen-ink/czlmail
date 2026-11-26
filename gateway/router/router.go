// Package router wires URLs to handlers.
package router

import (
	"log/slog"
	"net/http"

	"github.com/woodchen-ink/go-web-utils/resputil"

	"github.com/woodchen-ink/czlmail/gateway/handler"
	"github.com/woodchen-ink/czlmail/gateway/middleware"
	"github.com/woodchen-ink/czlmail/gateway/pkg/jmapserver"
)

// Deps are the handlers the router mounts.
type Deps struct {
	Log      *slog.Logger
	ClientIP middleware.ClientIPFunc
	Health   *handler.Health
	Admin    *handler.Admin
	// JMAP serves /.well-known/jmap and /jmap/...; nil leaves them unmounted.
	JMAP *jmapserver.Server
	// Czlmail serves /.well-known/czlmail.json; nil leaves it unmounted.
	Czlmail http.Handler
	// AdminWeb serves the admin pages under /admin/ (handler.NewAdminWeb); nil leaves
	// them unmounted.
	AdminWeb http.Handler
}

// New returns the gateway's root handler.
func New(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", d.Health)
	if d.Admin != nil {
		mux.Handle("/api/admin/", adminAPI(d))
		if d.Admin.OAuth != nil {
			mux.HandleFunc("GET /oauth/{provider}/callback", d.Admin.OAuthCallback)
		}
	}
	if d.Czlmail != nil {
		mux.Handle("GET /.well-known/czlmail.json", d.Czlmail)
	}
	if j := d.JMAP; j != nil {
		mux.HandleFunc("GET /.well-known/jmap", j.WellKnown)
		mux.HandleFunc("GET /jmap/session", j.Session)
		mux.HandleFunc("POST /jmap/api", j.API)
		mux.HandleFunc("POST /jmap/upload/{accountId}/{$}", j.Upload)
		mux.HandleFunc("POST /jmap/upload/{accountId}", j.Upload)
		mux.HandleFunc("GET /jmap/download/{accountId}/{blobId}/{name}", j.Download)
		mux.HandleFunc("GET /jmap/eventsource", j.EventSource)
	}
	if d.AdminWeb != nil {
		// After /api/: the API answers JSON 404 itself and never falls through to pages.
		mux.Handle(handler.AdminWebPrefix+"/", d.AdminWeb)
		mux.Handle(handler.AdminWebPrefix, d.AdminWeb)
		mux.HandleFunc("GET /setup", handler.LegacySetup)
		mux.HandleFunc("GET /{$}", handler.AdminHome)
	}
	mux.HandleFunc("/", notFound)
	return middleware.Chain(mux,
		middleware.Logging(d.Log, d.ClientIP),
		middleware.Recover(d.Log),
	)
}

// adminAPI is /api/admin/. Every write is audited and must be JSON; everything except
// setup, sign-in and registration needs a session, and session writes need the CSRF token.
func adminAPI(d Deps) http.Handler {
	a := d.Admin
	session := middleware.RequireSession(a.Accounts.ResolveSession)
	m := http.NewServeMux()
	handle := func(pattern string, h http.HandlerFunc) { m.Handle(pattern, session(h)) }

	m.HandleFunc("GET /api/admin/setup", a.SetupStatus)
	m.HandleFunc("POST /api/admin/setup", a.Setup)
	m.HandleFunc("POST /api/admin/login", a.Login)
	m.HandleFunc("POST /api/admin/register", a.Register)

	handle("POST /api/admin/logout", a.Logout)
	handle("GET /api/admin/me", a.Me)
	handle("PUT /api/admin/me/password", a.ChangePassword)

	handle("GET /api/admin/app-passwords", a.ListAppPasswords)
	handle("POST /api/admin/app-passwords", a.CreateAppPassword)
	handle("DELETE /api/admin/app-passwords/{id}", a.RevokeAppPassword)

	handle("GET /api/admin/users", a.ListUsers)
	handle("POST /api/admin/users", a.CreateUser)
	handle("GET /api/admin/users/{id}", a.GetUser)
	handle("PATCH /api/admin/users/{id}", a.UpdateUser)
	handle("DELETE /api/admin/users/{id}", a.DeleteUser)

	if a.Upstreams != nil {
		handle("GET /api/admin/upstreams", a.ListUpstreams)
		handle("POST /api/admin/upstreams", a.CreateUpstream)
		handle("POST /api/admin/upstreams/test", a.TestUpstream)
		handle("GET /api/admin/upstreams/{id}", a.GetUpstream)
		handle("PATCH /api/admin/upstreams/{id}", a.UpdateUpstream)
		handle("DELETE /api/admin/upstreams/{id}", a.DeleteUpstream)
		handle("POST /api/admin/upstreams/{id}/test", a.TestStoredUpstream)
		handle("POST /api/admin/upstreams/{id}/pause", a.PauseUpstream)
		handle("POST /api/admin/upstreams/{id}/resume", a.ResumeUpstream)
		handle("POST /api/admin/upstreams/{id}/default", a.DefaultUpstream)
	}
	if a.Submission != nil {
		handle("GET /api/admin/upstreams/{id}/identities", a.ListIdentities)
		handle("POST /api/admin/upstreams/{id}/identities", a.CreateIdentity)
		handle("PATCH /api/admin/upstreams/{id}/identities/{identityId}", a.UpdateIdentity)
		handle("DELETE /api/admin/upstreams/{id}/identities/{identityId}", a.DeleteIdentity)
		handle("POST /api/admin/upstreams/{id}/send-test", a.SendTest)
	}
	if a.OAuth != nil {
		handle("GET /api/admin/oauth-apps", a.ListOAuthApps)
		handle("PUT /api/admin/oauth-apps/{provider}", a.PutOAuthApp)
		handle("DELETE /api/admin/oauth-apps/{provider}", a.DeleteOAuthApp)
		handle("POST /api/admin/upstreams/oauth/{provider}/start", a.StartOAuthUpstream)
		handle("POST /api/admin/upstreams/{id}/reauth", a.ReauthUpstream)
	}

	handle("GET /api/admin/settings", a.ListSettings)
	handle("PATCH /api/admin/settings", a.UpdateSettings)
	handle("GET /api/admin/audit-logs", a.ListAudit)

	handle("GET /api/admin/overview", a.Overview)
	handle("GET /api/admin/system", a.System)
	handle("POST /api/admin/system/master-key-backup", a.ConfirmMasterKeyBackup)
	handle("GET /api/admin/system/tasks", a.ListTasks)
	handle("POST /api/admin/system/tasks/{name}/run", a.RunTask)

	m.HandleFunc("/", notFound)
	return middleware.Chain(m,
		middleware.Audit(a.Audit, d.ClientIP, d.Log),
		middleware.RequireJSON,
	)
}

func notFound(w http.ResponseWriter, r *http.Request) {
	resputil.FailStatus(w, http.StatusNotFound, 40400, "not_found")
}
