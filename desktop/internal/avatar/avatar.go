// Package avatar 提供发件人头像: Gravatar 优先, 其次发件域名图标, 都没有时由界面显示首字母。
//
// 所有上游请求都由本进程代理并缓存, 界面只访问本地的 /czl-avatar 路径。
// 若让 WebView 直接去拉发件人域名的图标, 等于每滚动一次列表就向发件方
// 报告一次"这个人正在看你的邮件"—— 与远程图片拦截要防的是同一件事。
package avatar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"

	"github.com/woodchen-ink/czlmail/desktop/internal/safehttp"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// Path 是界面请求头像的本地路径, 形如 /czl-avatar?email=a@b.com。
const Path = "/czl-avatar"

const (
	gravatarBase = "https://i.czl.net/avatar/"
	faviconBase  = "https://icons.duckduckgo.com/ip3/"

	positiveTTL = 14 * 24 * time.Hour
	// 负缓存时间短一些: 发件人随时可能新设 Gravatar。
	negativeTTL = 24 * time.Hour

	maxImageBytes = 512 << 10
)

// personalDomains 是免费邮箱服务商。这些域名的图标是服务商的 logo 而不是发件人,
// 给每个 gmail 用户都显示一个 Google 图标毫无信息量, 不如显示首字母。
var personalDomains = map[string]bool{
	"gmail.com": true, "googlemail.com": true, "outlook.com": true, "hotmail.com": true,
	"live.com": true, "msn.com": true, "yahoo.com": true, "yahoo.co.jp": true, "aol.com": true,
	"icloud.com": true, "me.com": true, "mac.com": true, "mail.com": true, "proton.me": true,
	"protonmail.com": true, "pm.me": true, "tutanota.com": true, "tuta.com": true, "zoho.com": true,
	"yandex.com": true, "yandex.ru": true, "gmx.com": true, "gmx.net": true, "fastmail.com": true,
	"hey.com": true, "posteo.de": true, "mailbox.org": true,
	"qq.com": true, "foxmail.com": true, "163.com": true, "126.com": true, "yeah.net": true,
	"sina.com": true, "sina.cn": true, "sohu.com": true, "aliyun.com": true, "139.com": true,
	"189.cn": true, "wo.cn": true, "tom.com": true,
	"example.com": true, "example.org": true, "example.net": true,
}

// domainPattern 用于拒绝畸形或内网主机名, 这些值会被拼进上游 URL。
var domainPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// Service 是头像的 HTTP 处理器, 挂在 Wails 的 AssetServer 上。
type Service struct {
	store   func() *store.Store
	enabled func() bool
	log     *slog.Logger
	client  *http.Client
	// site 直连发件方网站, 带内网地址拦截。
	site *http.Client

	// inflight 合并同一 key 的并发请求: 列表一屏里同一发件人往往出现十几次。
	mu       sync.Mutex
	inflight map[string]*call
}

type call struct {
	done chan struct{}
	img  *store.CachedAvatar
}

// New 构造服务。store 以函数形式传入, 因为应用启动时缓存库可能尚未打开。
// enabled 为假时一律返回 404, 界面回落到首字母, 用于用户关闭头像的隐私选项。
func New(st func() *store.Store, enabled func() bool, log *slog.Logger) *Service {
	return &Service{
		store:    st,
		enabled:  enabled,
		log:      log,
		client:   &http.Client{Timeout: 8 * time.Second},
		site:     safehttp.NewClient(12 * time.Second),
		inflight: map[string]*call{},
	}
}

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != Path {
		http.NotFound(w, r)
		return
	}
	if s.enabled != nil && !s.enabled() {
		http.NotFound(w, r)
		return
	}

	email := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("email")))
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		http.NotFound(w, r)
		return
	}

	// 分两轮: 第一轮 Gravatar 与聚合图标服务并发查, 按优先级取第一个命中的 ——
	// 串行时一个未命中的 Gravatar 要等上游回 404 才轮到域名图标, 列表首屏会明显发空。
	// 两者都没有时第二轮才直连发件方网站, 能不打扰对方就不打扰。
	keys := candidates(email)
	first, rest := keys, []string(nil)
	if len(keys) > 2 {
		first, rest = keys[:2], keys[2:]
	}
	for _, round := range [][]string{first, rest} {
		if img := s.firstHit(r.Context(), round); img != nil {
			writeImage(w, img)
			return
		}
	}
	http.NotFound(w, r)
}

