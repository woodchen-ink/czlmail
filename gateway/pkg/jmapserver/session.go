package jmapserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
)

// UserSession is what an AccountSource reports for one user.
type UserSession struct {
	// Username is shown by clients as the signed-in identity.
	Username string
	// Accounts by accountId. accountIds are public random ids, stable for the
	// account's lifetime (across re-authorisation of the upstream).
	Accounts map[string]*Account
	// PrimaryAccounts maps a capability URI to the accountId used by default.
	PrimaryAccounts map[string]string
	// State is an opaque version of everything above that the source tracks
	// (for example upstream statuses). The Session state is derived from the
	// whole Session plus this value, so any visible change also changes it.
	State string
}

// Account is one entry of the Session's accounts.
type Account struct {
	Name       string `json:"name"`
	IsPersonal bool   `json:"isPersonal"`
	IsReadOnly bool   `json:"isReadOnly"`
	// AccountCapabilities values must marshal to JSON objects.
	AccountCapabilities map[string]any `json:"accountCapabilities"`
}

// HasCapability reports whether the account supports the capability.
func (a *Account) HasCapability(uri string) bool {
	_, ok := a.AccountCapabilities[uri]
	return ok
}

type sessionView struct {
	Capabilities    map[string]any      `json:"capabilities"`
	Accounts        map[string]*Account `json:"accounts"`
	PrimaryAccounts map[string]string   `json:"primaryAccounts"`
	Username        string              `json:"username"`
	APIURL          string              `json:"apiUrl"`
	DownloadURL     string              `json:"downloadUrl"`
	UploadURL       string              `json:"uploadUrl"`
	EventSourceURL  string              `json:"eventSourceUrl"`
	State           string              `json:"state"`
}

// session is a loaded UserSession with its rendered state.
type session struct {
	*UserSession
	view  sessionView
	state string
}

func (s *Server) loadSession(ctx context.Context, userID string) (*session, error) {
	us, err := s.opts.Accounts.UserSession(ctx, userID)
	if err != nil {
		return nil, err
	}
	// Copy rather than fill in place: a source may hand out a cached value.
	accounts := make(map[string]*Account, len(us.Accounts))
	for id, a := range us.Accounts {
		c := *a
		if c.AccountCapabilities == nil {
			c.AccountCapabilities = map[string]any{}
		}
		accounts[id] = &c
	}
	primary := us.PrimaryAccounts
	if primary == nil {
		primary = map[string]string{}
	}
	us = &UserSession{Username: us.Username, Accounts: accounts, PrimaryAccounts: primary, State: us.State}
	s.mu.RLock()
	caps := make(map[string]any, len(s.caps))
	for k, v := range s.caps {
		caps[k] = v
	}
	s.mu.RUnlock()

	v := sessionView{
		Capabilities:    caps,
		Accounts:        us.Accounts,
		PrimaryAccounts: us.PrimaryAccounts,
		Username:        us.Username,
		APIURL:          s.url("/jmap/api"),
		DownloadURL:     s.url("/jmap/download/{accountId}/{blobId}/{name}?type={type}"),
		UploadURL:       s.url("/jmap/upload/{accountId}/"),
		EventSourceURL:  s.url("/jmap/eventsource?types={types}&closeafter={closeafter}&ping={ping}"),
	}
	body, err := json.Marshal(v) // map keys are sorted, so equal sessions hash equally
	if err != nil {
		return nil, err
	}
	sum := sha256.New()
	sum.Write(body)
	sum.Write([]byte{0})
	sum.Write([]byte(us.State))
	v.State = hex.EncodeToString(sum.Sum(nil)[:12])
	return &session{UserSession: us, view: v, state: v.State}, nil
}

// WellKnown redirects /.well-known/jmap to the Session resource (RFC 8620 §2.2).
func (s *Server) WellKnown(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, s.url("/jmap/session"), http.StatusFound)
}

// Session serves the Session resource. A user without any account gets 403
// no-upstream instead of an empty Session, so the client can tell them to add a
// mailbox in the gateway admin.
func (s *Server) Session(w http.ResponseWriter, r *http.Request) {
	userID, ok := s.authenticate(w, r)
	if !ok {
		return
	}
	sess, err := s.loadSession(r.Context(), userID)
	if err != nil {
		s.log.Error("load session", "trace_id", s.traceID(r.Context()), "user", userID, "err", err)
		writeProblem(w, problemInternal())
		return
	}
	if len(sess.Accounts) == 0 {
		writeProblem(w, problemNoUpstream(s.opts.BaseURL))
		return
	}
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	writeJSON(w, http.StatusOK, sess.view)
}

// authenticate writes 401 and returns false when the request has no valid credentials.
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (string, bool) {
	userID, err := s.opts.Auth.Authenticate(r)
	if err != nil || userID == "" {
		w.Header().Set("WWW-Authenticate", `Basic realm="czlmail gateway", charset="UTF-8"`)
		writeProblem(w, problemUnauthorized())
		return "", false
	}
	return userID, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false) // keep "&" readable in the URL templates
	_ = enc.Encode(v)
}
