package main

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"git.sr.ht/~rockorager/go-jmap"
	_ "git.sr.ht/~rockorager/go-jmap/core" // 注册 Core capability, 否则会话解析不出限制值
	_ "git.sr.ht/~rockorager/go-jmap/mail" // 注册 Mail capability 与方法
	"golang.org/x/oauth2"

	"github.com/woodchen-ink/czlmail/desktop/internal/auth"
	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/platform"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
	"github.com/woodchen-ink/czlmail/desktop/notify"
)

// App 承载应用生命周期, 方法绑定给前端调用。
//
// 绑定方法一律返回 (数据, error): Wails 会把 error 转成前端 Promise 的 reject,
// 吞掉错误只会让界面停在空列表上而不给任何线索。
type App struct {
	opens    openQueue
	prefetch prefetcher
	mcp      mcpState
	images   imageCache
	ctx      context.Context
	log      *slog.Logger

	mu     sync.RWMutex
	store  *store.Store
	client *jmap.Client
	sync   *syncer.Syncer
	cfg    config.Config

	notifier notify.Notifier

	// cancelSync 停掉后台同步循环, 用于重新登录时重建会话。
	cancelSync context.CancelFunc
	// cancelSignIn 中止正在进行的浏览器授权流程。
	// signInGen 标识当前流程, 使收尾时能分辨句柄归属。
	cancelSignIn context.CancelFunc
	signInGen    uint64

	// ready 在启动时读完配置后关闭。界面启动时查询会话状态要等它,
	// 否则会在库还没打开时读到"未配置", 把已登录的用户显示成登录页。
	ready     chan struct{}
	readyOnce sync.Once
}

func NewApp() *App {
	return &App{
		log:   newLogger(),
		ready: make(chan struct{}),
	}
}

// startup 由 Wails 在窗口创建后调用。
//
// 这里只打开本地缓存, 不连服务器: 缓存里已有上次同步的邮件, 界面应当立刻可用。
// 联网是随后异步发生的事, 不该挡在第一帧前面。
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.startTray()
	defer a.markReady()

	// 配置先读: 缓存库打不开时也要知道用户已经登录过, 不能把人赶回登录页。
	cfg, err := config.Load()
	if err != nil {
		a.log.Error("load config", "err", err)
	}
	a.mu.Lock()
	a.cfg = cfg
	a.mu.Unlock()

	dbPath, err := config.DatabasePath()
	if err != nil {
		a.log.Error("resolve database path", "err", err)
		return
	}

	st, err := store.Open(ctx, dbPath)
	if err != nil {
		a.log.Error("open store", "err", err)
		return
	}

	a.mu.Lock()
	a.store = st
	a.mu.Unlock()
	a.markReady()

	a.log.Info("store ready", "path", dbPath)

	go a.runUpdateChecks(ctx)
	go func() {
		if err := platform.RegisterHandlers(); err != nil {
			a.log.Warn("register handlers", "err", err)
		}
	}()
	a.handleArgs(os.Args[1:])

	if on, _ := st.BoolSetting(ctx, store.SettingMCPEnabled, false); on {
		if err := a.startMCP(); err != nil {
			a.log.Error("start mcp", "err", err)
		}
	}

	a.initNotifier()

	if !cfg.Configured() {
		a.log.Info("not configured yet, waiting for sign-in")
		return
	}

	// 已有刷新令牌就静默恢复会话。放后台是因为它要打网络,
	// 挡在这里会让窗口迟迟不出现。
	go func() {
		if err := a.restoreSession(cfg); err != nil {
			a.log.Warn("restore session failed, sign-in required", "err", err)
			a.emit(EventSignInRequired, err.Error())
		}
	}()
}

func (a *App) shutdown(ctx context.Context) {
	a.stopTray()
	a.stopMCP()

	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cancelSync != nil {
		a.cancelSync()
	}
	if a.notifier != nil {
		if err := a.notifier.Close(); err != nil {
			a.log.Error("close notifier", "err", err)
		}
	}
	if a.store != nil {
		if err := a.store.Close(); err != nil {
			a.log.Error("close store", "err", err)
		}
	}
}

// restoreSession 用已保存的凭据重建会话, 按登录方式分派。
func (a *App) restoreSession(cfg config.Config) error {
	if cfg.AuthMethod == config.AuthPassword {
		pass, err := config.LoadPassword(cfg.SessionEndpoint, cfg.Username)
		if err != nil {
			return err
		}
		if pass == "" {
			return fmt.Errorf("2020 no stored credential")
		}
		return a.connect(cfg, basicAuthClient(cfg.Username, pass))
	}
	return a.restoreOAuthSession(cfg)
}

