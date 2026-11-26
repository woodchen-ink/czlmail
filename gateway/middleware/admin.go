package middleware

import (
	"context"
	"errors"
	"mime"
	"net/http"

	"github.com/woodchen-ink/go-web-utils/cookieutil"
	"github.com/woodchen-ink/go-web-utils/resputil"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/service"
)

// SessionCookie holds the admin session token (HttpOnly). The CSRF token is in
// cookieutil.DefaultCSRFCookie and must be echoed in the X-CSRF-Token header.
const SessionCookie = "gw_session"

const principalKey ctxKey = 1

// SessionResolver turns a session cookie value into the signed-in user.
type SessionResolver func(token string) (*service.AdminPrincipal, error)

// Principal returns the signed-in admin user, set by RequireSession.
func Principal(ctx context.Context) *service.AdminPrincipal {
	p, _ := ctx.Value(principalKey).(*service.AdminPrincipal)
	return p
}

// RequireSession answers 401 without a valid session cookie, and checks the CSRF
// double-submit token on every request that is not GET or HEAD.
func RequireSession(resolve SessionResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := ""
			if c, err := r.Cookie(SessionCookie); err == nil {
				token = c.Value
			}
			p, err := resolve(token)
			if errors.Is(err, service.ErrUnauthenticated) {
				resputil.FailStatus(w, http.StatusUnauthorized, 40100, "unauthenticated")
				return
			}
			if err != nil {
				resputil.FailStatus(w, http.StatusInternalServerError, 50000, "internal_error")
				return
			}
			SetAuditActor(r.Context(), &p.User)
			if !isSafeMethod(r.Method) && !cookieutil.ValidateCSRF(r) {
				resputil.FailStatus(w, http.StatusForbidden, 40300, "csrf_failed")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, p)))
		})
	}
}

// RequireJSON rejects write requests whose body is not application/json. Browsers
// cannot send that cross-site without a CORS preflight, which the gateway never allows,
// so sign-in and setup are safe from login CSRF without a token.
func RequireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isSafeMethod(r.Method) && r.ContentLength != 0 {
			mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mt != "application/json" {
				resputil.FailStatus(w, http.StatusUnsupportedMediaType, 41500, "unsupported_media_type")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// actorHolder lets the session check, or a sign-in handler, tell Audit who acted.
type actorHolder struct{ user *model.User }

const actorKey ctxKey = 2

// SetAuditActor names the user the audit record of this request belongs to.
func SetAuditActor(ctx context.Context, u *model.User) {
	if h, ok := ctx.Value(actorKey).(*actorHolder); ok {
		h.user = u
	}
}
