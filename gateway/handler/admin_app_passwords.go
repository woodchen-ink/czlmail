package handler

import (
	"net/http"

	"github.com/woodchen-ink/go-web-utils/resputil"

	"github.com/woodchen-ink/czlmail/gateway/middleware"
)

// ListAppPasswords: GET /api/admin/app-passwords — the signed-in user's devices.
func (h *Admin) ListAppPasswords(w http.ResponseWriter, r *http.Request) {
	list, err := h.Accounts.ListAppPasswords(middleware.Principal(r.Context()))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]appPasswordView, 0, len(list))
	for i := range list {
		out = append(out, newAppPasswordView(&list[i]))
	}
	resputil.OK(w, out)
}

type createAppPasswordInput struct {
	Name string `json:"name"`
}

// CreateAppPassword: POST /api/admin/app-passwords — the response is the only time the
// full password is shown.
func (h *Admin) CreateAppPassword(w http.ResponseWriter, r *http.Request) {
	var in createAppPasswordInput
	if err := decodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	name, err := validName(in.Name, "")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	ap, plain, err := h.Accounts.CreateAppPassword(middleware.Principal(r.Context()), name)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	resputil.OK(w, createdAppPasswordView{appPasswordView: newAppPasswordView(ap), Password: plain})
}

// RevokeAppPassword: DELETE /api/admin/app-passwords/{id}.
func (h *Admin) RevokeAppPassword(w http.ResponseWriter, r *http.Request) {
	if err := h.Accounts.RevokeAppPassword(middleware.Principal(r.Context()), r.PathValue("id")); err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, struct{}{})
}
