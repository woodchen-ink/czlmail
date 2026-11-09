package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func retrieveError(status int, code string) error {
	return &oauth2.RetrieveError{Response: &http.Response{StatusCode: status}, ErrorCode: code}
}

func TestTokenRejected(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"刷新令牌失效", retrieveError(400, "invalid_grant"), true},
		{"客户端被删除", retrieveError(401, "invalid_client"), true},
		{"400 但没有标准错误码", retrieveError(400, ""), true},
		{"经 http.Client 包了一层", &url.Error{Op: "Get", URL: "https://x", Err: retrieveError(400, "invalid_grant")}, true},
		{"没有刷新令牌", fmt.Errorf("wrapped: %w", ErrNoRefreshToken), true},
		{"授权服务器 5xx", retrieveError(503, ""), false},
		{"授权服务器自称暂时不可用", retrieveError(400, "temporarily_unavailable"), false},
		{"授权服务器内部错误", retrieveError(400, "server_error"), false},
		{"授权服务器限流", retrieveError(429, "slow_down"), false},
		{"连不上", &url.Error{Op: "Post", URL: "https://x", Err: errors.New("dial tcp: connection refused")}, false},
		{"超时", context.DeadlineExceeded, false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := TokenRejected(c.err); got != c.want {
			t.Errorf("%s: TokenRejected = %v, want %v", c.name, got, c.want)
		}
	}
}

// 只有 401 算凭据失效的迹象; 403、5xx 与连不上都不能触发。
func TestWatchOnlyFlagsUnauthorized(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))

	var suspects atomic.Int32
	client := Watch(&http.Client{}, func() { suspects.Add(1) })

	for _, c := range []struct{ status, want int }{
		{http.StatusOK, 0},
		{http.StatusForbidden, 0},
		{http.StatusInternalServerError, 0},
		{http.StatusBadGateway, 0},
		{http.StatusUnauthorized, 1},
	} {
		status = c.status
		suspects.Store(0)
		resp, err := client.Get(srv.URL)
		if err != nil {
			t.Fatalf("status %d: %v", c.status, err)
		}
		resp.Body.Close()
		if got := int(suspects.Load()); got != c.want {
			t.Errorf("status %d: onSuspect called %d times, want %d", c.status, got, c.want)
		}
	}

	srv.Close()
	suspects.Store(0)
	if _, err := client.Get(srv.URL); err == nil {
		t.Fatal("已关闭的服务器应当连不上")
	}
	if got := suspects.Load(); got != 0 {
		t.Errorf("连不上服务器被当成凭据失效: onSuspect called %d times", got)
	}
}

// tokenServer 起一个假的令牌端点, 返回用它续期的客户端配置。
func tokenServer(t *testing.T, handler http.HandlerFunc) *Config {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Config{Metadata: &Metadata{TokenEndpoint: srv.URL}, ClientID: "client"}
}

// 访问令牌过期、刷新令牌被授权服务器拒绝: 请求发不出去, 必须被识别为凭据失效。
func TestExpiredTokenWithRejectedRefreshIsFlagged(t *testing.T) {
	cfg := tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant"}`)
	})
	expired := &oauth2.Token{AccessToken: "old", RefreshToken: "revoked", Expiry: time.Now().Add(-time.Hour)}

	var suspects atomic.Int32
	client := Watch(HTTPClient(context.Background(), cfg.TokenSource(context.Background(), expired, nil)),
		func() { suspects.Add(1) })

	_, err := client.Get("http://127.0.0.1:1/unused")
	if !TokenRejected(err) {
		t.Fatalf("续期被拒应识别为凭据失效, 实际 %v", err)
	}
	if suspects.Load() != 1 {
		t.Errorf("onSuspect called %d times, want 1", suspects.Load())
	}
}

// 授权服务器暂时故障时续期失败, 不能算凭据失效。
func TestRefreshServerErrorIsNotRejection(t *testing.T) {
	cfg := tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	expired := &oauth2.Token{AccessToken: "old", RefreshToken: "good", Expiry: time.Now().Add(-time.Hour)}

	var suspects atomic.Int32
	client := Watch(HTTPClient(context.Background(), cfg.TokenSource(context.Background(), expired, nil)),
		func() { suspects.Add(1) })

	_, err := client.Get("http://127.0.0.1:1/unused")
	if err == nil {
		t.Fatal("续期失败时请求不应成功")
	}
	if TokenRejected(err) || suspects.Load() != 0 {
		t.Errorf("授权服务器 503 被当成凭据失效: err=%v suspects=%d", err, suspects.Load())
	}
}

// 令牌本地看还没过期时不续期; MarkStale 之后强制换一次, 新令牌(含轮换后的刷新令牌)要落盘。
func TestMarkStaleForcesRefreshAndSaves(t *testing.T) {
	var refreshes atomic.Int32
	cfg := tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		refreshes.Add(1)
		if got := r.FormValue("refresh_token"); got != "r1" {
			t.Errorf("refresh_token = %q, want r1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"a2","token_type":"bearer","refresh_token":"r2","expires_in":3600}`)
	})
	var saved *oauth2.Token
	src := cfg.TokenSource(context.Background(),
		&oauth2.Token{AccessToken: "a1", RefreshToken: "r1", Expiry: time.Now().Add(time.Hour)},
		func(tok *oauth2.Token) error { saved = tok; return nil })

	tok, err := src.Token()
	if err != nil || tok.AccessToken != "a1" || refreshes.Load() != 0 {
		t.Fatalf("未过期的令牌应直接复用: tok=%+v err=%v refreshes=%d", tok, err, refreshes.Load())
	}

	src.MarkStale()
	tok, err = src.Token()
	if err != nil {
		t.Fatalf("强制续期: %v", err)
	}
	if tok.AccessToken != "a2" || refreshes.Load() != 1 {
		t.Errorf("MarkStale 后应换新令牌: access=%q refreshes=%d", tok.AccessToken, refreshes.Load())
	}
	if saved == nil || saved.RefreshToken != "r2" {
		t.Errorf("轮换后的刷新令牌没有落盘: %+v", saved)
	}

	if _, err := src.Token(); err != nil || refreshes.Load() != 1 {
		t.Errorf("换新之后不应再次续期: err=%v refreshes=%d", err, refreshes.Load())
	}
}

func TestNoRefreshTokenIsRejection(t *testing.T) {
	cfg := &Config{Metadata: &Metadata{}, ClientID: "client"}
	src := cfg.TokenSource(context.Background(),
		&oauth2.Token{AccessToken: "old", Expiry: time.Now().Add(-time.Hour)}, nil)
	if _, err := src.Token(); !TokenRejected(err) {
		t.Errorf("过期且没有刷新令牌应识别为凭据失效, 实际 %v", err)
	}
}