// firstHit 并发查一组候选, 按传入顺序返回第一个存在的图片。
func (s *Service) firstHit(ctx context.Context, keys []string) *store.CachedAvatar {
	results := make([]*store.CachedAvatar, len(keys))
	var wg sync.WaitGroup
	for i, key := range keys {
		wg.Add(1)
		go func(i int, key string) {
			defer wg.Done()
			results[i] = s.lookup(ctx, key)
		}(i, key)
	}
	wg.Wait()

	for _, img := range results {
		if img != nil && !img.Missing {
			return img
		}
	}
	return nil
}

func writeImage(w http.ResponseWriter, img *store.CachedAvatar) {
	h := w.Header()
	h.Set("Content-Type", img.ContentType)
	h.Set("X-Content-Type-Options", "nosniff")
	// 头像与应用同源; 站点图标可能是 SVG, 被直接打开时不能执行脚本。
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
	// 界面在同一会话内反复渲染同一头像, 让 WebView 自己缓存, 不必每次过 Go。
	h.Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(img.Data)
}

// candidates 按优先级给出要尝试的缓存 key: Gravatar、聚合图标服务、
// 直连发件方网站。后两者只对非免费邮箱的域名给出。
func candidates(email string) []string {
	sum := sha256.Sum256([]byte(email))
	keys := []string{"g:" + hex.EncodeToString(sum[:])}

	domain := email[strings.LastIndex(email, "@")+1:]
	if root, err := publicsuffix.EffectiveTLDPlusOne(domain); err == nil {
		// 用可注册域而不是完整主机名: newsletter.example.com 与 example.com
		// 是同一家发件方, 分开查只会多一次请求且子域通常没有自己的图标。
		if !personalDomains[root] && domainPattern.MatchString(root) {
			keys = append(keys, "f:"+root, "s:"+root)
		}
	}
	return keys
}

// lookup 先查缓存, 过期或未命中时拉上游并回写。
func (s *Service) lookup(ctx context.Context, key string) *store.CachedAvatar {
	st := s.store()
	if st == nil {
		return nil
	}

	if cached, err := st.Avatar(ctx, key); err == nil && cached != nil && fresh(cached) {
		return cached
	}

	s.mu.Lock()
	if c, ok := s.inflight[key]; ok {
		s.mu.Unlock()
		select {
		case <-c.done:
			return c.img
		case <-ctx.Done():
			return nil
		}
	}
	c := &call{done: make(chan struct{})}
	s.inflight[key] = c
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.inflight, key)
		s.mu.Unlock()
		close(c.done)
	}()

	img, ok := s.fetch(ctx, key)
	if !ok {
		// 网络错误不写负缓存: 断网时写入"没有头像", 会让恢复联网后一整天都显示首字母。
		return nil
	}
	if err := st.PutAvatar(context.Background(), key, *img); err != nil {
		s.log.Warn("cache avatar", "key", key, "err", err)
	}
	c.img = img
	return img
}

func fresh(a *store.CachedAvatar) bool {
	ttl := positiveTTL
	if a.Missing {
		ttl = negativeTTL
	}
	return time.Since(a.FetchedAt) < ttl
}

// fetch 拉取上游。返回的 bool 为假表示请求本身失败(不应缓存),
// 为真时 img 要么是图片、要么是确认不存在。
func (s *Service) fetch(ctx context.Context, key string) (*store.CachedAvatar, bool) {
	var target string
	switch {
	case strings.HasPrefix(key, "s:"):
		// 界面滚走取消了请求也把这一次抓完: 合并在 inflight 上的其它请求还在等结果。
		return s.fetchSiteIcon(context.WithoutCancel(ctx), key[2:])
	case strings.HasPrefix(key, "g:"):
		// d=404 让没有头像的地址返回 404, 而不是一张默认占位图 ——
		// 默认图会盖掉后面更有信息量的域名图标。
		target = gravatarBase + key[2:] + "?s=96&d=404"
	case strings.HasPrefix(key, "f:"):
		target = faviconBase + url.PathEscape(key[2:]) + ".ico"
	default:
		return nil, false
	}

	resp, err := s.client.Get(target)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return &store.CachedAvatar{Missing: true}, true
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil || len(data) > maxImageBytes {
		return nil, false
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "image/") {
		// 上游返回 HTML 错误页之类的内容时不当图片缓存, 也不交给 <img>。
		return &store.CachedAvatar{Missing: true}, true
	}
	// 只有一两个像素的图标是上游的"没有图标"占位, 按不存在处理。
	if len(data) < 100 {
		return &store.CachedAvatar{Missing: true}, true
	}
	return &store.CachedAvatar{ContentType: ct, Data: data}, true
}
