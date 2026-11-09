package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/auth"
	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/notify"
)

// 本文件处理"登录失效": 应用专用密码被撤销、OAuth 令牌续不了期、服务器不再认已保存的凭据。
//
// 判定标准只有一条 —— 服务器明确拒绝了凭据(会话地址回 401, 或授权服务器拒绝续期)。
// 断网、超时、5xx、403 都不算: 那些过一会儿自己会好, 同步循环照常退避重连。
// 确认失效后停掉同步(继续重试只会触发服务器的防爆破封禁)、提示用户; 配置、凭据、缓存库都不动,
// 重新登录同一账号后接着用。共享账号与个人账号共用一份凭据, 所以失效是整个会话的事, 不分账号。

// credentialError 表示服务器明确拒绝了凭据, 重试无用, 需要用户重新登录。
type credentialError struct{ msg string }

func (e *credentialError) Error() string { return e.msg }

func isCredentialError(err error) bool {
	var c *credentialError
	return errors.As(err, &c)
}

// errNoStoredCredential 配置还在但密钥库里的凭据没了(被用户或系统清掉)。
var errNoStoredCredential = &credentialError{"2020 no stored credential"}

// signInActionID 是"登录已失效"系统通知的动作标识, 点击只唤出窗口。
const signInActionID = "signin:"

// probeSession 向会话地址发一次请求并归类结果, 会话可用时返回 nil。
func (a *App) probeSession(cfg config.Config, httpClient *http.Client) error {
	ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.SessionEndpoint, nil)
	if err != nil {
		return fmt.Errorf("2062 invalid server address: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		if auth.TokenRejected(err) {
			return &credentialError{"2065 sign-in expired: the stored token can no longer be renewed"}
		}
		return fmt.Errorf("2062 cannot reach mail server: %w", err)
	}
	resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized:
		if cfg.AuthMethod == config.AuthOAuth {
			// 邮件服务器把认证委托给外部 IdP 时只认 IdP 的令牌, 自己签发的令牌会被拒。
			return &credentialError{"2064 the mail server rejected the OAuth token (HTTP 401); sign in with an app password instead"}
		}
		return &credentialError{"2060 authentication failed: check the email address and app password (an SSO password will not work here)"}
	case http.StatusForbidden:
		// 登录时按凭据问题提示, 但不当作"登录失效": 403 也可能来自前置的防火墙或被禁用的权限。
		if cfg.AuthMethod == config.AuthOAuth {
			return fmt.Errorf("2064 the mail server rejected the OAuth token (HTTP 403); sign in with an app password instead")
		}
		return fmt.Errorf("2060 authentication failed: check the email address and app password (an SSO password will not work here)")
	case http.StatusNotFound:
		return fmt.Errorf("2061 no JMAP service found at this server address")
	default:
		return fmt.Errorf("2063 mail server returned status %d", resp.StatusCode)
	}
}

// 启动时恢复会话的重试间隔。
const (
	restoreMinDelay = 5 * time.Second
	restoreMaxDelay = 5 * time.Minute
)

// restoreWithRetry 在启动时恢复会话。凭据被拒就提示重新登录; 其它失败(开机时网络还没好、
// 服务器在维护)退避重试, 直到连上 —— 否则离线启动的程序要等到重启才会开始同步。
func (a *App) restoreWithRetry(cfg config.Config) {
	delay := restoreMinDelay
	for {
		err := a.restoreSession(cfg)
		if err == nil {
			return
		}
		if isCredentialError(err) {
			a.requireSignIn(nil, err)
			return
		}
		a.log.Warn("restore session failed, will retry", "err", err, "retry_in", delay)

		select {
		case <-a.ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay *= 2; delay > restoreMaxDelay {
			delay = restoreMaxDelay
		}

		// 等待期间用户可能已经自己登录、退出或换了账号, 这时不该再拿旧配置去连。
		a.mu.RLock()
		superseded := a.client != nil || a.signInReason != "" ||
			a.cfg.SessionEndpoint != cfg.SessionEndpoint || a.cfg.AuthMethod != cfg.AuthMethod
		a.mu.RUnlock()
		if superseded {
			return
		}
	}
}

// suspectCredentials 在某个请求出现凭据失效的迹象时核实一次, 确认后才要求重新登录。
//
// 由请求协程调用, 核实放到后台。OAuth 先强制续期: 令牌可能只是被服务端提前作废,
// 刷新令牌还能用; 续期也被拒才算失效。
func (a *App) suspectCredentials(client *jmap.Client, cfg config.Config, httpClient *http.Client, tokens *auth.TokenSource) {
	a.mu.RLock()
	current := a.client == client
	a.mu.RUnlock()
	// 登录过程中的 401(密码填错)由登录流程自己报错, 不归这里管。
	if !current || !a.verifying.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer a.verifying.Store(false)
		if tokens != nil {
			tokens.MarkStale()
		}
		err := a.probeSession(cfg, httpClient)
		if isCredentialError(err) {
			a.requireSignIn(client, err)
			return
		}
		a.log.Info("credentials still accepted after a rejected request", "probe_err", err)
	}()
}

// requireSignIn 停掉同步并提示用户重新登录。client 非空时只在它仍是当前会话时生效。
func (a *App) requireSignIn(client *jmap.Client, reason error) {
	a.mu.Lock()
	if client != nil && a.client != client {
		a.mu.Unlock()
		return
	}
	if a.cancelSync != nil {
		a.cancelSync()
		a.cancelSync = nil
	}
	a.client = nil
	a.sync = nil
	first := a.signInReason == ""
	a.signInReason = reason.Error()
	username := a.cfg.Username
	n := a.notifier
	a.mu.Unlock()

	a.log.Warn("credentials rejected, sign-in required", "reason", reason)
	a.emit(EventSignInRequired, reason.Error())

	// 窗口多半缩在托盘里, 只在界面上提示用户看不到。
	if first && n != nil {
		_ = n.Notify(a.ctx, notify.Notification{
			Title:    "登录已失效",
			Body:     username + " 需要重新登录后才能继续收发邮件",
			ActionID: signInActionID,
		})
	}
}

// RetrySession 用已保存的凭据再连一次。供"登录已失效"提示里的重试用:
// 服务器迁移、维护期间的拒绝可能是暂时的。
func (a *App) RetrySession() error {
	// 从磁盘读配置: 内存里的用户名已换成服务器返回的写法, 与密钥库条目名可能不一致。
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if !cfg.Configured() {
		return errNotSignedIn
	}
	return a.restoreSession(cfg)
}
