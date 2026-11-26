package handler

import (
	"encoding/json"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/service"
)

type userView struct {
	ID          string  `json:"id"`
	Email       string  `json:"email"`
	Name        string  `json:"name"`
	Role        string  `json:"role"`
	Disabled    bool    `json:"disabled"`
	LastLoginAt *string `json:"lastLoginAt"`
	CreatedAt   string  `json:"createdAt"`
}

func newUserView(u *model.User) userView {
	return userView{
		ID: u.PublicID, Email: u.Email, Name: u.Name, Role: u.Role, Disabled: u.Disabled,
		LastLoginAt: rfc3339(u.LastLoginAt), CreatedAt: *rfc3339(&u.CreatedAt),
	}
}

type appPasswordView struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Prefix     string  `json:"prefix"` // czl_<prefix>_, to tell devices apart
	CreatedAt  string  `json:"createdAt"`
	LastUsedAt *string `json:"lastUsedAt"`
	LastUsedIP string  `json:"lastUsedIp"`
}

func newAppPasswordView(a *model.AppPassword) appPasswordView {
	return appPasswordView{
		ID: a.PublicID, Name: a.Name, Prefix: service.DisplayPrefix(a.Prefix),
		CreatedAt: *rfc3339(&a.CreatedAt), LastUsedAt: rfc3339(a.LastUsedAt), LastUsedIP: a.LastUsedIP,
	}
}

// createdAppPasswordView is the only response that ever carries the full password.
type createdAppPasswordView struct {
	appPasswordView
	Password string `json:"password"`
}

type auditView struct {
	ID         string          `json:"id"`
	ActorID    string          `json:"actorId"`
	ActorEmail string          `json:"actorEmail"`
	Method     string          `json:"method"`
	Path       string          `json:"path"`
	Params     json.RawMessage `json:"params"`
	Status     int             `json:"status"`
	Code       int             `json:"code"`
	Msg        string          `json:"msg"`
	IP         string          `json:"ip"`
	TraceID    string          `json:"traceId"`
	CreatedAt  string          `json:"createdAt"`
}

func newAuditView(a *model.AuditLog) auditView {
	params := json.RawMessage("{}")
	if a.Params != "" && json.Valid([]byte(a.Params)) {
		params = json.RawMessage(a.Params)
	} else if a.Params != "" {
		params, _ = json.Marshal(a.Params) // truncated summary: show as a string
	}
	return auditView{
		ID: a.PublicID, ActorID: a.ActorID, ActorEmail: a.ActorEmail, Method: a.Method, Path: a.Path,
		Params: params, Status: a.Status, Code: a.Code, Msg: a.Msg, IP: a.IP, TraceID: a.TraceID,
		CreatedAt: *rfc3339(&a.CreatedAt),
	}
}
