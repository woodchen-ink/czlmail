package handler

import (
	"net/http"

	"github.com/woodchen-ink/go-web-utils/resputil"

	"github.com/woodchen-ink/czlmail/gateway/middleware"
	"github.com/woodchen-ink/czlmail/gateway/service"
)

func actor(r *http.Request) *service.AdminPrincipal { return middleware.Principal(r.Context()) }

// ListUsers: GET /api/admin/users.
func (h *Admin) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.Accounts.ListUsers(&actor(r).User)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	out := make([]userView, 0, len(users))
	for i := range users {
		out = append(out, newUserView(&users[i]))
	}
	resputil.OK(w, out)
}

// GetUser: GET /api/admin/users/{id}.
func (h *Admin) GetUser(w http.ResponseWriter, r *http.Request) {
	u, err := h.Accounts.GetUser(&actor(r).User, r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, newUserView(u))
}

type createUserInput struct {
	signupInput
	Role string `json:"role"`
}

// CreateUser: POST /api/admin/users.
func (h *Admin) CreateUser(w http.ResponseWriter, r *http.Request) {
	var in createUserInput
	if err := decodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	if in.Role == "" {
		in.Role = "user"
	}
	role, err := validRole(in.Role)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	nu, err := in.validate(role)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	u, err := h.Accounts.CreateUser(&actor(r).User, nu)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, newUserView(u))
}

type updateUserInput struct {
	Email    *string `json:"email"`
	Name     *string `json:"name"`
	Role     *string `json:"role"`
	Disabled *bool   `json:"disabled"`
	Password *string `json:"password"`
}

func (in updateUserInput) validate() (service.UserPatch, error) {
	p := service.UserPatch{Disabled: in.Disabled}
	if in.Email != nil {
		e, err := validEmail(*in.Email)
		if err != nil {
			return p, err
		}
		p.Email = &e
	}
	if in.Name != nil {
		n, err := validName(*in.Name, "")
		if err != nil {
			return p, err
		}
		p.Name = &n
	}
	if in.Role != nil {
		role, err := validRole(*in.Role)
		if err != nil {
			return p, err
		}
		p.Role = &role
	}
	if in.Password != nil {
		pw, err := validPassword(*in.Password)
		if err != nil {
			return p, err
		}
		p.Password = &pw
	}
	return p, nil
}

// UpdateUser: PATCH /api/admin/users/{id}. Disabling signs the user out everywhere.
func (h *Admin) UpdateUser(w http.ResponseWriter, r *http.Request) {
	var in updateUserInput
	if err := decodeJSON(r, &in); err != nil {
		h.fail(w, r, err)
		return
	}
	patch, err := in.validate()
	if err != nil {
		h.fail(w, r, err)
		return
	}
	u, err := h.Accounts.UpdateUser(&actor(r).User, r.PathValue("id"), patch)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, newUserView(u))
}

// DeleteUser: DELETE /api/admin/users/{id}.
func (h *Admin) DeleteUser(w http.ResponseWriter, r *http.Request) {
	if err := h.Accounts.DeleteUser(&actor(r).User, r.PathValue("id")); err != nil {
		h.fail(w, r, err)
		return
	}
	resputil.OK(w, struct{}{})
}
