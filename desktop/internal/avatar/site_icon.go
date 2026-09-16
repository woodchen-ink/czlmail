package avatar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 直连发件方网站取图标, 作为聚合图标服务查不到时的兜底。
//
// 聚合服务对国内站点、小众域名覆盖很差(京东、顺丰都查不到), 而这些站点的首页
// 几乎都声明了图标。代价是向发件方的网站发一次请求, 但它与"哪封邮件被打开"无关:
// 同一域名 14 天只取一次, 且在列表渲染时统一发生。

const (
	maxPageBytes = 512 << 10
	maxRedirects = 5
)

// errBlockedAddress 表示目标解析到了内网或本机地址。
var errBlockedAddress = errors.New("blocked non-public address")

// newSiteClient 构造直连客户端。发件域名完全由发件人控制, 可以解析到 127.0.0.1
// 或局域网设备, 因此在拨号时按实际连接的 IP 拒绝非公网地址 —— 在解析阶段检查
// 挡不住 DNS rebinding, 重定向也会绕过只针对首个 URL 的检查。
func newSiteClient() *http.Client {
	dialer := &net.Dialer{
		Timeout: 5 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || !publicIP(ip) {
				return errBlockedAddress
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
		Timeout:   12 * time.Second,
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

func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() ||
		ip.IsInterfaceLocalMulticast())
}

// fetchSiteIcon 依次尝试 https://domain/ 与 https://www.domain/:
// 解析首页声明的图标, 没有声明时取 /favicon.ico。
//
// 返回的 bool 语义同 fetch: 为假表示网络层面失败, 不应写负缓存。
func (s *Service) fetchSiteIcon(ctx context.Context, domain string) (*store.CachedAvatar, bool) {
	reachable := false
	for _, host := range []string{domain, "www." + domain} {
		img, ok := s.iconFromHost(ctx, host)
		if img != nil {
			return img, true
		}
		reachable = reachable || ok
	}
	if !reachable {
		return nil, false
	}
	return &store.CachedAvatar{Missing: true}, true
}

// iconFromHost 返回找到的图标; ok 表示该主机至少能连上。
func (s *Service) iconFromHost(ctx context.Context, host string) (img *store.CachedAvatar, ok bool) {
	page, base, err := s.getPage(ctx, "https://"+host+"/")
	if err != nil {
		return nil, false
	}

	for _, href := range iconLinks(page) {
		u, err := base.Parse(href)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
			continue
		}
		if img := s.getIcon(ctx, u.String()); img != nil {
			return img, true
		}
	}

	fallback := &url.URL{Scheme: base.Scheme, Host: base.Host, Path: "/favicon.ico"}
	return s.getIcon(ctx, fallback.String()), true
}

// getPage 取首页 HTML, 返回内容与重定向之后的最终地址(相对图标路径以它为基准)。
func (s *Service) getPage(ctx context.Context, target string) ([]byte, *url.URL, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.site.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	// 非 200 的页面也可能带图标声明(比如需要登录的 403 页), 照样解析。
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
	if err != nil && len(data) == 0 {
		return nil, nil, err
	}
	if !strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "html") {
		data = nil
	}
	return data, resp.Request.URL, nil
}

func (s *Service) getIcon(ctx context.Context, target string) *store.CachedAvatar {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Accept", "image/*")
	req.Header.Set("User-Agent", userAgent)

	resp, err := s.site.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil || len(data) > maxImageBytes || len(data) < 64 {
		return nil
	}
	ct := imageType(data, resp.Header.Get("Content-Type"))
	if ct == "" {
		return nil
	}
	return &store.CachedAvatar{ContentType: ct, Data: data}
}

// imageType 按内容判定图片类型, 不信任服务器声明: 很多站点把 favicon.ico 标成
// text/plain 或 application/octet-stream, 而软 404 页面又会被标成 image/x-icon。
func imageType(data []byte, declared string) string {
	sniffed := http.DetectContentType(data)
	switch sniffed {
	case "image/png", "image/jpeg", "image/gif", "image/webp", "image/bmp", "image/x-icon":
		return sniffed
	}
	// SVG 嗅探出来是文本类型, 只在服务器也声明为 SVG 时接受。
	// 头像响应带 CSP sandbox, 即使被直接打开也不会执行其中的脚本。
	if strings.HasPrefix(strings.ToLower(declared), "image/svg+xml") &&
		strings.Contains(strings.ToLower(string(data[:min(len(data), 1024)])), "<svg") {
		return "image/svg+xml"
	}
	return ""
}

// iconLinks 从 HTML 里取图标声明, 按清晰度排序: apple-touch-icon 通常是
// 180px 的 PNG, 缩到头像尺寸比 16px 的 favicon 清楚得多。
func iconLinks(page []byte) []string {
	if len(page) == 0 {
		return nil
	}

	type link struct {
		href string
		rank int
	}
	var links []link

	z := html.NewTokenizer(strings.NewReader(string(page)))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt != html.StartTagToken && tt != html.SelfClosingTagToken {
			continue
		}
		name, hasAttr := z.TagName()
		tag := atom.Lookup(name)
		if tag == atom.Body {
			break
		}
		if tag != atom.Link || !hasAttr {
			continue
		}

		var rel, href, sizes string
		for {
			k, v, more := z.TagAttr()
			switch string(k) {
			case "rel":
				rel = strings.ToLower(string(v))
			case "href":
				href = strings.TrimSpace(string(v))
			case "sizes":
				sizes = string(v)
			}
			if !more {
				break
			}
		}
		if href == "" || strings.HasPrefix(href, "data:") {
			continue
		}

		rank := -1
		fields := strings.Fields(rel)
		switch {
		case contains(fields, "apple-touch-icon"), contains(fields, "apple-touch-icon-precomposed"):
			rank = 3
		case contains(fields, "icon"):
			rank = 1
			if n := iconSize(sizes); n >= 64 {
				rank = 2
			}
		}
		if rank >= 0 {
			links = append(links, link{href, rank})
		}
	}

	out := make([]string, 0, len(links))
	for r := 3; r >= 1; r-- {
		for _, l := range links {
			if l.rank == r {
				out = append(out, l.href)
			}
		}
	}
	return out
}

func iconSize(sizes string) int {
	best := 0
	for _, s := range strings.Fields(strings.ToLower(sizes)) {
		w, _, _ := strings.Cut(s, "x")
		if n, err := strconv.Atoi(w); err == nil && n > best {
			best = n
		}
	}
	return best
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// userAgent 用常见浏览器标识: 不少站点对非浏览器 UA 直接返回 403 或验证页。
const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0 Safari/537.36"
