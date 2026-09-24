// Package safehttp 提供只连公网地址的 HTTP 客户端, 用于请求由发件人控制的地址
// (发件方网站图标、邮件里的远程图片)。
package safehttp

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

const maxRedirects = 5

// ErrBlockedAddress 表示目标解析到了内网或本机地址。
var ErrBlockedAddress = errors.New("blocked non-public address")

// NewClient 构造直连客户端。地址完全由发件人控制, 可以解析到 127.0.0.1
// 或局域网设备, 因此在拨号时按实际连接的 IP 拒绝非公网地址 —— 在解析阶段检查
// 挡不住 DNS rebinding, 重定向也会绕过只针对首个 URL 的检查。
func NewClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || !PublicIP(ip) {
				return ErrBlockedAddress
			}
			return nil
		},
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 6 * time.Second,
		MaxIdleConnsPerHost:   2,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return http.ErrUseLastResponse
			}
			if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
				return fmt.Errorf("unsupported redirect scheme %q", req.URL.Scheme)
			}
			return nil
		},
	}
}

// PublicIP 判断是否公网地址。
func PublicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() ||
		ip.IsInterfaceLocalMulticast())
}
