package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/accountid"
	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// 登录 (sign-in) 是一组服务器地址 + 凭据, 对应一个 JMAP 会话; 一个会话里可以有多个账号 (个人 + 共享)。
//
// 会话的状态 (客户端、同步器、凭据失效、授权流程) 都挂在 signIn 上, 不直接挂在 App 上。
// 主登录之外的登录 (config.json 的 signIns) 在启动时各自恢复会话; 访问服务器的调用一律经 route 按账号 id 找到所属登录与服务器上的账号 id,
// 不自己拼或拆账号 id 的前缀 (格式见 internal/accountid)。

// signIn 是一个登录的运行时状态。除两个原子标记外, 字段都由 App.mu 保护。
type signIn struct {
	// id 是该登录在本机账号 id 里的前缀, 主登录为空。
	id string

	cfg    config.Config
	client *jmap.Client
	sync   *syncer.Syncer
	// cancelSync 停掉这个会话的后台同步循环, 用于重新登录时重建会话。
	cancelSync context.CancelFunc

	// reason 非空表示服务器已明确拒绝保存的凭据, 同步已停, 等用户重新登录。
	// 内容是带错误码的原因, 会话重新建立时清空。
	reason string

	// removed 表示这个登录已被 removeSignIn 删掉。删除时可能还有一次连接在路上 (启动恢复、退避重试),
	// 它认证完回来时据此放弃, 不能把会话、同步循环和账号缓存重新建起来。
	removed bool

	// cancelFlow 中止这个登录正在进行的浏览器授权流程。
	// flowGen 标识当前流程, 使收尾时能分辨句柄归属。
	cancelFlow context.CancelFunc
	flowGen    uint64

	// signedOut 表示用户退出了这个登录 (config.Config.SignedOut), 会话不连, 缓存保留, 等重新登录。
	signedOut bool

	// restoring 在启动时恢复会话的那次连接进行中为 true, 期间需要会话的调用等它连上 (见 route)。
	restoring atomic.Bool
	// verifying 保证同一时刻只有一次"凭据是否真的失效"的核实在跑。
	verifying atomic.Bool
}

// signInLocked 按前缀找登录, 没有时返回 nil。调用方须持有 a.mu。
func (a *App) signInLocked(id string) *signIn {
	if id == accountid.Primary {
		return &a.primary
	}
	for _, si := range a.extra {
		if si.id == id {
			return si
		}
	}
	return nil
}

// signInsLocked 列出全部登录, 主登录在前。调用方须持有 a.mu。
func (a *App) signInsLocked() []*signIn {
	return append([]*signIn{&a.primary}, a.extra...)
}

// primaryConfig 返回主登录的配置。
func (a *App) primaryConfig() config.Config {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.primary.cfg
}

// accountRoute 是某个本机账号 id 落到哪个会话上: 用哪个客户端与同步器, 服务器上的账号 id 是什么。
type accountRoute struct {
	signIn *signIn
	client *jmap.Client
	sync   *syncer.Syncer
	// serverID 是服务器认识的账号 id, 直接拼 JMAP 请求时用它。
	// 同步器的方法照旧收本机账号 id: 缓存库里的行都按本机账号 id 存。
	serverID string
}

// routeWait 是会话正在恢复时 route 等它连上的上限。
const routeWait = 20 * time.Second

// route 把本机账号 id 解析成所属登录的会话与服务器上的账号 id。
// 所有访问服务器的调用 (经同步器或直接发 JMAP 请求) 都从这里拿会话。
//
// 启动时会话在后台恢复 (要打网络, 几秒), 这期间界面已经能操作: 点通知冷启动后立刻打开邮件、
// 刚启动就点同步, 都会先于会话到达。恢复正在进行时等它连上 (最多 20 秒), 而不是直接报未登录;
// 离线退避的间隙里不等, 免得每个操作都卡住。不属于任何登录的账号 id 按未登录处理。
func (a *App) route(accountID string) (accountRoute, error) {
	return a.routeWithin(accountID, routeWait)
}

