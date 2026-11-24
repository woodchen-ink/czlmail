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

	"github.com/woodchen-ink/czlmail/desktop/internal/accountid"
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
	notes    noteState
	opens    openQueue
	prefetch prefetcher
	mcp      mcpState
	images   imageCache
	// firstStatus 只记一次前端首次查询会话的耗时。
	firstStatus sync.Once
	ctx         context.Context
	log         *slog.Logger

	mu    sync.RWMutex
	store *store.Store
	// primary 是主登录, extra 是其余登录, 按添加顺序 (见 signins.go)。
	primary signIn
	extra   []*signIn

	notifier notify.Notifier
	// uiLocale 是界面当前语言 (SetUILocale), 用于后端自己发出的通知文案。
	uiLocale string
	// reminders 是移动端交给系统的定时日程提醒 (reminders_schedule.go)。
	reminders reminderSchedule

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
	file, err := config.LoadFile()
	if err != nil {
		a.log.Error("load config", "err", err)
	}
	a.mu.Lock()
	a.primary.cfg = file.Config
	for _, s := range file.SignIns {
		a.extra = append(a.extra, &signIn{id: s.ID, cfg: s.Config})
	}
	signIns := a.signInsLocked()
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
	a.log.Info("startup done", "since_start", time.Since(processStart).Round(time.Millisecond))

	if !file.Configured() {
		a.log.Info("not configured yet, waiting for sign-in")
	}

	// 已有凭据就静默恢复会话。放后台是因为它要打网络,
	// 挡在这里会让窗口迟迟不出现。
	for _, si := range signIns {
		if si.cfg.SignedOut {
			// 用户退出的登录: 凭据已删, 不连, 也不提示登录失效。
			a.mu.Lock()
			si.signedOut = true
			a.mu.Unlock()
			continue
		}
		if si.cfg.Configured() {
			go a.restoreWithRetry(si, si.cfg)
		}
	}
}

// processStart 记进程启动时刻, 启动各阶段的日志带上距此的耗时, 排查白屏用。
var processStart = time.Now()

func (a *App) domReady(ctx context.Context) {
	a.log.Info("dom ready", "since_start", time.Since(processStart).Round(time.Millisecond))
}

