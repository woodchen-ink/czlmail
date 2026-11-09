package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/woodchen-ink/czlmail/desktop/internal/auth"
	"github.com/woodchen-ink/czlmail/desktop/internal/config"
)

// 会话地址的应答决定是否算登录失效: 只有 401 算, 403/404/5xx 与连不上都不能把用户踢出登录。
func TestProbeSessionSeparatesCredentialFromAvailability(t *testing.T) {
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	a := &App{ctx: context.Background()}
	client := basicAuthClient("user@example.com", "app-password")

	cases := []struct {
		status     int
		method     string
		code       string // 期望的错误码前缀, 空表示无错误
		credential bool
	}{
		{http.StatusOK, config.AuthPassword, "", false},
		{http.StatusUnauthorized, config.AuthPassword, "2060", true},
		{http.StatusUnauthorized, config.AuthOAuth, "2064", true},
		{http.StatusForbidden, config.AuthPassword, "2060", false},
		{http.StatusNotFound, config.AuthPassword, "2061", false},
		{http.StatusTooManyRequests, config.AuthPassword, "2063", false},
		{http.StatusInternalServerError, config.AuthPassword, "2063", false},
		{http.StatusBadGateway, config.AuthPassword, "2063", false},
		{http.StatusServiceUnavailable, config.AuthPassword, "2063", false},
	}
	for _, c := range cases {
		status = c.status
		err := a.probeSession(config.Config{SessionEndpoint: srv.URL, AuthMethod: c.method}, client)
		if c.code == "" {
			if err != nil {
				t.Errorf("HTTP %d: 应当无错误, 实际 %v", c.status, err)
			}
			continue
		}
		if err == nil || !strings.HasPrefix(err.Error(), c.code) {
			t.Errorf("HTTP %d (%s): 错误应以 %s 开头, 实际 %v", c.status, c.method, c.code, err)
		}
		if got := isCredentialError(err); got != c.credential {
			t.Errorf("HTTP %d (%s): isCredentialError = %v, want %v", c.status, c.method, got, c.credential)
		}
	}

	srv.Close()
	err := a.probeSession(config.Config{SessionEndpoint: srv.URL, AuthMethod: config.AuthPassword}, client)
	if err == nil || !strings.HasPrefix(err.Error(), "2062") || isCredentialError(err) {
		t.Errorf("连不上服务器应为 2062 且不算登录失效, 实际 %v", err)
	}
}

// OAuth: 邮件服务器根本没被请求到, 续期被授权服务器拒绝才算失效; 授权服务器故障不算。
func TestProbeSessionOAuthRefresh(t *testing.T) {
	mail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer mail.Close()

	tokenStatus := http.StatusBadRequest
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(tokenStatus)
		fmt.Fprint(w, `{"error":"invalid_grant"}`)
	}))
	defer idp.Close()

	a := &App{ctx: context.Background()}
	cfg := config.Config{SessionEndpoint: mail.URL, AuthMethod: config.AuthOAuth}
	newClient := func() *http.Client {
		authCfg := &auth.Config{Metadata: &auth.Metadata{TokenEndpoint: idp.URL}, ClientID: "client"}
		expired := &oauth2.Token{AccessToken: "old", RefreshToken: "r", Expiry: time.Now().Add(-time.Hour)}
		return auth.HTTPClient(a.ctx, authCfg.TokenSource(a.ctx, expired, nil))
	}

	err := a.probeSession(cfg, newClient())
	if !isCredentialError(err) || !strings.HasPrefix(err.Error(), "2065") {
		t.Errorf("续期被拒应为 2065 登录失效, 实际 %v", err)
	}

	tokenStatus = http.StatusServiceUnavailable
	err = a.probeSession(cfg, newClient())
	if err == nil || isCredentialError(err) || !strings.HasPrefix(err.Error(), "2062") {
		t.Errorf("授权服务器 503 应为 2062 且不算登录失效, 实际 %v", err)
	}
}

func TestNoStoredCredentialRequiresSignIn(t *testing.T) {
	if !isCredentialError(fmt.Errorf("restore: %w", errNoStoredCredential)) {
		t.Error("密钥库里没有凭据应当要求重新登录")
	}
	if isCredentialError(errNotSignedIn) || isCredentialError(nil) {
		t.Error("其它错误不应被当成凭据失效")
	}
}
