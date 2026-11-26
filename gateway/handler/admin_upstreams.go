package handler

import (
	"errors"
	"net"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/woodchen-ink/go-web-utils/resputil"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/pkg/netx"
	"github.com/woodchen-ink/czlmail/gateway/service"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

var (
	errInvalidHost     = errors.New("invalid host")
	errInvalidPort     = errors.New("invalid port")
	errInvalidTLSMode  = errors.New("invalid tls mode")
	errInvalidProxy    = errors.New("invalid proxy")
	errMissingPassword = errors.New("missing password")
)

const maxSecretLen = 1024

type endpointInput struct {
	Host     string  `json:"host"`
	Port     int     `json:"port"`
	TLS      string  `json:"tls"`
	Username string  `json:"username"`
	Password *string `json:"password"`
}

type proxyInput struct {
	Mode     string  `json:"mode"`
	URL      string  `json:"url"`
	Password *string `json:"password"`
}

type upstreamInput struct {
	User  string         `json:"user"` // admins: whose upstream; default the signed-in user
	Name  string         `json:"name"`
	Email string         `json:"email"`
	IMAP  *endpointInput `json:"imap"`
	SMTP  *endpointInput `json:"smtp"`
	Proxy *proxyInput    `json:"proxy"`
	// SentCopy: auto, gateway or server (see README "Sending").
	SentCopy string `json:"sentCopy"`
}

// toService validates the input. creating requires every IMAP field and a password.
func (in *upstreamInput) toService(creating bool) (service.UpstreamInput, error) {
	var out service.UpstreamInput
	if creating || in.Email != "" {
		email, err := validEmail(in.Email)
		if err != nil {
			return out, err
		}
		out.Email = email
	}
	if creating || in.Name != "" {
		name, err := validName(in.Name, out.Email)
		if err != nil {
			return out, err
		}
		out.Name = name
	}
	if creating && in.IMAP == nil {
		return out, errInvalidHost
	}
	if in.IMAP != nil {
		ep, err := validEndpoint(in.IMAP, out.Email)
		if err != nil {
			return out, err
		}
		if creating && (ep.Password == nil || *ep.Password == "") {
			return out, errMissingPassword
		}
		out.IMAP = ep
	}
	if in.SMTP != nil {
		ep, err := validEndpoint(in.SMTP, "")
		if err != nil {
			return out, err
		}
		out.SMTP = ep
	}
	if p := in.Proxy; p != nil {
		switch p.Mode {
		case model.ProxyFollow, model.ProxyNone:
		case model.ProxyCustom:
			if _, err := netx.ParseProxyURL(p.URL); err != nil {
				return out, errInvalidProxy
			}
		default:
			return out, errInvalidProxy
		}
		if p.Password != nil && len(*p.Password) > maxSecretLen {
			return out, errInvalidProxy
		}
		out.Proxy = &service.ProxyInput{Mode: p.Mode, URL: strings.TrimSpace(p.URL), Password: emptyIsKeep(p.Password)}
	}
	switch in.SentCopy {
	case "", upstream.SentCopyAuto, upstream.SentCopyGateway, upstream.SentCopyServer:
		out.SentCopy = in.SentCopy
	default:
		return out, errInvalidSentCopy
	}
	return out, nil
}

func validEndpoint(e *endpointInput, defaultUser string) (*service.Endpoint, error) {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(e.Host)), ".")
	if host == "" || len(host) > 253 || strings.ContainsAny(host, " /\\@:") && net.ParseIP(host) == nil {
		return nil, errInvalidHost
	}
	if e.Port < 1 || e.Port > 65535 {
		return nil, errInvalidPort
	}
	switch e.TLS {
	case imapc.TLSImplicit, imapc.TLSStartTLS, imapc.TLSNone:
	default:
		return nil, errInvalidTLSMode
	}
	user := strings.TrimSpace(e.Username)
	if user == "" {
		user = defaultUser
	}
	if len(user) > 254 || !utf8.ValidString(user) {
		return nil, errInvalidName
	}
	if e.Password != nil && len(*e.Password) > maxSecretLen {
		return nil, errInvalidPassword
	}
	return &service.Endpoint{Host: host, Port: e.Port, TLS: e.TLS, Username: user, Password: emptyIsKeep(e.Password)}, nil
}

// emptyIsKeep: an empty or missing password keeps the stored one.
func emptyIsKeep(p *string) *string {
	if p == nil || *p == "" {
		return nil
	}
	return p
}

// ListUpstreams: GET /api/admin/upstreams[?user=<publicId>].
func (h *Admin) ListUpstreams(w http.ResponseWriter, r *http.Request) {
	list, err := h.Upstreams.List(&actor(r).User, r.URL.Query().Get("user"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]upstreamView, 0, len(list))
	for i := range list {
		out = append(out, newUpstreamView(&list[i]))
	}
	resputil.OK(w, out)
}

// CreateUpstream: POST /api/admin/upstreams — an IMAP + SMTP mailbox.
func (h *Admin) CreateUpstream(w http.ResponseWriter, r *http.Request) {
	var in upstreamInput
	if err := decodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	svc, err := in.toService(true)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d, err := h.Upstreams.CreateIMAP(r.Context(), &actor(r).User, in.User, svc)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, newUpstreamView(d))
}