// routeWithin 同 route, 会话正在恢复时最多等 wait; 传 0 不等。
func (a *App) routeWithin(accountID string, wait time.Duration) (accountRoute, error) {
	prefix, serverID := accountid.Split(accountID)
	deadline := time.Now().Add(wait)
	for {
		a.mu.RLock()
		si := a.signInLocked(prefix)
		var rt accountRoute
		if si != nil {
			rt = accountRoute{signIn: si, client: si.client, sync: si.sync, serverID: serverID}
		}
		a.mu.RUnlock()
		if si == nil {
			return accountRoute{}, errNotSignedIn
		}
		if rt.sync != nil {
			return rt, nil
		}
		if !si.restoring.Load() || time.Now().After(deadline) {
			return accountRoute{}, errNotSignedIn
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// syncerFor 返回某账号所属会话的同步器。
func (a *App) syncerFor(accountID string) (*syncer.Syncer, error) {
	rt, err := a.route(accountID)
	if err != nil {
		return nil, err
	}
	return rt.sync, nil
}

// do 在这个会话上直接发起一次请求, 并把方法错误转成 error。
// 请求里的账号 id 必须是 serverID, 不能是本机账号 id。
func (r accountRoute) do(req *jmap.Request) (*jmap.Response, error) {
	if r.client == nil {
		return nil, errNotSignedIn
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("2133 jmap request: %w", err)
	}
	for _, inv := range resp.Responses {
		if me, ok := inv.Args.(*jmap.MethodError); ok {
			return nil, fmt.Errorf("2134 %s failed: %s", inv.Name, me.Type)
		}
	}
	return resp, nil
}

// stopLocked 停掉这个登录的同步并丢下会话; 配置、凭据与失效原因不动。调用方须持有 a.mu。
func (si *signIn) stopLocked() {
	if si.cancelSync != nil {
		si.cancelSync()
		si.cancelSync = nil
	}
	si.client = nil
	si.sync = nil
}

// usernameFor 返回某账号所属登录的用户名 (登录时服务器给出的写法), 不属于任何登录时为空。
func (a *App) usernameFor(accountID string) string {
	prefix, _ := accountid.Split(accountID)
	a.mu.RLock()
	defer a.mu.RUnlock()
	if si := a.signInLocked(prefix); si != nil {
		return si.cfg.Username
	}
	return ""
}

// signedIn 报告是否至少有一个登录的会话已建立。启动时正在恢复的会话会等它 (同 route)。
func (a *App) signedIn() bool {
	deadline := time.Now().Add(routeWait)
	for {
		connected, restoring := false, false
		a.mu.RLock()
		for _, si := range a.signInsLocked() {
			connected = connected || si.sync != nil
			restoring = restoring || si.restoring.Load()
		}
		a.mu.RUnlock()
		if connected {
			return true
		}
		if !restoring || time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// removeSignIn 删掉一个附加登录: 停掉它的同步, 删掉它的配置、钥匙串里的凭据、账号缓存与按账号记的设置。
// 服务器上的数据不受影响。它的前缀不会再分配给以后的登录 (见 config.File.AddSignIn)。
func (a *App) removeSignIn(id string) error {
	err := a.dropSignIn(id)
	if id != accountid.Primary && !errors.Is(err, errNotSignedIn) {
		a.emit(EventAccountsChanged)
		a.refreshTrayUnreadSoon()
	}
	return err
}

// dropSignIn 是 removeSignIn 中不通知界面的部分。
func (a *App) dropSignIn(id string) error {
	if id == accountid.Primary {
		return fmt.Errorf("2022 the primary sign-in cannot be removed this way")
	}
	a.mu.Lock()
	si := a.signInLocked(id)
	if si == nil {
		a.mu.Unlock()
		return errNotSignedIn
	}
	si.removed = true
	si.stopLocked()
	if si.cancelFlow != nil {
		si.cancelFlow()
		si.cancelFlow = nil
	}
	a.extra = slices.DeleteFunc(a.extra, func(x *signIn) bool { return x == si })
	cfg := si.cfg
	primary := a.primary.cfg
	st := a.store
	a.mu.Unlock()

	// 钥匙串条目按配置文件里的用户名存 (登录时输入的写法), 内存里的已换成服务器返回的写法。
	var errs []error
	if err := config.UpdateFile(func(f *config.File) error {
		if saved, ok := f.RemoveSignIn(id); ok {
			cfg = saved
		}
		return nil
	}); err != nil {
		errs = append(errs, err)
	}
	// 同一个身份不会登录两次 (config.File.Find); 万一配置被手改成那样, 不能连主登录的密码一起删。
	if !(cfg.SessionEndpoint == primary.SessionEndpoint && strings.EqualFold(cfg.Username, primary.Username)) {
		errs = append(errs, deleteCredentials(id, cfg)...)
	}
	if st != nil {
		if err := a.forgetSignInData(st, id); err != nil {
			errs = append(errs, err)
		}
	}
	a.log.Info("sign-in removed", "signin", id)
	return errors.Join(errs...)
}

// forgetSignInData 删掉某个登录的全部账号缓存, 并把这些账号从按账号 id 记的设置里去掉。
func (a *App) forgetSignInData(st *store.Store, id string) error {
	// 账号行可能已被清理掉 (撤销共享), 缓存与设置里却还留着: 按前缀而不是按账号表判断。
	if err := st.RemoveSignInData(a.ctx, id); err != nil {
		return err
	}
	return a.forgetAccountSettings(st, func(acc string) bool {
		prefix, _ := accountid.Split(acc)
		return prefix == id
	})
}

// forgetAccountSettings 把 owned 认领的账号从按账号 id 记的设置 (静音、统一视图排除、默认身份) 里去掉。
func (a *App) forgetAccountSettings(st *store.Store, owned func(accountID string) bool) error {
	for _, key := range []string{"mutedMailAccounts", settingUnifiedExcluded} {
		list := a.accountListSetting(st, key)
		kept := slices.DeleteFunc(slices.Clone(list), owned)
		if len(kept) == len(list) {
			continue
		}
		raw, _ := json.Marshal(kept)
		if err := st.SetStringSetting(a.ctx, key, string(raw)); err != nil {
			return err
		}
	}
	ids := map[string]string{}
	if v, err := st.StringSetting(a.ctx, "defaultIdentities"); err == nil && v != "" {
		_ = json.Unmarshal([]byte(v), &ids)
	}
	changed := false
	for acc := range ids {
		if owned(acc) {
			delete(ids, acc)
			changed = true
		}
	}
	if changed {
		raw, _ := json.Marshal(ids)
		if err := st.SetStringSetting(a.ctx, "defaultIdentities", string(raw)); err != nil {
			return err
		}
	}
	return nil
}
