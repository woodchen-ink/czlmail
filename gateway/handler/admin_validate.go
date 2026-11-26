package handler

import (
	"errors"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/service"
)

var (
	errInvalidEmail    = errors.New("invalid email")
	errInvalidPassword = errors.New("password must be 10 to 256 characters")
	errInvalidRole     = errors.New("role must be admin or user")
	errInvalidName     = errors.New("name must be 1 to 64 characters")
)

const (
	minPasswordLen = 10
	maxPasswordLen = 256
	maxNameLen     = 64
)

func validEmail(raw string) (string, error) {
	e := service.NormalizeEmail(raw)
	if len(e) < 3 || len(e) > 254 {
		return "", errInvalidEmail
	}
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Address != e || addr.Name != "" {
		return "", errInvalidEmail
	}
	return e, nil
}

func validPassword(p string) (string, error) {
	n := utf8.RuneCountInString(p)
	if n < minPasswordLen || n > maxPasswordLen || !utf8.ValidString(p) {
		return "", errInvalidPassword
	}
	return p, nil
}

// validName trims the name; an empty name falls back to fallback (if any).
func validName(raw, fallback string) (string, error) {
	n := strings.TrimSpace(raw)
	if n == "" {
		n = fallback
	}
	if n == "" || utf8.RuneCountInString(n) > maxNameLen || !utf8.ValidString(n) {
		return "", errInvalidName
	}
	return n, nil
}

func validRole(r string) (string, error) {
	if r != model.RoleAdmin && r != model.RoleUser {
		return "", errInvalidRole
	}
	return r, nil
}

func localPart(email string) string {
	local, _, _ := strings.Cut(email, "@")
	return local
}

// signupInput is the body shared by setup, registration and user creation.
type signupInput struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
}

func (in signupInput) validate(role string) (service.NewUser, error) {
	email, err := validEmail(in.Email)
	if err != nil {
		return service.NewUser{}, err
	}
	name, err := validName(in.Name, localPart(email))
	if err != nil {
		return service.NewUser{}, err
	}
	pw, err := validPassword(in.Password)
	if err != nil {
		return service.NewUser{}, err
	}
	return service.NewUser{Email: email, Name: name, Password: pw, Role: role}, nil
}