// TestUpstream: POST /api/admin/upstreams/test — try settings without saving them.
func (h *Admin) TestUpstream(w http.ResponseWriter, r *http.Request) {
	var in upstreamInput
	if err := decodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	svc, err := in.toService(true)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	rep, err := h.Upstreams.Test(r.Context(), &actor(r).User, "", svc)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, rep)
}

// GetUpstream: GET /api/admin/upstreams/{id}.
func (h *Admin) GetUpstream(w http.ResponseWriter, r *http.Request) {
	d, err := h.Upstreams.Get(&actor(r).User, r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, newUpstreamView(d))
}

// UpdateUpstream: PATCH /api/admin/upstreams/{id} — sections left out stay; empty
// passwords keep the stored ones.
func (h *Admin) UpdateUpstream(w http.ResponseWriter, r *http.Request) {
	var in upstreamInput
	if err := decodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	svc, err := in.toService(false)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d, err := h.Upstreams.Update(r.Context(), &actor(r).User, r.PathValue("id"), svc)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, newUpstreamView(d))
}

// TestStoredUpstream: POST /api/admin/upstreams/{id}/test — with optional changes
// on top of the saved settings (not saved).
func (h *Admin) TestStoredUpstream(w http.ResponseWriter, r *http.Request) {
	var in upstreamInput
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &in); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	svc, err := in.toService(false)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	rep, err := h.Upstreams.Test(r.Context(), &actor(r).User, r.PathValue("id"), svc)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, rep)
}

// PauseUpstream / ResumeUpstream: POST /api/admin/upstreams/{id}/pause|resume.
func (h *Admin) PauseUpstream(w http.ResponseWriter, r *http.Request)  { h.setPaused(w, r, true) }
func (h *Admin) ResumeUpstream(w http.ResponseWriter, r *http.Request) { h.setPaused(w, r, false) }

func (h *Admin) setPaused(w http.ResponseWriter, r *http.Request, paused bool) {
	d, err := h.Upstreams.SetPaused(&actor(r).User, r.PathValue("id"), paused)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, newUpstreamView(d))
}

// DefaultUpstream: POST /api/admin/upstreams/{id}/default.
func (h *Admin) DefaultUpstream(w http.ResponseWriter, r *http.Request) {
	d, err := h.Upstreams.SetDefault(&actor(r).User, r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, newUpstreamView(d))
}

// DeleteUpstream: DELETE /api/admin/upstreams/{id}.
func (h *Admin) DeleteUpstream(w http.ResponseWriter, r *http.Request) {
	if err := h.Upstreams.Delete(&actor(r).User, r.PathValue("id")); err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, struct{}{})
}

type endpointView struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	TLS         string `json:"tls"`
	Username    string `json:"username"`
	HasPassword bool   `json:"hasPassword"`
}

type proxyView struct {
	Mode        string `json:"mode"`
	URL         string `json:"url"`
	HasPassword bool   `json:"hasPassword"`
}

type upstreamStatusView struct {
	State string  `json:"state"`
	Code  *int    `json:"code"`
	Since *string `json:"since"`
	// Detail is the mail server's own last error text.
	Detail      string           `json:"detail"`
	InitialSync *initialSyncView `json:"initialSync"`
}

type initialSyncView struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

type upstreamView struct {
	ID           string             `json:"id"` // the JMAP accountId
	UserID       string             `json:"userId"`
	Kind         string             `json:"kind"`
	Name         string             `json:"name"`
	Email        string             `json:"email"`
	IMAP         endpointView       `json:"imap"`
	SMTP         endpointView       `json:"smtp"`
	Proxy        proxyView          `json:"proxy"`
	Paused       bool               `json:"paused"`
	IsDefault    bool               `json:"isDefault"`
	SentCopy     string             `json:"sentCopy"`
	Status       upstreamStatusView `json:"status"`
	Capabilities any                `json:"capabilities"`
	CreatedAt    *string            `json:"createdAt"`
}

func newUpstreamView(d *service.UpstreamDetail) upstreamView {
	u := &d.Upstream
	st := upstreamStatusView{State: d.Live.State, Detail: d.Live.Error}
	if d.Live.Code != 0 {
		c := d.Live.Code
		st.Code = &c
	}
	if !d.Live.Since.IsZero() {
		st.Since = rfc3339(&d.Live.Since)
	}
	if p := d.Live.Progress; p != nil {
		st.InitialSync = &initialSyncView{Done: p.Done, Total: p.Total}
	}
	var caps any = struct{}{}
	if d.Capabilities != nil {
		caps = d.Capabilities
	}
	return upstreamView{
		ID: u.PublicID, UserID: d.OwnerPublicID, Kind: u.Kind, Name: u.Name, Email: u.Email,
		IMAP:         endpointView{Host: u.IMAPHost, Port: u.IMAPPort, TLS: u.IMAPTLS, Username: u.IMAPUsername, HasPassword: u.PasswordSealed != ""},
		SMTP:         endpointView{Host: u.SMTPHost, Port: u.SMTPPort, TLS: u.SMTPTLS, Username: u.SMTPUsername, HasPassword: u.SMTPPasswordSealed != "" || u.PasswordSealed != ""},
		Proxy:        proxyView{Mode: u.ProxyMode, URL: u.ProxyURL, HasPassword: u.ProxyPasswordSealed != ""},
		Paused:       u.Paused,
		IsDefault:    u.IsDefault,
		SentCopy:     u.SentCopy,
		Status:       st,
		Capabilities: caps,
		CreatedAt:    rfc3339(&u.CreatedAt),
	}
}