func (a *App) restoreOAuthSession(cfg config.Config) error {
	tok, err := config.LoadToken(cfg.Issuer)
	if err != nil {
		return err
	}
	if tok == nil {
		return fmt.Errorf("2020 no stored credential")
	}

	// 重新发现一次端点: 服务器升级后地址可能变化。失败时不中断 ——
	// 用缓存的端点通常仍能刷新成功, 没必要因为一次发现失败就要求重新登录。
	// 按登录时的授权服务器重新发现: 用外部 IdP 登录的会话必须回到 IdP 续期,
	// 不能按邮件服务器公布的授权服务器(可能是邮件服务器自己)去找。
	var meta *auth.Metadata
	if cfg.Issuer != "" {
		meta, err = auth.Discover(a.ctx, cfg.Issuer)
	} else {
		meta, err = auth.DiscoverForResource(a.ctx, cfg.ServerHost)
	}
	if err != nil {
		a.log.Warn("rediscover authorization server, using cached endpoints", "err", err)
		meta = cachedMetadata(cfg)
	}
	return a.connect(cfg, a.oauthClient(cfg, meta, tok))
}

// oauthClient 构造自动续期的 Bearer 客户端。
func (a *App) oauthClient(cfg config.Config, meta *auth.Metadata, tok *oauth2.Token) *http.Client {
	authCfg := &auth.Config{
		Metadata:    meta,
		ClientID:    cfg.ClientID,
		RedirectURI: cfg.RedirectURI,
		Scopes:      cfg.Scopes,
	}
	// 令牌刷新时同步落盘 —— 服务器可能启用刷新令牌轮换, 不存新值会导致
	// 应用重启后只剩一个已作废的令牌。
	save := func(t *oauth2.Token) error {
		if err := config.SaveToken(cfg.Issuer, t); err != nil {
			a.log.Error("persist refreshed token", "err", err)
			return err
		}
		return nil
	}
	return authCfg.HTTPClient(a.ctx, tok, save)
}

// basicAuthClient 构造带 Basic 认证的客户端。
func basicAuthClient(username, password string) *http.Client {
	c := &jmap.Client{}
	c.WithBasicAuth(username, password)
	return c.HttpClient
}

// connect 用已认证的 HTTP 客户端建立 JMAP 会话并启动后台同步。
func (a *App) markReady() {
	a.readyOnce.Do(func() { close(a.ready) })
}

// waitReady 等启动读完配置与缓存库, 最多等 timeout。
func (a *App) waitReady(timeout time.Duration) {
	select {
	case <-a.ready:
	case <-time.After(timeout):
	}
}

func (a *App) connect(cfg config.Config, httpClient *http.Client) error {
	a.waitReady(30 * time.Second)
	if _, err := a.currentStore(); err != nil {
		return err
	}
	client := &jmap.Client{
		SessionEndpoint: cfg.SessionEndpoint,
		HttpClient:      httpClient,
	}
	// go-jmap 的 Authenticate 不接受 context, 客户端又不能设全局超时(推送连接是长连接),
	// 在这里单独限时, 服务器无响应时报错而不是让登录界面一直转圈。
	authErr := make(chan error, 1)
	go func() { authErr <- client.Authenticate() }()
	select {
	case err := <-authErr:
		if err != nil {
			return a.classifyAuthFailure(cfg, httpClient, err)
		}
	case <-time.After(45 * time.Second):
		return fmt.Errorf("2063 mail server did not respond while opening the session")
	}

	cfg.Username = client.Session.Username

	a.mu.Lock()
	st := a.store
	if a.cancelSync != nil {
		a.cancelSync()
	}
	a.client = client
	a.cfg = cfg
	s := syncer.New(client, st, a.log)
	s.OnNewMail = a.onNewMail
	s.OnChanged = func(accountID string) {
		a.emit(EventMailChanged, accountID)
		a.refreshTrayUnreadSoon()
		a.prefetchRecent(accountID)
	}
	s.OnPIMChanged = func(accountID, typeName string) {
		a.emit(EventPIMChanged, PIMChange{AccountID: accountID, Type: typeName})
		// 模板文件存在个人网盘里; 网盘有变化(含首次同步)时合并一次。
		if (typeName == "" || typeName == "FileNode") && accountID == a.personalAccountID(st) {
			a.syncTemplatesSoon()
		}
	}
	a.sync = s

	// 同步循环的生命周期跟随应用而不是跟随某次调用, 因此从 a.ctx 派生。
	syncCtx, cancel := context.WithCancel(a.ctx)
	a.cancelSync = cancel
	a.mu.Unlock()

	if err := a.persistAccounts(syncCtx, client, st); err != nil {
		a.log.Error("persist accounts", "err", err)
	}

	go a.runReminders(syncCtx)
	go a.runReconcile(syncCtx, s, st)
	go a.runBodyTextBackfill(syncCtx, st)
	go func() {
		if err := s.Run(syncCtx); err != nil {
			a.log.Error("sync loop exited", "err", err)
		}
	}()

	a.log.Info("connected", "username", cfg.Username, "accounts", len(client.Session.Accounts))
	a.emit(EventConnected, cfg.Username)
	return nil
}

