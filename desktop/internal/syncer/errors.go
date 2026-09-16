package syncer

import (
	"errors"
	"fmt"
)

// syncer 层错误码占用 1100-1199 区间。
const (
	CodeSession   = 1101 // authenticate session failed
	CodeRequest   = 1102 // jmap request failed
	CodeMethod    = 1103 // jmap method returned an error
	CodeUnhandled = 1104 // unexpected response shape
	CodePush      = 1105 // push stream failed
)

type Error struct {
	Code int
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%d %s: %v", e.Code, e.Msg, e.Err)
	}
	return fmt.Sprintf("%d %s", e.Code, e.Msg)
}

func (e *Error) Unwrap() error { return e.Err }

func (e *Error) Is(target error) bool {
	var t *Error
	if !errors.As(target, &t) {
		return false
	}
	return t.Code == e.Code
}

func wrap(code int, msg string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Code: code, Msg: msg, Err: err}
}
