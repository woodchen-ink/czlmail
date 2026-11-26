package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/woodchen-ink/go-web-utils/cookieutil"

	"github.com/woodchen-ink/czlmail/gateway/cron"
	"github.com/woodchen-ink/czlmail/gateway/middleware"
	"github.com/woodchen-ink/czlmail/gateway/service"
	"github.com/woodchen-ink/czlmail/gateway/service/submission"
)

// Admin serves /api/admin/: setup, sign-in, the signed-in user, app passwords,
// users, settings, the audit log, the overview and the system pages.
type Admin struct {
	Accounts      *service.Accounts
	Settings      *service.Settings
	Audit         *service.Audit
	Upstreams     *service.Upstreams
	OverviewSvc   *service.Overview
	SystemSvc     *service.System
	Tasks         *cron.Scheduler
	Submission    *submission.Service
	ClientIP      func(*http.Request) string
	SecureCookies bool
	Log           *slog.Logger
}

// decodeJSON reads a JSON object; unknown fields are an error, so typos do not pass silently.
func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errInvalidRequest
	}
	if dec.More() {
		return errInvalidRequest
	}
	return nil
}

func (h *Admin) cookieOptions() cookieutil.Options {
	return cookieutil.Options{Secure: h.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: service.SessionMaxAge}
}

// signIn sets the session cookie and a fresh CSRF token.
func (h *Admin) signIn(w http.ResponseWriter, token string) error {
	csrf, err := cookieutil.NewCSRFToken()
	if err != nil {
		return err
	}
	opts := h.cookieOptions()
	cookieutil.SetAuth(w, middleware.SessionCookie, token, opts)
	cookieutil.SetCSRF(w, csrf, opts)
	return nil
}

func (h *Admin) signOut(w http.ResponseWriter) {
	opts := h.cookieOptions()
	cookieutil.Clear(w, middleware.SessionCookie, opts)
	http.SetCookie(w, &http.Cookie{Name: cookieutil.DefaultCSRFCookie, Path: "/", MaxAge: -1, Secure: opts.Secure, SameSite: opts.SameSite})
}

// ensureCSRF re-issues the CSRF cookie when the browser lost it but still has a session.
func (h *Admin) ensureCSRF(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieutil.DefaultCSRFCookie); err == nil && c.Value != "" {
		return
	}
	if csrf, err := cookieutil.NewCSRFToken(); err == nil {
		cookieutil.SetCSRF(w, csrf, h.cookieOptions())
	}
}

var errInvalidRequest = errors.New("invalid request body")

func rfc3339(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}