// classifyAuthFailure 把会话认证失败转成用户能看懂原因的错误。
//
// go-jmap 对任何非 200 响应都只返回 "couldn't authenticate", 分不出是密码错、
// 地址错, 还是服务器不可达。这三种情况用户的补救动作完全不同, 因此失败后
// 再发一次请求拿到真实状态码。最常见的是账号走 SSO 却填了 SSO 密码。
func (a *App) classifyAuthFailure(cfg config.Config, httpClient *http.Client, cause error) error {
	ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.SessionEndpoint, nil)
	if err != nil {
		return fmt.Errorf("2062 invalid server address: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("2062 cannot reach mail server: %w", err)
	}
	resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		if cfg.AuthMethod == config.AuthOAuth {
			// 邮件服务器把认证委托给外部 IdP 时只认 IdP 的令牌, 自己签发的令牌会被拒。
			return fmt.Errorf("2064 the mail server rejected the OAuth token (HTTP %d); sign in with an app password instead", resp.StatusCode)
		}
		return fmt.Errorf("2060 authentication failed: check the email address and app password (an SSO password will not work here)")
	case http.StatusNotFound:
		return fmt.Errorf("2061 no JMAP service found at this server address")
	default:
		return fmt.Errorf("2063 mail server returned status %d: %w", resp.StatusCode, cause)
	}
}

// persistAccounts 把会话里发现的账号写进缓存, 供界面在离线时也能列出账号。
func (a *App) persistAccounts(ctx context.Context, client *jmap.Client, st *store.Store) error {
	accounts := make([]store.Account, 0, len(client.Session.Accounts))
	caps := make(map[string][]string, len(client.Session.Accounts))

	for id, acc := range client.Session.Accounts {
		accounts = append(accounts, store.Account{
			ID:         string(id),
			Name:       acc.Name,
			IsPersonal: acc.IsPersonal,
		})
		list := make([]string, 0, len(acc.RawCapabilities))
		for uri := range acc.RawCapabilities {
			list = append(list, string(uri))
		}
		caps[string(id)] = list
	}

	return st.WithTx(ctx, func(tx *sql.Tx) error {
		return store.UpsertAccounts(ctx, tx, accounts, caps)
	})
}

// cachedMetadata 在重新发现失败时兜底。
//
// 只填 issuer: 恢复会话走的是刷新令牌, oauth2 只需要 TokenEndpoint,
// 而端点路径因授权服务器而异, 猜不得。TokenEndpoint 为空时刷新会失败并
// 明确要求重新登录, 好过拿一个拼错的地址反复重试。
func cachedMetadata(cfg config.Config) *auth.Metadata {
	return &auth.Metadata{Issuer: cfg.Issuer}
}

// runBodyTextBackfill 把旧缓存里存成 HTML 源码的 body_text 重写成纯文本。整库只跑一次。
// 延后执行: 它会重建对应的 FTS 行, 不该和刚连上时的首轮同步抢写锁。
func (a *App) runBodyTextBackfill(ctx context.Context, st *store.Store) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(30 * time.Second):
	}
	n, err := st.BackfillBodyText(ctx)
	if err != nil {
		a.log.Warn("backfill body text", "err", err)
		return
	}
	if n > 0 {
		a.log.Info("rewrote html body text", "emails", n)
	}
}

// reconcileInterval 两次邮件对账的最小间隔。增量同步已经处理删除, 对账只是兜底, 一天一次足够。
const reconcileInterval = 24 * time.Hour

// runReconcile 启动一分钟后按账号对账, 清掉本地残留的、服务器上早已删除的邮件。
func (a *App) runReconcile(ctx context.Context, s *syncer.Syncer, st *store.Store) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Minute):
	}
	accounts, err := st.Accounts(ctx)
	if err != nil {
		return
	}
	for _, acc := range accounts {
		key := "reconciledAt:" + acc.ID
		if v, _ := st.StringSetting(ctx, key); v != "" {
			if t, err := time.Parse(time.RFC3339, v); err == nil && time.Since(t) < reconcileInterval {
				continue
			}
		}
		n, err := s.ReconcileEmails(ctx, acc.ID)
		if err != nil {
			a.log.Warn("reconcile emails", "account", acc.ID, "err", err)
			continue
		}
		_ = st.SetStringSetting(ctx, key, time.Now().Format(time.RFC3339))
		if n > 0 {
			a.emit(EventMailChanged, acc.ID)
			a.refreshTrayUnreadSoon()
		}
	}
}
