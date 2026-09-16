package store

import (
	"errors"
	"fmt"
)

// store 层错误码占用 1000-1099 区间。
const (
	CodeOpen        = 1001 // open database failed
	CodeMigrate     = 1002 // schema migration failed
	CodeQuery       = 1003 // query failed
	CodeTransaction = 1004 // transaction failed
	CodeEncode      = 1005 // encode column value failed
	CodeDecode      = 1006 // decode column value failed
	CodeNotFound    = 1007 // record not found
)

// ErrNotFound 供调用方用 errors.Is 判定, 不要用字符串比对。
var ErrNotFound = &Error{Code: CodeNotFound, Msg: "record not found"}

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

// Is 让所有同码错误彼此匹配, 使 errors.Is(err, ErrNotFound) 对包装过的实例同样成立。
func (e *Error) Is(target error) bool {
	var t *Error
	if !errors.As(target, &t) {
		return false
	}
	return t.Code == e.Code
}

// wrap 在 err 非空时附加错误码, 为空时返回 nil, 便于调用点直接 return wrap(...)。
func wrap(code int, msg string, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Code: code, Msg: msg, Err: err}
}
