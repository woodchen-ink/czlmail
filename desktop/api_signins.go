package main

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/woodchen-ink/czlmail/desktop/internal/accountid"
	"github.com/woodchen-ink/czlmail/desktop/internal/config"
)

// 设置 → 账户里的多个登录: 列出、添加、重新登录、移除 (#15)。
//
// 主登录沿用原来的入口 (SignIn / SignInWithPassword / RetrySession / SignOut), 这里的方法带上登录前缀 id;
// 空 id 也指主登录, 界面因此可以对每个登录用同一套调用。

var (
	// errAlreadySignedIn 同一个服务器上的同一个用户已经登录过。
	errAlreadySignedIn = errors.New("2023 this account is already signed in")
	// errDifferentAccount 重新登录附加登录时换成了另一个用户: 前缀下缓存的是原来那个人的数据。
	errDifferentAccount = errors.New("2024 this sign-in belongs to a different account; add the account as a new sign-in instead")
	// errUnknownSignIn 界面传来的登录前缀不存在 (可能刚被移除)。
	errUnknownSignIn = errors.New("2021 unknown sign-in")
)

// SignInInfo 是设置 → 账户里一个登录的状态。
type SignInInfo struct {
	// ID 是登录前缀, 主登录为空。
	ID         string `json:"id"`
	Primary    bool   `json:"primary"`
	Server     string `json:"server"`
	Username   string `json:"username"`
	AuthMethod string `json:"authMethod"`
	// Connected 表示会话已建立; Restoring 表示启动时正在连。都为假且 SignInRequired 为假时是离线, 在退避重试。
	Connected bool `json:"connected"`
	Restoring bool `json:"restoring"`
	// SignedOut 表示用户退出了这个登录: 不同步, 缓存保留, 可以重新登录或移除。
	SignedOut bool `json:"signedOut"`
	// SignInRequired 表示服务器拒绝了保存的凭据, 这个登录的同步已停。
	SignInRequired bool   `json:"signInRequired"`
	SignInReason   string `json:"signInReason"`
	// Accounts 是这个登录下的本机账号 id (个人与共享)。
	Accounts []string `json:"accounts"`
}

// ListSignIns 列出全部登录, 主登录在前。主登录未配置 (还没登录过) 时不列。
func (a *App) ListSignIns() ([]SignInInfo, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	accounts, err := st.Accounts(a.ctx)
	if err != nil {
		return nil, err
	}
	a.mu.RLock()
	list := a.signInsLocked()
	out := make([]SignInInfo, 0, len(list))
	for _, si := range list {
		if !si.cfg.Configured() && si.client == nil {
			continue
		}
		info := SignInInfo{
			ID: si.id, Primary: si.id == accountid.Primary,
			Server: si.cfg.ServerHost, Username: si.cfg.Username, AuthMethod: si.cfg.AuthMethod,
			Connected: si.client != nil, Restoring: si.restoring.Load(), SignedOut: si.signedOut,
			SignInRequired: si.reason != "", SignInReason: si.reason,
			Accounts: []string{},
		}
		for _, acc := range accounts {
			if prefix, _ := accountid.Split(acc.ID); prefix == si.id {
				info.Accounts = append(info.Accounts, acc.ID)
			}
		}
		out = append(out, info)
	}
	a.mu.RUnlock()
	return out, nil
}

// AddSignInWithPassword 用应用专用密码添加一个登录, 返回它的前缀。
func (a *App) AddSignInWithPassword(mailHost, username, password string) (string, error) {
	return a.addSignIn(func(si *signIn) error { return a.passwordSignIn(si, true, mailHost, username, password) })
}

// AddSignInOAuth 用浏览器授权添加一个登录, 返回它的前缀。会打开系统浏览器并阻塞到授权完成、取消或超时。
func (a *App) AddSignInOAuth(mailHost, idp, clientID string) (string, error) {
	return a.addSignIn(func(si *signIn) error { return a.oauthSignIn(si, true, mailHost, idp, clientID) })
}

// SignInAgainWithPassword 用应用专用密码重新登录某个登录 (凭据失效、密码换了)。
func (a *App) SignInAgainWithPassword(id, mailHost, username, password string) error {
	si, err := a.signInByID(id)
	if err != nil {
		return err
	}
	return a.passwordSignIn(si, false, mailHost, username, password)
}

