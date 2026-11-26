package service

import (
	"errors"
	"time"
)

// Errors returned to handlers, which map each to a public code.
var (
	ErrInvalidCredentials  = errors.New("invalid credentials")
	ErrAccountDisabled     = errors.New("account disabled")
	ErrUnauthenticated     = errors.New("unauthenticated")
	ErrForbidden           = errors.New("forbidden")
	ErrSelfModify          = errors.New("cannot change own role, status or account")
	ErrRegistrationClosed  = errors.New("registration closed")
	ErrSetupCompleted      = errors.New("setup already completed")
	ErrInvalidSetupToken   = errors.New("invalid or expired setup token")
	ErrEmailTaken          = errors.New("email already registered")
	ErrUserNotFound        = errors.New("user not found")
	ErrAppPasswordNotFound = errors.New("app password not found")
	ErrWrongPassword       = errors.New("current password is wrong")
	ErrUnknownSetting      = errors.New("unknown setting")
	ErrInvalidSetting      = errors.New("invalid setting value")
	ErrInvalidCursor       = errors.New("invalid cursor")
)

// RateLimitError means too many failed attempts; retry after RetryAfter.
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string { return "rate limited" }

// RetryAfterSeconds is the Retry-After header value, at least 1.
func (e *RateLimitError) RetryAfterSeconds() int {
	s := int((e.RetryAfter + time.Second - 1) / time.Second)
	return max(s, 1)
}
