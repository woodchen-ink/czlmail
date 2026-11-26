// Package router wires URLs to handlers.
package router

import (
	"log/slog"
	"net/http"

	"github.com/woodchen-ink/go-web-utils/resputil"

	"github.com/woodchen-ink/czlmail/gateway/handler"
	"github.com/woodchen-ink/czlmail/gateway/middleware"
)

// Deps are the handlers the router mounts. Later work packages add the JMAP
// endpoints (/.well-known/jmap, /jmap/...) and the admin web pages (/admin/, /setup).
type Deps struct {
	Log      *slog.Logger
	ClientIP middleware.ClientIPFunc
	Health   *handler.Health
	Admin    *handler.Admin
}

// New returns the gateway's root handler.
func New(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", d.Health)
	if d.Admin != nil {
		mux.Handle("/api/admin/", adminAPI(d))
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

	handle("GET /api/admin/settings", a.ListSettings)
	handle("PATCH /api/admin/settings", a.UpdateSettings)
	handle("GET /api/admin/audit-logs", a.ListAudit)

	m.HandleFunc("/", notFound)
	return middleware.Chain(m,
		middleware.Audit(a.Audit, d.ClientIP, d.Log),
		middleware.RequireJSON,
	)
}

func notFound(w http.ResponseWriter, r *http.Request) {
	resputil.FailStatus(w, http.StatusNotFound, 40400, "not_found")
}
