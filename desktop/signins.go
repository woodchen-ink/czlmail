package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/accountid"
	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// 登录 (sign-in) 是一组服务器地址 + 凭据, 对应一个 JMAP 会话; 一个会话里可以有多个账号 (个人 + 共享)。
//
// 会话的状态 (客户端、同步器、凭据失效、授权流程) 都挂在 signIn 上, 不直接挂在 App 上。
// 目前只有主登录; 访问服务器的调用一律经 route 按账号 id 找到所属登录与服务器上的账号 id,
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

	// cancelFlow 中止这个登录正在进行的浏览器授权流程。
	// flowGen 标识当前流程, 使收尾时能分辨句柄归属。
	cancelFlow context.CancelFunc
	flowGen    uint64

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
	return nil
}

// signInsLocked 列出全部登录, 主登录在前。调用方须持有 a.mu。
func (a *App) signInsLocked() []*signIn {
	return []*signIn{&a.primary}
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
