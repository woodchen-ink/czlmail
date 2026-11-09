package auth

import (
	"errors"
	"net/http"

	"golang.org/x/oauth2"
)

// 本文件回答一个问题: 一次失败是"凭据被拒绝"还是"暂时连不上"。
//
// 两者的后果完全不同: 前者重试多少次都没用, 必须让用户重新登录; 后者过一会儿自己会好,
// 把用户踢回登录页是错的。判定只认服务器明确说出来的拒绝, 断网、超时、5xx 一律不算。

// TokenRejected 报告 err 是否表示授权服务器拒绝续期: 刷新令牌已失效、被撤销, 或客户端已被删除。
//
// 取不到令牌的其它原因(连不上授权服务器、5xx、授权服务器自称暂时不可用)返回 false。
func TokenRejected(err error) bool {
	if errors.Is(err, ErrNoRefreshToken) {
		return true
	}
	var re *oauth2.RetrieveError
	if !errors.As(err, &re) || re.Response == nil {
		return false
	}
	switch re.ErrorCode {
	case "temporarily_unavailable", "server_error":
		return false
	}
	// RFC 6749 §5.2: 刷新令牌无效回 400 invalid_grant, 客户端认证失败回 401 invalid_client。
	// 不按 error 字段白名单判断: 各家授权服务器对"这个刷新令牌我不认"的写法并不统一,
	// 而 400/401 已经说明请求本身被拒绝, 原样重发不会成功。
	return re.Response.StatusCode == http.StatusBadRequest || re.Response.StatusCode == http.StatusUnauthorized
}

// Watch 包住已认证的客户端, 在出现"凭据可能失效"的迹象时调用 onSuspect。
//
// 迹象有两种: 服务器回 401, 或续期令牌被授权服务器拒绝(请求根本没发出去)。
// 这只是迹象不是结论 —— 单次 401 可能来自中间代理, 由调用方再向会话地址核实一次。
// onSuspect 在请求协程上同步调用, 实现方不应阻塞。
func Watch(client *http.Client, onSuspect func()) *http.Client {
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	watched := *client
	watched.Transport = &watchTransport{base: base, onSuspect: onSuspect}
	return &watched
}

type watchTransport struct {
	base      http.RoundTripper
	onSuspect func()
}

func (t *watchTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		if TokenRejected(err) {
			t.onSuspect()
		}
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		t.onSuspect()
	}
	return resp, nil
}
