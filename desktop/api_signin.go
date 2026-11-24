package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/woodchen-ink/czlmail/desktop/internal/auth"
	"github.com/woodchen-ink/czlmail/desktop/internal/config"
)

// 登录流程, 两种方式:
//
//   - 应用专用密码(主入口): 服务器 + 邮箱 + App Password, 走 Basic 认证。
//     任何 JMAP 服务器都支持; 在账号走外部 SSO 的部署上, 邮件服务器手里根本
//     没有用户的主密码, 它只认本地签发的应用专用密码 —— OAuth 登录页里要填的
//     也是同一个东西, 绕一趟浏览器没有增加任何安全性, 只增加了失败点。
//   - OAuth(次要入口): 下文所述, 适合有内部账号且开了动态注册的服务器。
//
// OAuth 方式用户只提供邮件服务器地址, 其余全部自动发现。
//
// 授权服务器可能是邮件服务器自己, 也可能是它委托的外部 IdP(Stalwart 的 OIDC
// 后端模式只校验令牌, 不会把用户重定向过去)。去哪里授权由邮件服务器的
// RFC 9728 资源元数据决定, client_id 由 RFC 7591 动态注册获得。
//
// 刻意不在界面上提供"填 IdP 地址 / 填 client_id"的入口: 那是运营方的配置,
// 让每个终端用户去填, 等于把服务端没配好的代价转嫁给最不了解它的人。
// 发现链上缺了哪一环, 就报一个能直接转述给管理员的错误。

// ProbeResult 是对一个服务器地址的探测结果, 供登录界面决定显示什么。
type ProbeResult struct {
	// Issuer 是该地址上的授权服务器标识。
	Issuer string `json:"issuer"`
	// SupportsRegistration 为假时无法动态注册客户端, 登录不可能成功。
	SupportsRegistration bool `json:"supportsRegistration"`
	// Scopes 是实际会申请的权限, 展示给用户看清楚授权范围。
	Scopes []string `json:"scopes"`
}

// ProbeServer 检查某个地址是否提供 OAuth 授权服务。
//
// 登录界面用它给出即时反馈, 而不是等用户点了登录、浏览器弹出来、
// 再发现地址填错了。
func (a *App) ProbeServer(host string) (*ProbeResult, error) {
	meta, err := auth.DiscoverForResource(a.ctx, host)
	if err != nil {
		return nil, err
	}
	return &ProbeResult{
		Issuer:               meta.Issuer,
		SupportsRegistration: meta.RegistrationEndpoint != "",
		Scopes:               meta.Scopes(),
	}, nil
}

// SignIn 走 OAuth 授权码流程登录。
//
// idp 与 clientID 为空时按邮件服务器公布的授权服务器走(需要支持动态注册)。
// 邮件服务器把认证交给外部身份提供商(如 Stalwart 的 OIDC 目录)时, 它自己的授权页只认本地密码,
// 不会跳转到外部 IdP; 这时要直接向 IdP 授权, 由邮件服务器校验 IdP 签发的令牌 —— 与 Bulwark 相同。
// IdP 通常不开放动态注册, 客户端 ID 需要管理员在 IdP 上创建公开客户端后提供。
//
// 本方法会打开系统浏览器并阻塞到用户完成授权、取消或超时。
func (a *App) SignIn(mailHost, idp, clientID string) error {
	return a.oauthSignIn(&a.primary, false, mailHost, idp, clientID)
}

