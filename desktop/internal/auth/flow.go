package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/pkg/browser"
	"golang.org/x/oauth2"
)

// authorizeTimeout 是等待用户在浏览器里完成授权的上限。
// 给得宽松: 用户可能要先登录、过二次验证, 甚至去翻手机上的验证码。
const authorizeTimeout = 5 * time.Minute

// loopbackPorts 是优先尝试的回环端口。
//
// RFC 8252 要求服务器在匹配回环重定向时忽略端口, 但并非所有实现都照做。
// 固定候选端口使得 client_id 可以连同 redirect_uri 一起缓存, 常规情况下
// 后续登录不必重新注册客户端。全部被占用时退回系统分配的临时端口。
var loopbackPorts = []int{47821, 47822, 47823, 47824}

// Config 描述一次已就绪的 OAuth 配置, 可直接用于取令牌。
type Config struct {
	Metadata *Metadata
	ClientID string
	// RedirectURI 必须与注册时一致, 交换授权码时服务器会校验。
	RedirectURI string
	Scopes      []string
}

func (c *Config) oauth2Config() *oauth2.Config {
	return &oauth2.Config{
		ClientID:    c.ClientID,
		RedirectURL: c.RedirectURI,
		Scopes:      c.Scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:  c.Metadata.AuthorizationEndpoint,
			TokenURL: c.Metadata.TokenEndpoint,
			// 公开客户端没有 secret, 只能把 client_id 放在请求体里,
			// 用 basic auth 风格发送会被服务器当成缺少凭据。
			AuthStyle: oauth2.AuthStyleInParams,
		},
	}
}

// Authorize 打开系统浏览器完成授权码流程, 返回令牌。
//
// 授权码交给本机监听的回环地址而不是自定义 URL scheme: 自定义 scheme 可以被同机
// 的其它应用抢注, 回环地址则由操作系统保证只有绑定该端口的进程能收到。
func Authorize(ctx context.Context, cfg *Config, listener net.Listener) (*oauth2.Token, error) {
	ctx, cancel := context.WithTimeout(ctx, authorizeTimeout)
	defer cancel()

	state, err := randomString()
	if err != nil {
		return nil, err
	}
	verifier := oauth2.GenerateVerifier()

	oc := cfg.oauth2Config()
	authURL := oc.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.S256ChallengeOption(verifier),
	)

	result := make(chan authResult, 1)
	srv := &http.Server{Handler: callbackHandler(state, result)}

	go srv.Serve(listener)
	defer srv.Close()

	if err := browser.OpenURL(authURL); err != nil {
		return nil, fmt.Errorf("3020 open browser: %w", err)
	}

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("3021 authorization timed out or was cancelled")
	case res := <-result:
		if res.err != nil {
			return nil, res.err
		}
		// 换令牌必须有自己的超时: 授权上下文还剩几分钟, 令牌端点卡住时界面会一直停在"请在浏览器中完成授权"。
		exCtx, exCancel := context.WithTimeout(context.WithValue(ctx, oauth2.HTTPClient, httpClient), 30*time.Second)
		defer exCancel()
		tok, err := oc.Exchange(exCtx, res.code, oauth2.VerifierOption(verifier))
		if err != nil {
			return nil, fmt.Errorf("3022 exchange authorization code: %w", err)
		}
		return tok, nil
	}
}

type authResult struct {
	code string
	err  error
}

func callbackHandler(wantState string, out chan<- authResult) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		if e := q.Get("error"); e != "" {
			desc := q.Get("error_description")
			writePage(w, "授权未完成", desc)
			out <- authResult{err: fmt.Errorf("3023 authorization denied: %s %s", e, desc)}
			return
		}

		// 常数时间比较: state 是防 CSRF 的凭据, 按普通字符串比较会泄漏前缀信息。
		got := q.Get("state")
		if subtle.ConstantTimeCompare([]byte(got), []byte(wantState)) != 1 {
			writePage(w, "授权未完成", "状态校验失败，请重试。")
			out <- authResult{err: fmt.Errorf("3024 state mismatch")}
			return
		}

		code := q.Get("code")
		if code == "" {
			writePage(w, "授权未完成", "服务器未返回授权码。")
			out <- authResult{err: fmt.Errorf("3025 no authorization code in callback")}
			return
		}

		writePage(w, "登录成功", "可以关闭这个页面，回到 CZL Mail。")
		out <- authResult{code: code}
	})
	return mux
}

// writePage 回一个极简的结果页。样式内联, 因为这个页面由临时的回环服务器提供,
// 取不到应用自身的任何静态资源。
func writePage(w http.ResponseWriter, title, detail string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="zh"><head><meta charset="utf-8">
<title>%s</title></head>
<body style="margin:0;display:flex;align-items:center;justify-content:center;height:100vh;
background:#F6F7F8;color:#10131A;font:15px/1.6 system-ui,-apple-system,sans-serif">
<div style="text-align:center"><h1 style="font-size:18px;margin:0 0 8px">%s</h1>
<p style="margin:0;color:#687280;font-size:14px">%s</p></div></body></html>`,
		title, title, detail)
}

// ListenLoopback 在回环地址上绑定一个端口, 返回监听器与对应的 redirect_uri。
//
// 优先使用固定候选端口, 便于复用已注册的 client_id; 全部被占用时退回临时端口,
// 此时调用方需要为新的 redirect_uri 重新注册客户端。
func ListenLoopback() (net.Listener, string, error) {
	for _, port := range loopbackPorts {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			continue
		}
		return ln, fmt.Sprintf("http://127.0.0.1:%d/callback", port), nil
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, "", fmt.Errorf("3026 bind loopback listener: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	return ln, fmt.Sprintf("http://127.0.0.1:%d/callback", port), nil
}

func randomString() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("3027 generate random state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