// SignInAgainOAuth 用浏览器授权重新登录某个登录。
func (a *App) SignInAgainOAuth(id, mailHost, idp, clientID string) error {
	si, err := a.signInByID(id)
	if err != nil {
		return err
	}
	return a.oauthSignIn(si, false, mailHost, idp, clientID)
}

// RetrySignIn 用已保存的凭据再连一次某个登录。
func (a *App) RetrySignIn(id string) error {
	if id == accountid.Primary {
		return a.RetrySession()
	}
	si, err := a.signInByID(id)
	if err != nil {
		return err
	}
	f, err := config.LoadFile()
	if err != nil {
		return err
	}
	cfg, ok := f.SignIn(id)
	if !ok || !cfg.Configured() {
		return errUnknownSignIn
	}
	return a.restoreSession(si, cfg)
}

// SignOutOf 退出某个登录 (空 id 是主登录, 同 SignOut)。别的登录照常同步, 这个登录的缓存保留, 以后可以重新登录。
func (a *App) SignOutOf(id string) error {
	si, err := a.signInByID(id)
	if err != nil {
		return err
	}
	return a.signOutOf(si)
}

// signOutOf 停掉一个登录的同步并删掉它在钥匙串里的凭据, 本地缓存保留。
//
// 主登录在没有别的登录时照旧清掉配置 (界面回到登录页); 其余情况配置留着并标上 SignedOut,
// 启动时不连、不提示登录失效, 设置 → 账户里可以重新登录 (同一个用户) 或移除。
func (a *App) signOutOf(si *signIn) error {
	if si.id == accountid.Primary {
		// 先撤推送: 要用到还在的会话 (iOS; 其它平台为空)。
		a.stopPush()
	}
	a.mu.Lock()
	si.stopLocked()
	si.reason = ""
	others := len(a.extra)
	clear := si.id == accountid.Primary && others == 0
	if clear {
		si.cfg = config.Config{}
		si.signedOut = false
	} else {
		si.signedOut = true
		si.cfg.SignedOut = true
	}
	a.mu.Unlock()

	// 钥匙串条目按配置文件里的用户名存 (登录时输入的写法), 内存里的已换成服务器返回的写法。
	var saved config.Config
	err := config.UpdateFile(func(f *config.File) error {
		var ok bool
		saved, ok = f.SignIn(si.id)
		if clear {
			f.Config = config.Config{}
			return nil
		}
		if !ok {
			// 正在添加、还没落盘, 或刚被移除: 不能把它当成已退出的登录写回配置。
			return nil
		}
		c := saved
		c.SignedOut = true
		return f.SetSignIn(si.id, c)
	})
	errs := deleteCredentials(si.id, saved)
	if si.id == accountid.Primary {
		// 系统里登记的日程提醒 (移动端) 跟着清掉: 退出后不该再替这个账号弹通知。
		a.planRemindersSoon(true)
	}
	a.log.Info("signed out", "signin", si.id, "others", others)
	if !clear {
		a.emit(EventAccountsChanged)
		a.refreshTrayUnreadSoon()
	}
	return errors.Join(append(errs, err)...)
}

// RemoveSignIn 移除一个附加登录及其本机缓存与凭据。主登录用 SignOut。
func (a *App) RemoveSignIn(id string) error {
	if _, err := a.signInByID(id); err != nil {
		return err
	}
	err := a.removeSignIn(id)
	if _, e := a.settleSignedOutPrimary(); e != nil {
		err = errors.Join(err, e)
	}
	return err
}

// settleSignedOutPrimary 在只剩一个已退出的主登录 (其余登录都移除了) 时回到旧版退出登录后的状态:
// 主登录的配置清空, 界面回登录页, 缓存照旧保留。否则配置还在、什么都连不上, 启动后停在一个空转的邮件页。
// 报告是否做了清理。
func (a *App) settleSignedOutPrimary() (bool, error) {
	a.mu.Lock()
	if len(a.extra) > 0 || !a.primary.cfg.SignedOut {
		a.mu.Unlock()
		return false, nil
	}
	a.primary.cfg = config.Config{}
	a.primary.signedOut = false
	a.mu.Unlock()
	a.log.Info("only a signed-out primary sign-in is left, clearing its config")
	return true, config.UpdateFile(func(f *config.File) error {
		if len(f.SignIns) == 0 && f.Config.SignedOut {
			f.Config = config.Config{}
		}
		return nil
	})
}