// oauthSignIn 为一个登录走 OAuth 授权: 主登录、重新登录某个附加登录, 或 isNew 时添加新的附加登录。
func (a *App) oauthSignIn(si *signIn, isNew bool, mailHost, idp, clientID string) error {
	if mailHost == "" {
		return fmt.Errorf("2050 mail server address is required")
	}

	sessionEndpoint, err := auth.SessionEndpoint(mailHost)
	if err != nil {
		return err
	}

	ctx, cancel, gen := a.beginSignIn(si)
	// 无论成功失败都要释放 context, 否则每次登录都漏一个。
	defer cancel()
	defer a.endSignIn(si, gen)

	idp, clientID = strings.TrimSpace(idp), strings.TrimSpace(clientID)
	if (idp == "") != (clientID == "") {
		return fmt.Errorf("2052 identity provider address and client ID must be provided together")
	}
	var meta *auth.Metadata
	if idp != "" {
		meta, err = auth.Discover(ctx, idp)
	} else {
		meta, err = auth.DiscoverForResource(ctx, mailHost)
	}
	if err != nil {
		a.log.Warn("oauth discover failed", "host", mailHost, "idp", idp, "err", err)
		return err
	}
	a.log.Info("oauth discovered", "issuer", meta.Issuer, "registration", meta.RegistrationEndpoint != "")

	listener, redirectURI, err := auth.ListenLoopback()
	if err != nil {
		return err
	}
	defer listener.Close()

	cfg, err := a.oauthStartConfig(si, isNew, meta.Issuer, redirectURI)
	if err != nil {
		return err
	}

	// 复用已注册的客户端: 只有服务器变了或回环端口变了时才重新注册,
	// 否则每次登录都会在服务器上堆一个新的客户端条目。
	if clientID != "" {
		cfg.ClientID = clientID
		cfg.RedirectURI = redirectURI
	} else if cfg.ClientID == "" || cfg.RedirectURI != redirectURI || cfg.Issuer != meta.Issuer {
		info, err := auth.Register(ctx, meta, redirectURI)
		if err != nil {
			a.log.Warn("oauth register failed", "err", err)
			return err
		}
		a.log.Info("oauth client registered", "redirect", redirectURI)
		cfg.ClientID = info.ID
		cfg.RedirectURI = redirectURI
	}

	cfg.ServerHost = mailHost
	cfg.AuthMethod = config.AuthOAuth
	cfg.Issuer = meta.Issuer
	cfg.SessionEndpoint = sessionEndpoint
	cfg.Scopes = meta.Scopes()

	tok, err := auth.Authorize(ctx, &auth.Config{
		Metadata:    meta,
		ClientID:    cfg.ClientID,
		RedirectURI: cfg.RedirectURI,
		Scopes:      cfg.Scopes,
		Text:        a.t,
	}, listener)
	if err != nil {
		a.log.Warn("oauth authorize failed", "err", err)
		return err
	}
	a.log.Info("oauth token received", "refreshToken", tok.RefreshToken != "", "expiry", tok.Expiry)

	// 先连通再落盘, 避免把一组用不了的凭据写进密钥库。
	// 令牌刷新时的落盘回调要等登录登记好才有意义, 新登录在连通前不存在于配置里, 但钥匙串条目只按前缀与 issuer 命名, 先写无妨。
	httpClient, src := a.oauthClient(si, cfg, meta, tok)
	if err := a.connect(si, cfg, httpClient, src, a.acceptIdentity(si, isNew, "")); err != nil {
		a.log.Warn("oauth session connect failed", "err", err)
		return err
	}

	a.mu.RLock()
	connected := si.cfg
	a.mu.RUnlock()

	if err := a.finishSignIn(si, isNew, connected); err != nil {
		return err
	}
	if err := config.SaveToken(si.id, connected.Issuer, tok); err != nil {
		a.log.Error("oauth save token failed", "err", err)
		return err
	}
	a.log.Info("oauth sign-in complete", "signin", si.id, "username", connected.Username)
	return nil
}

// oauthStartConfig 返回 OAuth 授权起步用的配置: 已有登录用它自己的 (复用注册过的客户端);
// 新登录从空配置开始, 但借用已有登录在同一授权服务器、同一回调地址上注册过的客户端, 免得重复注册。
func (a *App) oauthStartConfig(si *signIn, isNew bool, issuer, redirectURI string) (config.Config, error) {
	f, err := config.LoadFile()
	if err != nil {
		return config.Config{}, err
	}
	if !isNew {
		cfg, _ := f.SignIn(si.id)
		return cfg, nil
	}
	for _, c := range append([]config.Config{f.Config}, signInConfigs(f.SignIns)...) {
		if c.Issuer == issuer && c.RedirectURI == redirectURI && c.ClientID != "" {
			return config.Config{Issuer: c.Issuer, ClientID: c.ClientID, RedirectURI: c.RedirectURI}, nil
		}
	}
	return config.Config{}, nil
}

