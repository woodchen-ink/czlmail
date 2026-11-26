package upstream

import (
	"errors"

	"git.sr.ht/~rockorager/go-jmap"
)

// go-jmap 把方法错误解析成 *jmap.MethodError, 只留 type 与 description, czlUpstream 就丢了。
// 这里重新登记 "error" 的响应工厂 (go-jmap 的 init 先于依赖它的包执行, 这里的登记覆盖它),
// 解析成带上游原因的 MethodError。方法错误一律经 MethodErrorOf 取, 不要直接断言 *jmap.MethodError。
func init() {
	jmap.RegisterMethod("error", func() jmap.MethodResponse { return &MethodError{} })
}

// MethodError 是 JMAP 方法级错误, 网关因上游不可用而拒绝时带 Upstream。
type MethodError struct {
	jmap.MethodError
	Upstream *Error `json:"czlUpstream,omitempty"`
}

func (m *MethodError) Error() string {
	if m.Upstream != nil {
		return m.Type + ": " + m.Upstream.Error()
	}
	return m.MethodError.Error()
}

// Unwrap 让 errors.As 能取到 *jmap.MethodError 与 *Error。
func (m *MethodError) Unwrap() []error {
	out := []error{&m.MethodError}
	if m.Upstream != nil {
		out = append(out, m.Upstream)
	}
	return out
}

// MethodErrorOf 报告一个方法响应是否是方法错误, 是时返回它 (作为 error, 保留上游原因) 与 JMAP 错误类型名。
func MethodErrorOf(args any) (error, string, bool) {
	switch me := args.(type) {
	case *MethodError:
		return me, me.Type, true
	case *jmap.MethodError:
		return me, me.Type, true
	}
	return nil, "", false
}

// FromError 取出错误链里网关的上游原因, 不是上游不可用时为 nil。
func FromError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return nil
}
