package handler

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/woodchen-ink/go-web-utils/resputil"

	"github.com/woodchen-ink/czlmail/gateway/middleware"
	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/service"
)

var errInvalidExpiry = errors.New("invalid secret expiry")

// ListOAuthApps: GET /api/admin/oauth-apps (site admins).
func (h *Admin) ListOAuthApps(w http.ResponseWriter, r *http.Request) {
	list, err := h.OAuth.ListApps(&actor(r).User)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, list)
}

type oauthAppInput struct {
	ClientID        string  `json:"clientId"`
	ClientSecret    *string `json:"clientSecret"`
	Tenant          string  `json:"tenant"`
	SecretExpiresAt *string `json:"secretExpiresAt"` // YYYY-MM-DD or RFC 3339; null/"" clears
}

// PutOAuthApp: PUT /api/admin/oauth-apps/{provider}. An empty or missing
// clientSecret keeps the stored one.
func (h *Admin) PutOAuthApp(w http.ResponseWriter, r *http.Request) {
	var in oauthAppInput
	if err := decodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	svc := service.OAuthAppInput{ClientID: in.ClientID, ClientSecret: in.ClientSecret, Tenant: in.Tenant}
	if s := in.SecretExpiresAt; s != nil && *s != "" {
		t, err := parseDate(*s)
		if err != nil {
			h.fail(w, r, errInvalidExpiry)
			return
		}
		svc.SecretExpiresAt = &t
	}
	if len(in.Tenant) > 64 || strings.ContainsAny(in.Tenant, " /\\?#") {
		h.fail(w, r, errInvalidRequest)
		return
	}
	v, err := h.OAuth.PutApp(&actor(r).User, r.PathValue("provider"), svc)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, v)
}

func parseDate(s string) (time.Time, error) {
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	return t.UTC(), err
}

// DeleteOAuthApp: DELETE /api/admin/oauth-apps/{provider}.
func (h *Admin) DeleteOAuthApp(w http.ResponseWriter, r *http.Request) {
	if err := h.OAuth.DeleteApp(&actor(r).User, r.PathValue("provider")); err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, struct{}{})
}

type oauthStartInput struct {
	User  string      `json:"user"`  // admins: whose upstream
	Name  string      `json:"name"`  // account name; default the address
	Email string      `json:"email"` // login hint
	Proxy *proxyInput `json:"proxy"`
}

type oauthURL struct {
	URL string `json:"url"`
}

// StartOAuthUpstream: POST /api/admin/upstreams/oauth/{provider}/start → {url},
// the provider's authorisation page. The browser comes back to
// /oauth/{provider}/callback, which redirects to the admin's mailboxes page.
func (h *Admin) StartOAuthUpstream(w http.ResponseWriter, r *http.Request) {
	var in oauthStartInput
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &in); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	start := service.OAuthStart{Owner: in.User}
	if in.Email != "" {
		email, err := validEmail(in.Email)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		start.LoginHint = email
	}
	if in.Name != "" {
		name, err := validName(in.Name, start.LoginHint)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		start.Name = name
	}
	if in.Proxy != nil {
		p, err := validProxy(in.Proxy)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		start.Proxy = p
	}
	u, err := h.OAuth.Start(actor(r), r.PathValue("provider"), start)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, oauthURL{URL: u})
}

// ReauthUpstream: POST /api/admin/upstreams/{id}/reauth → {url} for OAuth
// upstreams. Password upstreams are re-authorised with PATCH (new password).
func (h *Admin) ReauthUpstream(w http.ResponseWriter, r *http.Request) {
	d, err := h.Upstreams.Get(&actor(r).User, r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	provider, ok := h.OAuth.ProviderOfKind(d.Kind)
	if !ok {
		h.fail(w, r, service.ErrUpstreamKindReadOnly)
		return
	}
	u, err := h.OAuth.Start(actor(r), provider, service.OAuthStart{Reauth: d.PublicID})
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, oauthURL{URL: u})
}

// OAuthCallback: GET /oauth/{provider}/callback. The provider's redirect lands
// here in the admin's browser; its session cookie (SameSite=Lax is sent on this
// top-level navigation) must be the one that started the flow. It answers with
// a redirect to /admin/mailboxes/: ?added=<id> for a new mailbox,
// ?reauthorized=<id> after re-authorisation, ?oauthError=<msg>&code=<n> on failure.
func (h *Admin) OAuthCallback(w http.ResponseWriter, r *http.Request) {
	var p *service.AdminPrincipal
	if c, err := r.Cookie(middleware.SessionCookie); err == nil {
		p, _ = h.Accounts.ResolveSession(c.Value)
	}
	res, err := h.OAuth.Callback(r.Context(), p, r.PathValue("provider"), r.URL.Query())
	q := url.Values{}
	entry := &model.AuditLog{Method: r.Method, Path: r.URL.Path, Status: http.StatusFound, Code: 200, Msg: "success",
		IP: h.ClientIP(r), TraceID: middleware.TraceID(r.Context())}
	if p != nil {
		entry.ActorID, entry.ActorEmail = p.User.PublicID, p.User.Email
	}
	switch {
	case err != nil:
		e := h.callbackError(r, err)
		q.Set("oauthError", e.msg)
		q.Set("code", strconv.Itoa(e.code))
		entry.Code, entry.Msg = e.code, e.msg
	case res.Created:
		q.Set("added", res.AccountID)
		entry.Params = `{"upstream":"` + res.AccountID + `","created":true}`
	default:
		q.Set("reauthorized", res.AccountID)
		entry.Params = `{"upstream":"` + res.AccountID + `"}`
	}
	if h.Audit != nil {
		if aerr := h.Audit.Record(entry); aerr != nil {
			h.Log.Error("write audit log", "trace_id", entry.TraceID, "err", aerr)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/admin/mailboxes/?"+q.Encode(), http.StatusFound)
}

// callbackError is the public form of a callback failure.
func (h *Admin) callbackError(r *http.Request, err error) apiError {
	for _, e := range adminErrors {
		if errors.Is(err, e.err) {
			return e.api
		}
	}
	h.Log.Error("oauth callback failed", "trace_id", middleware.TraceID(r.Context()), "err", err)
	return apiError{500, 50000, "internal_error"}
}