func (a *App) shutdown(ctx context.Context) {
	a.stopTray()
	a.stopMCP()

	a.mu.Lock()
	defer a.mu.Unlock()

	for _, si := range a.signInsLocked() {
		if si.cancelSync != nil {
			si.cancelSync()
		}
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

// restoreSession 用已保存的凭据重建这个登录的会话, 按登录方式分派。
func (a *App) restoreSession(si *signIn, cfg config.Config) error {
	if cfg.AuthMethod == config.AuthPassword {
		pass, err := config.LoadPassword(cfg.SessionEndpoint, cfg.Username)
		if err != nil {
			return err
		}
		if pass == "" {
			return errNoStoredCredential
		}
		return a.connect(si, cfg, basicAuthClient(cfg.Username, pass), nil, nil)
	}
	return a.restoreOAuthSession(si, cfg)
}

func (a *App) restoreOAuthSession(si *signIn, cfg config.Config) error {
	tok, err := config.LoadToken(si.id, cfg.Issuer)
	if err != nil {
		return err
	}
	if tok == nil {
		return errNoStoredCredential
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
	httpClient, src := a.oauthClient(si, cfg, meta, tok)
	return a.connect(si, cfg, httpClient, src, nil)
}

// oauthClient 构造自动续期的 Bearer 客户端。一并返回令牌源, 供服务器拒绝令牌时强制续期。
func (a *App) oauthClient(si *signIn, cfg config.Config, meta *auth.Metadata, tok *oauth2.Token) (*http.Client, *auth.TokenSource) {
	authCfg := &auth.Config{
		Metadata:    meta,
		ClientID:    cfg.ClientID,
		RedirectURI: cfg.RedirectURI,
		Scopes:      cfg.Scopes,
	}
	// 令牌刷新时同步落盘 —— 服务器可能启用刷新令牌轮换, 不存新值会导致
	// 应用重启后只剩一个已作废的令牌。
	save := func(t *oauth2.Token) error {
		if err := config.SaveToken(si.id, cfg.Issuer, t); err != nil {
			a.log.Error("persist refreshed token", "err", err)
			return err
		}
		return nil
	}
	src := authCfg.TokenSource(a.ctx, tok, save)
	return auth.HTTPClient(a.ctx, src), src
}

// basicAuthClient 构造带 Basic 认证的客户端。
func basicAuthClient(username, password string) *http.Client {
	c := &jmap.Client{}
	c.WithBasicAuth(username, password)
	return c.HttpClient
}

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

// connect 用已认证的 HTTP 客户端为这个登录建立 JMAP 会话并启动后台同步。
// tokens 是 OAuth 的令牌源, 密码登录传 nil。accept 非空时在会话建立后、开始同步前检查登录到的身份,
// 返回错误则放弃这次会话 (重复登录同一个人、重新登录时换了人), 缓存里不会留下它的数据。
func (a *App) connect(si *signIn, cfg config.Config, httpClient *http.Client, tokens *auth.TokenSource, accept func(config.Config) error) error {
	a.waitReady(30 * time.Second)
	if _, err := a.currentStore(); err != nil {
		return err
	}
	client := &jmap.Client{SessionEndpoint: cfg.SessionEndpoint}
	// 会话建立后的每个请求(同步、推送、附件)都经过这层: 凭据中途失效时没有哪一处会主动报告,
	// 同步循环只会当成断线一直重连。
	client.HttpClient = auth.Watch(httpClient, func() {
		a.suspectCredentials(si, client, cfg, httpClient, tokens)
	})
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
	if accept != nil {
		if err := accept(cfg); err != nil {
			return err
		}
	}

	// 换了人登录就先清掉上一个人的缓存, 再开始同步 (见 identity.go)。
	// 只有主登录需要: 附加登录的前缀从不复用, 缓存里不会有上一个人留下的同前缀数据。
	if st, err := a.currentStore(); err == nil && si.id == accountid.Primary {
		if err := a.ensureCacheOwner(a.ctx, st, cfg, client); err != nil {
			return fmt.Errorf("2003 reset local cache for new account: %w", err)
		}
	}

	a.mu.Lock()
	// 认证期间登录被删掉了: 放弃这次连接, 不启动同步、不写账号缓存。
	if si.removed {
		a.mu.Unlock()
		return errNotSignedIn
	}
	st := a.store
	if si.cancelSync != nil {
		si.cancelSync()
	}
	si.client = client
	cfg.SignedOut = false
	si.cfg = cfg
	si.reason = ""
	si.signedOut = false
	s := syncer.New(client, st, a.log)
	s.SetSignIn(si.id)
	s.OnNewMail = a.onNewMail
	s.OnChanged = func(accountID string) {
		a.emit(EventMailChanged, accountID)
		a.refreshTrayUnreadSoon()
		a.prefetchRecent(accountID)
	}
	s.OnRound = func(round syncer.RoundStatus) {
		a.emit(EventSyncStatus, a.mailSyncStatusWith(s, round))
	}
	s.OnPIMChanged = func(accountID, typeName string) {
		if typeName == "" || typeName == "FileNode" {
			a.wakeNotes(true)
		}
		a.emit(EventPIMChanged, PIMChange{AccountID: accountID, Type: typeName})
		if typeName == "" || typeName == "Calendar" || typeName == "CalendarEvent" {
			a.planRemindersSoon(false)
		}
		// 模板与 AI/MCP 设置存在个人网盘里; 网盘有变化(含首次同步)时合并一次。
		if (typeName == "" || typeName == "FileNode") && accountID == a.personalAccountID(st) {
			a.syncDriveSoon()
		}
	}
	si.sync = s

	// 同步循环的生命周期跟随应用而不是跟随某次调用, 因此从 a.ctx 派生。
	syncCtx, cancel := context.WithCancel(a.ctx)
	s.OnAccountsChanged = func(diff syncer.AccountDiff) { a.onAccountsChanged(syncCtx, si, client, st, diff) }
	si.cancelSync = cancel
	a.mu.Unlock()

	if err := a.persistAccounts(syncCtx, si, client, st); err != nil {
		a.log.Error("persist accounts", "err", err)
	}
	// 程序关着时被撤销的共享: 缓存里有、会话里没有。这里只记下, 之后的重读会话确认了再删。
	s.PruneAccounts(syncCtx)

	go a.runReconcile(syncCtx, si, s, st)
	// 笔记、提醒、正文回填与 iOS 推送目前只跟主登录走: 笔记与网盘设置在主登录的个人网盘里,
	// 提醒与回填覆盖整个缓存库, 推送登记暂时只有一份 (附加登录的推送见 #15 后续)。
	if si.id == accountid.Primary {
		go a.runNotes(syncCtx, s)
		go a.runReminders(syncCtx)
		go a.runBodyTextBackfill(syncCtx, st)
		// 先恢复「推送已生效」的标记, 第一轮同步据此不重复弹推送已经通知过的新信。
		a.primePush(syncCtx, st)
		go a.runPush(syncCtx, s, st)
	}
	go func() {
		if err := s.Run(syncCtx); err != nil {
			a.log.Error("sync loop exited", "err", err)
		}
	}()

	a.log.Info("connected", "signin", si.id, "username", cfg.Username, "accounts", len(client.Session.Accounts))
	a.emit(EventConnected, cfg.Username)
	return nil
}

// classifyAuthFailure 把会话认证失败转成用户能看懂原因的错误。
//
// go-jmap 对任何非 200 响应都只返回 "couldn't authenticate", 分不出是密码错、
// 地址错, 还是服务器不可达。这三种情况用户的补救动作完全不同, 因此失败后
// 再发一次请求拿到真实状态码。最常见的是账号走 SSO 却填了 SSO 密码。
func (a *App) classifyAuthFailure(cfg config.Config, httpClient *http.Client, cause error) error {
	if err := a.probeSession(cfg, httpClient); err != nil {
		return err
	}
	return fmt.Errorf("2063 mail server session could not be read: %w", cause)
}

// persistAccounts 把会话里发现的账号写进缓存, 供界面在离线时也能列出账号。
// 缓存里存本机账号 id (带这个登录的前缀)。
func (a *App) persistAccounts(ctx context.Context, si *signIn, client *jmap.Client, st *store.Store) error {
	accounts := make([]store.Account, 0, len(client.Session.Accounts))
	caps := make(map[string][]string, len(client.Session.Accounts))

	for id, acc := range client.Session.Accounts {
		local := accountid.Join(si.id, string(id))
		accounts = append(accounts, store.Account{
			ID:         local,
			Name:       acc.Name,
			IsPersonal: acc.IsPersonal,
		})
		list := make([]string, 0, len(acc.RawCapabilities))
		for uri := range acc.RawCapabilities {
			list = append(list, string(uri))
		}
		caps[local] = list
	}

	return st.WithTx(ctx, func(tx *sql.Tx) error {
		return store.UpsertAccounts(ctx, tx, accounts, caps)
	})
}

// onAccountsChanged 在运行中重读会话发现账号变化时更新缓存并通知界面。
// 失去访问的账号缓存已由 syncer 删掉; 新账号的邮件随后由 syncer 引导, 落库后照常发 mail:changed。
// ctx 是这次会话的同步上下文: 退出登录后迟到的回调不再写库。
func (a *App) onAccountsChanged(ctx context.Context, si *signIn, client *jmap.Client, st *store.Store, diff syncer.AccountDiff) {
	if ctx.Err() != nil {
		return
	}
	if err := a.persistAccounts(ctx, si, client, st); err != nil {
		a.log.Error("persist accounts", "err", err)
	}
	a.emit(EventAccountsChanged)
	a.refreshTrayUnreadSoon()
	// 推送订阅按账号列出要推的收件箱, 账号变了要重新对齐 (仅 iOS 有实现)。
	a.pushSettingsChanged()
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

// runReconcile 启动一分钟后按账号对账, 清掉本地残留的、服务器上早已删除的邮件。只对账这个登录的账号。
func (a *App) runReconcile(ctx context.Context, si *signIn, s *syncer.Syncer, st *store.Store) {
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
		if prefix, _ := accountid.Split(acc.ID); prefix != si.id {
			continue
		}
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