func (a *App) signInByID(id string) (*signIn, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if si := a.signInLocked(id); si != nil {
		return si, nil
	}
	return nil, errUnknownSignIn
}

// addSignIn 分配一个新前缀并用 run 登录; 失败时撤掉这个登录与它可能已写进缓存的数据, 前缀作废。
func (a *App) addSignIn(run func(si *signIn) error) (string, error) {
	var id string
	if err := config.UpdateFile(func(f *config.File) error {
		id = f.ReserveSignIn()
		return nil
	}); err != nil {
		return "", err
	}
	si := &signIn{id: id}
	// 先登记再连: 连上后同步回调里的账号 id 立刻就能被 route 找到。
	a.mu.Lock()
	a.extra = append(a.extra, si)
	a.mu.Unlock()
	if err := run(si); err != nil {
		a.mu.Lock()
		old := si.stopLocked()
		a.extra = slices.DeleteFunc(a.extra, func(x *signIn) bool { return x == si })
		a.mu.Unlock()
		if old != nil {
			old.Close()
		}
		if st, e := a.currentStore(); e == nil {
			if e := a.forgetSignInData(st, id); e != nil {
				a.log.Warn("clean up failed sign-in", "signin", id, "err", e)
			}
		}
		return "", err
	}
	a.log.Info("sign-in added", "signin", id)
	a.emit(EventAccountsChanged)
	return id, nil
}

// checkSignInTarget 在连服务器之前检查要登录的身份: 新登录不能与已有的重复, 重新登录附加登录不能换人。
// 这里比的是用户输入的用户名; 服务器返回的写法 (别名、大小写) 由 acceptIdentity 在连上之后再比一次。
func (a *App) checkSignInTarget(si *signIn, isNew bool, sessionEndpoint, username string) error {
	f, err := config.LoadFile()
	if err != nil {
		return err
	}
	if id, found := f.Find(sessionEndpoint, username); found && (isNew || id != si.id) {
		return errAlreadySignedIn
	}
	if !isNew && si.id != accountid.Primary {
		prev, _ := f.SignIn(si.id)
		if prev.Configured() && !sameIdentity(prev, sessionEndpoint, username) {
			return errDifferentAccount
		}
	}
	return nil
}

// acceptIdentity 在会话建立后、开始同步前确认登录到的身份 (见 connect)。typed 是用户输入的用户名, 浏览器授权时为空。
func (a *App) acceptIdentity(si *signIn, isNew bool, typed string) func(config.Config) error {
	return func(cfg config.Config) error {
		a.mu.RLock()
		others := a.signInsLocked()
		a.mu.RUnlock()
		for _, other := range others {
			if other == si {
				continue
			}
			a.mu.RLock()
			oc := other.cfg
			a.mu.RUnlock()
			if oc.Configured() && (sameIdentity(oc, cfg.SessionEndpoint, cfg.Username) || (typed != "" && sameIdentity(oc, cfg.SessionEndpoint, typed))) {
				return errAlreadySignedIn
			}
		}
		if isNew || si.id == accountid.Primary {
			return nil
		}
		f, err := config.LoadFile()
		if err != nil {
			return err
		}
		prev, _ := f.SignIn(si.id)
		if prev.Configured() && !sameIdentity(prev, cfg.SessionEndpoint, cfg.Username) &&
			(typed == "" || !sameIdentity(prev, cfg.SessionEndpoint, typed)) {
			return errDifferentAccount
		}
		return nil
	}
}

func sameIdentity(c config.Config, sessionEndpoint, username string) bool {
	return c.SessionEndpoint == sessionEndpoint && strings.EqualFold(c.Username, username)
}

// finishSignIn 把连通了的登录写进配置。主登录写顶层字段, 附加登录写进 signIns。
func (a *App) finishSignIn(si *signIn, isNew bool, cfg config.Config) error {
	cfg.SignedOut = false
	return config.UpdateFile(func(f *config.File) error {
		if !isNew && si.id != accountid.Primary {
			if _, ok := f.SignIn(si.id); !ok {
				// 重新登录的过程中被移除了。
				return fmt.Errorf("%w %q", errUnknownSignIn, si.id)
			}
		}
		f.PutSignIn(si.id, cfg)
		return nil
	})
}