func signInConfigs(list []config.SignIn) []config.Config {
	out := make([]config.Config, 0, len(list))
	for _, s := range list {
		out = append(out, s.Config)
	}
	return out
}

// CancelSignIn 中止正在进行的登录。
//
// 必须有这个出口: 授权流程会等用户在浏览器里操作, 上限数分钟。用户关掉浏览器
// 标签页、或者发现服务器地址填错了, 没有取消的话界面会一直卡在等待里。
func (a *App) CancelSignIn() {
	var cancels []context.CancelFunc
	a.mu.Lock()
	for _, si := range a.signInsLocked() {
		if si.cancelFlow != nil {
			cancels = append(cancels, si.cancelFlow)
			si.cancelFlow = nil
		}
	}
	a.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
}

// beginSignIn 为这个登录登记一次登录流程的取消句柄, 同时中止它上一次未结束的流程。
//
// 返回的代次用于收尾时判断句柄是否仍属于本次流程。被中止的旧流程随后也会走到
// endSignIn, 不比对代次的话它会把刚登记的新句柄擦掉, 导致新流程无法取消。
func (a *App) beginSignIn(si *signIn) (context.Context, context.CancelFunc, uint64) {
	ctx, cancel := context.WithCancel(a.ctx)

	a.mu.Lock()
	var previous []context.CancelFunc
	// 同一时刻只跑一个授权流程, 不论属于哪个登录: 回环端口与注册过的回调地址是共用的。
	for _, other := range append(a.signInsLocked(), si) {
		if other.cancelFlow != nil {
			previous = append(previous, other.cancelFlow)
			other.cancelFlow = nil
		}
	}
	si.flowGen++
	gen := si.flowGen
	si.cancelFlow = cancel
	a.mu.Unlock()

	// 用户改了地址重新点登录时, 上一次还挂着的流程要先停掉,
	// 否则两个回环监听器会抢同一个端口。
	for _, p := range previous {
		p()
	}
	return ctx, cancel, gen
}

// endSignIn 只清理仍属于本次流程的句柄。
func (a *App) endSignIn(si *signIn, gen uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if si.flowGen == gen {
		si.cancelFlow = nil
	}
}

// SignInWithPassword 用邮箱与应用专用密码登录。
func (a *App) SignInWithPassword(mailHost, username, password string) error {
	return a.passwordSignIn(&a.primary, false, mailHost, username, password)
}

// passwordSignIn 用应用专用密码登录: 主登录、重新登录某个附加登录, 或 isNew 时添加新的附加登录。
func (a *App) passwordSignIn(si *signIn, isNew bool, mailHost, username, password string) error {
	if mailHost == "" || username == "" || password == "" {
		return fmt.Errorf("2051 server, email and app password are all required")
	}

	sessionEndpoint, err := auth.SessionEndpoint(mailHost)
	if err != nil {
		return err
	}

	cfg := config.Config{
		ServerHost:      mailHost,
		AuthMethod:      config.AuthPassword,
		SessionEndpoint: sessionEndpoint,
		Username:        username,
	}

	if err := a.checkSignInTarget(si, isNew, sessionEndpoint, username); err != nil {
		return err
	}

	// 先连通再落盘, 避免把错的密码写进密钥库。
	if err := a.connect(si, cfg, basicAuthClient(username, password), nil, a.acceptIdentity(si, isNew, username)); err != nil {
		return err
	}

	// 密钥库条目按登录时输入的用户名存, 而不是服务器返回的 username:
	// 两者大小写或别名可能不同, 恢复会话时手里只有配置里的这个值。
	a.mu.Lock()
	si.cfg.Username = username
	saved := si.cfg
	a.mu.Unlock()

	if err := a.finishSignIn(si, isNew, saved); err != nil {
		return err
	}
	return config.SavePassword(sessionEndpoint, username, password)
}
