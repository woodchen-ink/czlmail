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
	// refreshed 记每个 key 最近一次后台刷新的时间: 刷新失败 (断网) 时缓存仍是过期的,
	// 不冷却的话列表每渲染一次就为每个发件人再打一次上游。
	refreshed map[string]time.Time

	// fetchKey 拉取一个候选的上游, 默认是 fetch; 测试替换它以免打真实网络。
	fetchKey func(ctx context.Context, key string) (*store.CachedAvatar, bool)
}

type call struct {
	done chan struct{}
	img  *store.CachedAvatar
}

// New 构造服务。store 以函数形式传入, 因为应用启动时缓存库可能尚未打开。
// enabled 为假时一律返回 404, 界面回落到首字母, 用于用户关闭头像的隐私选项。
func New(st func() *store.Store, enabled func() bool, log *slog.Logger) *Service {
	s := &Service{
		store:    st,
		enabled:  enabled,
		log:      log,
		client:   &http.Client{Timeout: 8 * time.Second},
		site:     safehttp.NewClient(12 * time.Second),
		inflight: map[string]*call{},

		refreshed: map[string]time.Time{},
	}
	s.fetchKey = s.fetch
	return s
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

	email, ok := normalize(r.URL.Query().Get("email"))
	if !ok {
		http.NotFound(w, r)
		return
	}

	// 分两轮: 第一轮 Gravatar 与聚合图标服务并发查, 按优先级取第一个命中的 ——
	// 串行时一个未命中的 Gravatar 要等上游回 404 才轮到域名图标, 列表首屏会明显发空。
	// 两者都没有时第二轮才直连发件方网站, 能不打扰对方就不打扰。
	first, rest := rounds(email)
	for _, round := range [][]string{first, rest} {
		if img := s.firstHit(r.Context(), round); img != nil {
			writeImage(w, img)
			return
		}
	}
	http.NotFound(w, r)
}

func normalize(raw string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(raw))
	at := strings.LastIndex(email, "@")
	return email, at > 0 && at < len(email)-1
}

// rounds 把候选分成两轮: 第三方头像与图标服务, 以及直连发件方网站。
func rounds(email string) (first, rest []string) {
	keys := candidates(email)
	if len(keys) > 2 {
		return keys[:2], keys[2:]
	}
	return keys, nil
}

// warmLimit 是一次预热最多处理的地址数: 一轮同步里来了大批邮件时只顾最新的一批。
const warmLimit = 50

// Warm 在后台为这些地址预先取好头像, 新邮件出现在列表里时头像已在缓存里。
// 只查第一轮 (Gravatar 与聚合图标服务): 用户还没看到这封邮件, 不直连发件方网站,
// 否则对方能从访问记录知道邮件已送达一台开着的客户端。逐个串行, 不和界面抢带宽。
func (s *Service) Warm(emails []string) {
	if s.enabled != nil && !s.enabled() {
		return
	}
	seen := map[string]bool{}
	var todo []string
	for _, raw := range emails {
		if email, ok := normalize(raw); ok && !seen[email] && len(todo) < warmLimit {
			seen[email] = true
			todo = append(todo, email)
		}
	}
	if len(todo) == 0 {
		return
	}
	go func() {
		for _, email := range todo {
			// 预热途中用户关掉了头像: 停下, 不再替这些发件人去问第三方。
			if s.enabled != nil && !s.enabled() {
				return
			}
			first, _ := rounds(email)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			s.firstHit(ctx, first)
			cancel()
		}
	}()
}

// firstHit 并发查一组候选, 按传入顺序返回第一个存在的图片。
// 排在前面的都有了结果、且其中有图时立即返回, 不等后面还在拉上游的候选:
// Gravatar 命中时, 域名图标再慢也不该拖住这一张头像。
func (s *Service) firstHit(ctx context.Context, keys []string) *store.CachedAvatar {
	type result struct {
		i   int
		img *store.CachedAvatar
	}
	ch := make(chan result, len(keys))
	for i, key := range keys {
		go func() { ch <- result{i, s.lookup(ctx, key)} }()
	}
	results := make([]*store.CachedAvatar, len(keys))
	done := make([]bool, len(keys))
	for range keys {
		r := <-ch
		results[r.i], done[r.i] = r.img, true
		for i := range keys {
			if !done[i] {
				break
			}
			if img := results[i]; img != nil && !img.Missing {
				return img
			}
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
	// 见 main.noStore: WebView2 复用缓存的响应时图片可能是坏的, 头像另有库里的缓存。
	h.Set("Cache-Control", "no-store")
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

// lookup 先查缓存。没有缓存时拉上游并等结果; 缓存过期时先返回旧的, 后台再拉 ——
// 头像隔天才变一次, 不值得让列表每天都对每个发件人等一次网络。
func (s *Service) lookup(ctx context.Context, key string) *store.CachedAvatar {
	st := s.store()
	if st == nil {
		return nil
	}

	cached, err := st.Avatar(ctx, key)
	if err != nil {
		cached = nil
	}
	if cached != nil {
		if !fresh(cached) {
			go s.refresh(st, key)
		}
		return cached
	}

	c, leader := s.join(key)
	if !leader {
		select {
		case <-c.done:
			return c.img
		case <-ctx.Done():
			return nil
		}
	}
	defer s.leave(key, c)
	c.img = s.fetchAndStore(ctx, st, key)
	return c.img
}

// refreshCooldown 是同一个 key 两次后台刷新的最短间隔。
const refreshCooldown = 10 * time.Minute

// refresh 在后台重新拉一个过期的候选; 已经有人在拉或刚拉过时不重复。
func (s *Service) refresh(st *store.Store, key string) {
	s.mu.Lock()
	if time.Since(s.refreshed[key]) < refreshCooldown {
		s.mu.Unlock()
		return
	}
	s.refreshed[key] = time.Now()
	if len(s.refreshed) > 4096 {
		for k, at := range s.refreshed {
			if time.Since(at) >= refreshCooldown {
				delete(s.refreshed, k)
			}
		}
	}
	s.mu.Unlock()

	c, leader := s.join(key)
	if !leader {
		return
	}
	defer s.leave(key, c)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c.img = s.fetchAndStore(ctx, st, key)
}

// join 登记对 key 的拉取。leader 为真时由调用方去拉, 拉完必须 leave; 否则等 c.done。
func (s *Service) join(key string) (c *call, leader bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.inflight[key]; ok {
		return c, false
	}
	c = &call{done: make(chan struct{})}
	s.inflight[key] = c
	return c, true
}

func (s *Service) leave(key string, c *call) {
	s.mu.Lock()
	delete(s.inflight, key)
	s.mu.Unlock()
	close(c.done)
}

func (s *Service) fetchAndStore(ctx context.Context, st *store.Store, key string) *store.CachedAvatar {
	img, ok := s.fetchKey(ctx, key)
	if !ok {
		// 网络错误不写负缓存: 断网时写入"没有头像", 会让恢复联网后一整天都显示首字母。
		return nil
	}
	if err := st.PutAvatar(context.Background(), key, *img); err != nil {
		s.log.Warn("cache avatar", "key", key, "err", err)
	}
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
