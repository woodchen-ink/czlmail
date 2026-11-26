package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 登录自动配置: 邮件服务器把登录交给外部身份提供商时, 管理员在域名下放一个
// /.well-known/czlmail.json, 客户端据此直接显示「使用 XX 登录」, 用户不必手填 IdP 地址与客户端 ID。
//
// 文件内容:
//
//	{
//	  "version": 1,
//	  "oauth": {
//	    "name": "CZL Connect",
//	    "issuer": "https://connect.czl.net",
//	    "clientId": "xxxx"
//	  }
//	}
//
// czlmail 网关 (gateway/) 也放这个文件, 带 gateway 对象而不是 oauth:
//
//	{"version": 1, "gateway": {"name": "czlmail 网关", "adminUrl": "https://mail.example.net/admin/", "protocolVersion": 1, "version": "…"}}
//
// 登录页据此说明密码是网关后台生成的应用专用密码 (不是 Gmail 等邮箱的密码), 并给出打开网关后台的链接。
//
// 依次查找服务器主机名与其上级域名(mail.czl.net → czl.net), 取第一个有效的。
// 只走 HTTPS: 文件决定用户去哪里输入密码, 来源必须由域名证书保证, 不接受 DNS TXT 这类可被劫持的渠道。

// SignInHint 是自动发现到的登录方式。Name / Issuer / ClientID 是单点登录, 没有时为空 (ClientID 为空即没有单点登录)。
type SignInHint struct {
	Name     string `json:"name"`
	Issuer   string `json:"issuer"`
	ClientID string `json:"clientId"`
	// Gateway 在服务器是 czlmail 网关时给出, 否则为空。
	Gateway *GatewayHint `json:"gateway"`
	// Source 是找到配置文件的地址, 界面上显示以便核对。
	Source string `json:"source"`
}

// GatewayHint 是 czlmail 网关的名称与后台地址。
type GatewayHint struct {
	Name     string `json:"name"`
	AdminURL string `json:"adminUrl"`
}

const signInHintMaxBytes = 64 << 10

// DiscoverSignIn 为服务器地址查找登录自动配置, 找不到时返回 nil。
func (a *App) DiscoverSignIn(server string) (*SignInHint, error) {
	host := strings.TrimSpace(server)
	if host == "" {
		return nil, nil
	}
	if !strings.Contains(host, "://") {
		host = "https://" + host
	}
	u, err := url.Parse(host)
	if err != nil || u.Hostname() == "" {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(a.ctx, 8*time.Second)
	defer cancel()
	for _, h := range hintHosts(u.Hostname()) {
		endpoint := "https://" + h + "/.well-known/czlmail.json"
		hint, err := fetchSignInHint(ctx, endpoint)
		if err != nil {
			a.log.Info("sign-in hint not found", "url", endpoint, "err", err)
			continue
		}
		return hint, nil
	}
	return nil, nil
}

// hintHosts 列出要查找的主机名: 自身及去掉最左一段后的上级域名, 不查到顶级域。
func hintHosts(hostname string) []string {
	labels := strings.Split(strings.ToLower(hostname), ".")
	var out []string
	for i := 0; len(labels)-i >= 2; i++ {
		out = append(out, strings.Join(labels[i:], "."))
	}
	return out
}

func fetchSignInHint(ctx context.Context, endpoint string) (*SignInHint, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Timeout: 5 * time.Second,
		// 跟随跳转(czl.net → www.czl.net), 但不允许降级到 HTTP。
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if r.URL.Scheme != "https" {
				return fmt.Errorf("refuse non-https redirect")
			}
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return parseSignInHint(io.LimitReader(resp.Body, signInHintMaxBytes), endpoint)
}

// parseSignInHint 解读 czlmail.json, 既没有可用的单点登录也不是网关时报错。
func parseSignInHint(r io.Reader, endpoint string) (*SignInHint, error) {
	var doc struct {
		Version int `json:"version"`
		OAuth   *struct {
			Name     string `json:"name"`
			Issuer   string `json:"issuer"`
			ClientID string `json:"clientId"`
		} `json:"oauth"`
		Gateway *struct {
			Name     string `json:"name"`
			AdminURL string `json:"adminUrl"`
		} `json:"gateway"`
	}
	if err := json.NewDecoder(r).Decode(&doc); err != nil {
		return nil, err
	}
	hint := &SignInHint{Source: endpoint}
	if o := doc.OAuth; o != nil && o.ClientID != "" && strings.HasPrefix(o.Issuer, "https://") {
		hint.Name = strings.TrimSpace(o.Name)
		if hint.Name == "" {
			hint.Name = strings.TrimPrefix(o.Issuer, "https://")
		}
		hint.Issuer, hint.ClientID = o.Issuer, o.ClientID
	}
	if g := doc.Gateway; g != nil {
		hint.Gateway = &GatewayHint{Name: strings.TrimSpace(g.Name), AdminURL: webURL(g.AdminURL)}
	}
	if hint.ClientID == "" && hint.Gateway == nil {
		return nil, fmt.Errorf("neither an oauth nor a gateway section")
	}
	return hint, nil
}

// webURL 只留 http(s) 地址, 其它 (javascript:、file: 等) 一律丢掉: 界面会把它交给系统浏览器打开。
func webURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	return u.String()
}
