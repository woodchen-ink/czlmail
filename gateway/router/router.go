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
// endpoints (/.well-known/jmap, /jmap/...) and the admin (/admin/, /api/admin/...).
type Deps struct {
	Log      *slog.Logger
	ClientIP middleware.ClientIPFunc
	Health   *handler.Health
}

// New returns the gateway's root handler.
func New(d Deps) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /healthz", d.Health)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		resputil.FailStatus(w, http.StatusNotFound, 40400, "not_found")
	})
	return middleware.Chain(mux,
		middleware.Logging(d.Log, d.ClientIP),
		middleware.Recover(d.Log),
	)
}
