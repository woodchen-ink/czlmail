package handler

import (
	"net/http"

	"github.com/woodchen-ink/go-web-utils/resputil"

	"github.com/woodchen-ink/czlmail/gateway/middleware"
)

type setupStatusView struct {
	Required bool `json:"required"`
}

// SetupStatus: GET /api/admin/setup — whether the first-run setup is still open.
func (h *Admin) SetupStatus(w http.ResponseWriter, r *http.Request) {
	req, err := h.Accounts.SetupRequired()
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, setupStatusView{Required: req})
}

type setupInput struct {
	Token string `json:"token"`
	signupInput
}

// Setup: POST /api/admin/setup — create the owner with the one-time link token.
func (h *Admin) Setup(w http.ResponseWriter, r *http.Request) {
	var in setupInput
	if err := decodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	nu, err := in.validate("")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	u, token, err := h.Accounts.CompleteSetup(in.Token, nu, h.ClientIP(r), r.UserAgent())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	middleware.SetAuditActor(r.Context(), u)
	if err := h.signIn(w, token); err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, newUserView(u))
}

type loginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Login: POST /api/admin/login — email + admin password.
func (h *Admin) Login(w http.ResponseWriter, r *http.Request) {
	var in loginInput
	if err := decodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	email, err := validEmail(in.Email)
	if err != nil || in.Password == "" || len(in.Password) > 4*maxPasswordLen {
		h.fail(w, r, errInvalidRequest)
		return
	}
	u, token, err := h.Accounts.Login(email, in.Password, h.ClientIP(r), r.UserAgent())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	middleware.SetAuditActor(r.Context(), u)
	if err := h.signIn(w, token); err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, newUserView(u))
}

// Register: POST /api/admin/register — self-service sign-up while registration is open.
func (h *Admin) Register(w http.ResponseWriter, r *http.Request) {
	var in signupInput
	if err := decodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	nu, err := in.validate("")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	u, token, err := h.Accounts.Register(nu, h.ClientIP(r), r.UserAgent())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	middleware.SetAuditActor(r.Context(), u)
	if err := h.signIn(w, token); err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, newUserView(u))
}

// Logout: POST /api/admin/logout — end this session.
func (h *Admin) Logout(w http.ResponseWriter, r *http.Request) {
	if err := h.Accounts.Logout(middleware.Principal(r.Context())); err != nil {
		h.fail(w, r, err)
		return
	}
	h.signOut(w)
	resputil.OK(w, struct{}{})
}

// Me: GET /api/admin/me — the signed-in user.
func (h *Admin) Me(w http.ResponseWriter, r *http.Request) {
	h.ensureCSRF(w, r)
	p := middleware.Principal(r.Context())
	resputil.OK(w, newUserView(&p.User))
}

type changePasswordInput struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

// ChangePassword: PUT /api/admin/me/password — signs out the user's other sessions.
func (h *Admin) ChangePassword(w http.ResponseWriter, r *http.Request) {
	var in changePasswordInput
	if err := decodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	next, err := validPassword(in.NewPassword)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.Accounts.ChangeOwnPassword(middleware.Principal(r.Context()), in.CurrentPassword, next); err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, struct{}{})
}
