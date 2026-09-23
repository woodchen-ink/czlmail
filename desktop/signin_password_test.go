package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/auth"
	"github.com/woodchen-ink/czlmail/desktop/internal/config"
)

// 应用专用密码登录的主路径: 只凭用户填的主机名推出会话地址并通过 Basic 认证。
// 打真实服务器, 未提供凭据时跳过。
func TestIntegrationPasswordSignIn(t *testing.T) {
	host := os.Getenv("CZLMAIL_TEST_HOST")
	user := os.Getenv("CZLMAIL_TEST_USER")
	pass := os.Getenv("CZLMAIL_TEST_PASS")
	if host == "" || user == "" || pass == "" {
		t.Skip("set CZLMAIL_TEST_HOST / _USER / _PASS to run")
	}

	endpoint, err := auth.SessionEndpoint(host)
	if err != nil {
		t.Fatalf("session endpoint: %v", err)
	}

	a := &App{ctx: context.Background()}

	ok := &jmap.Client{SessionEndpoint: endpoint, HttpClient: basicAuthClient(user, pass)}
	if err := ok.Authenticate(); err != nil {
		t.Fatalf("正确凭据应能登录: %v", err)
	}
	t.Logf("username=%s accounts=%d", ok.Session.Username, len(ok.Session.Accounts))

	// 填错密码(最常见的是填成了 SSO 密码)时, 错误必须指明是凭据问题。
	badClient := basicAuthClient(user, "wrong-password")
	bad := &jmap.Client{SessionEndpoint: endpoint, HttpClient: badClient}
	err = bad.Authenticate()
	if err == nil {
		t.Fatal("错误密码不应登录成功")
	}
	mapped := a.classifyAuthFailure(config.Config{SessionEndpoint: endpoint, AuthMethod: config.AuthPassword}, badClient, err).Error()
	t.Logf("raw=%q mapped=%q", err.Error(), mapped)
	if !strings.HasPrefix(mapped, "2060") {
		t.Errorf("错误密码应映射为 2060 凭据错误, 实际 %q", mapped)
	}
}

// 地址不是 JMAP 服务器时, 错误要指向地址而不是密码。
func TestIntegrationWrongServerIsNotCredentialError(t *testing.T) {
	if os.Getenv("CZLMAIL_TEST_HOST") == "" {
		t.Skip("set CZLMAIL_TEST_HOST to run")
	}
	a := &App{ctx: context.Background()}
	endpoint := "https://example.com/.well-known/jmap"
	client := basicAuthClient("user@example.com", "x")

	c := &jmap.Client{SessionEndpoint: endpoint, HttpClient: client}
	err := c.Authenticate()
	if err == nil {
		t.Fatal("example.com 不应通过 JMAP 认证")
	}
	mapped := a.classifyAuthFailure(config.Config{SessionEndpoint: endpoint, AuthMethod: config.AuthPassword}, client, err).Error()
	t.Logf("mapped=%q", mapped)
	if strings.HasPrefix(mapped, "2060") {
		t.Errorf("非 JMAP 地址被误报成密码错误: %q", mapped)
	}
}
