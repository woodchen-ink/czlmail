// Package auth 实现 RFC 8252(原生应用的 OAuth 2.0): 授权码 + PKCE + 回环重定向。
//
// 桌面端是公开客户端, 拿不住 client secret, 因此不使用任何需要 secret 的流程;
// 用户的密码始终只在服务器自己的登录页里输入, 不经过本应用。
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Metadata 是授权服务器元数据(RFC 8414)中本应用用得到的部分。
type Metadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RegistrationEndpoint  string   `json:"registration_endpoint"`
	ScopesSupported       []string `json:"scopes_supported"`

	CodeChallengeMethods []string `json:"code_challenge_methods_supported"`
	GrantTypesSupported  []string `json:"grant_types_supported"`
}

// Discover 从主机名取回授权服务器元数据。
//
// 先试 RFC 8414 的 oauth-authorization-server, 再退到 OpenID Connect 的
// openid-configuration: 前者才是 OAuth 的规范位置, 后者是多数实现的兼容别名。
func Discover(ctx context.Context, host string) (*Metadata, error) {
	base, err := normalizeHost(host)
	if err != nil {
		return nil, err
	}

	var lastErr error
	for _, path := range []string{
		"/.well-known/oauth-authorization-server",
		"/.well-known/openid-configuration",
	} {
		meta, err := fetchMetadata(ctx, base+path)
		if err == nil {
			if err := meta.validate(); err != nil {
				return nil, err
			}
			return meta, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("3001 discover authorization server: %w", lastErr)
}

func fetchMetadata(ctx context.Context, endpoint string) (*Metadata, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var meta Metadata
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// validate 拒绝缺少必需端点或不支持 S256 的服务器。
//
// 不接受降级到 plain challenge: PKCE 的 plain 方法在本地回环场景下几乎不提供保护,
// 宁可明确失败也不要在用户以为走了 PKCE 时静默退化。
func (m *Metadata) validate() error {
	if m.AuthorizationEndpoint == "" || m.TokenEndpoint == "" {
		return fmt.Errorf("3002 authorization server metadata is missing required endpoints")
	}
	if len(m.CodeChallengeMethods) > 0 && !contains(m.CodeChallengeMethods, "S256") {
		return fmt.Errorf("3003 authorization server does not support S256 code challenge")
	}
	return nil
}

// Scopes 返回本应用要申请的权限, 只保留服务器声明支持的部分。
//
// offline_access 决定能否拿到 refresh token —— 没有它, 每次令牌过期都要重新走一遍
// 浏览器授权, 桌面客户端会变得不可用。
func (m *Metadata) Scopes() []string {
	want := []string{
		"openid",
		// 邮件服务器校验外部 IdP 的令牌时, 靠 userinfo 里的 email 识别用户
		// (Stalwart 的 claimUsername 通常就是 email)。漏了它令牌照样能发下来,
		// 但邮件服务器会认不出这是谁。
		"email",
		"profile",
		"offline_access",
		"urn:ietf:params:oauth:scope:mail",
		"urn:ietf:params:oauth:scope:contacts",
		"urn:ietf:params:oauth:scope:calendars",
	}

	// 服务器没有声明 scopes_supported 时按 RFC 8414 不能推断, 原样申请。
	if len(m.ScopesSupported) == 0 {
		return want
	}

	out := make([]string, 0, len(want))
	for _, s := range want {
		if contains(m.ScopesSupported, s) {
			out = append(out, s)
		}
	}
	return out
}

// SessionEndpoint 由邮件服务器地址推出 JMAP 会话地址。
//
// 刻意接受参数而不是用 m.Issuer: 授权服务器与邮件服务器可以是两台机器。
// 当邮件服务器把认证委托给外部 IdP 时, issuer 指向 IdP, 拿它拼会话地址
// 会把 JMAP 请求打到身份提供商上。
func SessionEndpoint(mailHost string) (string, error) {
	base, err := normalizeHost(mailHost)
	if err != nil {
		return "", err
	}
	return base + "/.well-known/jmap", nil
}

// normalizeHost 接受 "mail.example.net" 或 "https://mail.example.net/" 两种写法。
func normalizeHost(host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", fmt.Errorf("3004 server address is empty")
	}
	if !strings.Contains(host, "://") {
		host = "https://" + host
	}

	u, err := url.Parse(host)
	if err != nil {
		return "", fmt.Errorf("3005 invalid server address: %w", err)
	}
	if u.Scheme != "https" && u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1" {
		return "", fmt.Errorf("3006 server address must use https")
	}
	return u.Scheme + "://" + u.Host, nil
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// httpClient 给发现与注册用, 超时短于交互式授权本身。
var httpClient = &http.Client{Timeout: 20 * time.Second}

// resourceMetadata 是受保护资源元数据(RFC 9728)中本应用用得到的部分。
type resourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
}

// DiscoverForResource 只凭邮件服务器地址找到应当去哪里授权。
//
// 邮件服务器可能自己就是授权服务器, 也可能把认证委托给外部 IdP ——
// 后一种情况下它只校验令牌, 不会把用户重定向过去。唯一不需要用户额外填写
// 任何东西的办法, 是由邮件服务器按 RFC 9728 在 oauth-protected-resource 里
// 声明它信任的授权服务器。这是运营方的配置, 不是用户的。
//
// 取不到资源元数据时回落到把邮件服务器本身当授权服务器, 兼容未实现 RFC 9728 的部署。
func DiscoverForResource(ctx context.Context, mailHost string) (*Metadata, error) {
	base, err := normalizeHost(mailHost)
	if err != nil {
		return nil, err
	}

	if as := fetchAuthorizationServer(ctx, base); as != "" {
		return Discover(ctx, as)
	}
	return Discover(ctx, base)
}

// fetchAuthorizationServer 返回资源元数据里声明的第一个授权服务器, 取不到时返回空串。
func fetchAuthorizationServer(ctx context.Context, base string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/.well-known/oauth-protected-resource", nil)
	if err != nil {
		return ""
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var prm resourceMetadata
	if err := json.NewDecoder(resp.Body).Decode(&prm); err != nil {
		return ""
	}
	for _, as := range prm.AuthorizationServers {
		if strings.TrimSpace(as) != "" {
			return strings.TrimSpace(as)
		}
	}
	return ""
}
