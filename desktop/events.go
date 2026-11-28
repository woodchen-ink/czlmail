package main

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 前端订阅的事件名。同步是后台持续发生的, 界面不能靠轮询去发现变化。
const (
	// EventConnected 会话建立, 负载为用户名。
	EventConnected = "session:connected"
	// EventSignInRequired 服务器拒绝了保存的凭据, 同步已停, 负载为带错误码的原因。
	EventSignInRequired = "session:sign-in-required"
	// EventMailChanged 某账号的邮件有变动, 负载为 accountID。
	// 前端据此重新拉取当前列表, 而不是全量刷新界面。
	EventMailChanged = "mail:changed"
	// EventSyncStatus 连上后那一轮全账号同步的进度变了, 负载为 MailSyncStatus。
	EventSyncStatus = "mail:sync-status"
	// EventNewMail 到达新邮件, 负载为摘要列表。
	EventNewMail = "mail:new"
	// EventUnread 各账号收件箱未读总数变了(不含静音账号), 负载为总数。
	EventUnread = "mail:unread"
	// EventPIMChanged 日历/通讯录/文件有变动, 负载为 PIMChange。
	EventPIMChanged = "pim:changed"
	// EventSettingsChanged 应用设置(AppSettings)保存了, 无负载; 侧栏据此重读静音账号。
	EventSettingsChanged = "settings:changed"
	// EventAccountsChanged 会话里的账号增减或改名(共享、撤销共享), 无负载; 界面据此重读账号列表。
	EventAccountsChanged = "session:accounts-changed"
)

// PIMChange 是 EventPIMChanged 的负载。Type 为空表示该账号全部类型都可能变了。
type PIMChange struct {
	AccountID string `json:"accountId"`
	Type      string `json:"type"`
}

// onNewMail 由 syncer 在新邮件落库后回调。
//
// 只处理个人账号与共享账号中真正新增的邮件; 标记已读之类的更新不会走到这里。
func (a *App) onNewMail(accountID string, emails []store.Email) {
	// 不止收件箱: 归档、文件夹里的新邮件一样会出现在列表里。
	a.warmAvatars(emails)
	// 只通知收件箱: 已发送、草稿、被过滤规则移走的邮件都不是"来信"。
	inbox := ""
	if st, err := a.currentStore(); err == nil {
		inbox, _ = mailboxByRole(st, a.ctx, accountID, "inbox")
	}
	if inbox == "" {
		return
	}
	own := a.ownAddresses()

	summaries := make([]newMailNotice, 0, len(emails))
	for i := range emails {
		e := &emails[i]
		// 自己发出去的信(任一发件身份, 含别名)不通知, 包括发给自己的。
		if !slices.Contains(e.MailboxIDs, inbox) || hasKeyword(e.Keywords, "$draft") || isFromSelf(e, own) {
			continue
		}
		summaries = append(summaries, newMailNotice{
			AccountID: accountID,
			ID:        e.ID,
			Subject:   e.Subject,
			From:      formatSender(e.From),
			Preview:   e.Preview,
		})
	}

	if len(summaries) == 0 {
		return
	}

	a.emit(EventNewMail, summaries)
	// iOS 上推送生效后由推送通知 (程序在后台时同步根本不跑), 这里再弹会在前台重复一条。
	if a.settingOn("notifyMail", true) && !a.mailMuted(accountID) && !a.pushCovers(accountID) {
		a.notifyNewMail(summaries)
	}
}

type newMailNotice struct {
	AccountID string `json:"accountId"`
	ID        string `json:"id"`
	Subject   string `json:"subject"`
	From      string `json:"from"`
	Preview   string `json:"preview"`
}

// username 是主登录的用户名。只用于跟主登录走的功能 (笔记、模板); 某个账号的登录名用 usernameFor。
func (a *App) username() string {
	return a.primaryConfig().Username
}

func hasKeyword(keywords []string, want string) bool {
	for _, k := range keywords {
		if k == want {
			return true
		}
	}
	return false
}

func isFromSelf(e *store.Email, own map[string]bool) bool {
	for _, a := range e.From {
		if own[strings.ToLower(a.Email)] {
			return true
		}
	}
	return false
}

// ownCache 缓存自己的全部地址 (各登录的登录名 + 各账号的发件身份), 避免每批新邮件都请求一次服务器。
//
// 取身份要打网络, 一律在锁外并行做, 每个请求限时 (ownFetchTimeout): 某个登录有会话但服务器连不上时,
// 新邮件通知、写信、阅读栏、MCP 都不能跟着等 HTTP 超时。已经有结果时过期了先返回旧结果, 后台刷新;
// 取不到的账号沿用上次取到的身份 (perAccount)。
var ownCache struct {
	sync.Mutex
	at    time.Time
	ttl   time.Duration
	addrs map[string]bool
	// perAccount 是各账号上次取到的发件身份地址, 取不到时沿用。
	perAccount map[string][]string
	// refreshing 表示有一次刷新正在进行, 别的调用方不再另起一次。
	refreshing bool
	// done 在正在进行的刷新结束时关闭, 还没有任何结果的调用方等它。
	done chan struct{}
}

const (
	ownCacheTTL = 10 * time.Minute
	// ownCacheRetry 是有账号的身份没取到 (登录离线、还在连) 时的缓存时长: 等它连上后尽快补上。
	ownCacheRetry = time.Minute
	// ownFetchTimeout 是取一个账号身份的上限。
	ownFetchTimeout = 5 * time.Second
)

// fetchOwnIdentities 取一个账号的发件身份地址, 测试里换成假的。
var fetchOwnIdentities = func(a *App, ctx context.Context, accountID string) ([]string, error) {
	ids, err := a.identitiesNow(ctx, accountID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, strings.ToLower(id.Email))
	}
	return out, nil
}

// ownAddresses 返回自己的全部地址 (小写): 所有登录的登录名与所有账号的发件身份 (含别名、共享账号的身份)。
// 新邮件通知、推送过滤、回复时排除自己、界面里的「我」都用它。返回的表不要修改。
func (a *App) ownAddresses() map[string]bool {
	ownCache.Lock()
	if ownCache.addrs != nil && time.Since(ownCache.at) < ownCache.ttl {
		addrs := ownCache.addrs
		ownCache.Unlock()
		return addrs
	}
	if ownCache.refreshing {
		addrs, done := ownCache.addrs, ownCache.done
		ownCache.Unlock()
		if addrs != nil {
			return addrs // 过期的旧结果先用着, 刷新完会换上
		}
		<-done
		ownCache.Lock()
		addrs = ownCache.addrs
		ownCache.Unlock()
		return addrs
	}
	ownCache.refreshing = true
	ownCache.done = make(chan struct{})
	stale := ownCache.addrs
	ownCache.Unlock()

	if stale != nil {
		go a.refreshOwnAddresses()
		return stale
	}
	return a.refreshOwnAddresses()
}

// refreshOwnAddresses 重新取一遍并写进缓存。调用方已把 ownCache.refreshing 置为真。
func (a *App) refreshOwnAddresses() map[string]bool {
	ownCache.Lock()
	last := ownCache.perAccount
	ownCache.Unlock()

	out := map[string]bool{}
	a.mu.RLock()
	for _, si := range a.signInsLocked() {
		if u := strings.ToLower(si.cfg.Username); u != "" {
			out[u] = true
		}
	}
	a.mu.RUnlock()

	per := map[string][]string{}
	complete := true
	if st, err := a.currentStore(); err == nil {
		if accounts, err := st.Accounts(a.ctx); err == nil {
			type result struct {
				acc   string
				addrs []string
				err   error
			}
			results := make(chan result, len(accounts))
			for _, acc := range accounts {
				go func(id string) {
					ctx, cancel := context.WithTimeout(a.ctx, ownFetchTimeout)
					defer cancel()
					addrs, err := fetchOwnIdentities(a, ctx, id)
					results <- result{id, addrs, err}
				}(acc.ID)
			}
			for range accounts {
				r := <-results
				if r.err != nil {
					complete = false
					if prev, ok := last[r.acc]; ok {
						per[r.acc] = prev
					}
					continue
				}
				per[r.acc] = r.addrs
			}
		}
	}
	for _, addrs := range per {
		for _, addr := range addrs {
			out[addr] = true
		}
	}

	ownCache.Lock()
	defer ownCache.Unlock()
	ownCache.addrs, ownCache.perAccount, ownCache.at, ownCache.ttl = out, per, time.Now(), ownCacheTTL
	if !complete {
		ownCache.ttl = ownCacheRetry
	}
	ownCache.refreshing = false
	close(ownCache.done)
	return out
}

// forgetOwnAddresses 让下次 ownAddresses 重新取 (登录增减、账号增减之后); 取到之前先用旧结果, 取不到的账号沿用上次的身份。
func forgetOwnAddresses() {
	ownCache.Lock()
	ownCache.at = time.Time{}
	ownCache.Unlock()
}

// resetOwnAddresses 连同上次的结果一起丢掉: 主登录换了人, 上一个人的地址不能再算自己的。
func resetOwnAddresses() {
	ownCache.Lock()
	ownCache.addrs, ownCache.perAccount, ownCache.at = nil, nil, time.Time{}
	ownCache.Unlock()
}

// OwnAddresses 返回自己的全部地址 (小写, 已排序), 界面据此把自己的地址显示成「我」、回复时去掉自己。
func (a *App) OwnAddresses() []string {
	own := a.ownAddresses()
	out := make([]string, 0, len(own))
	for addr := range own {
		out = append(out, addr)
	}
	slices.Sort(out)
	return out
}

// identitiesNow 同 ListIdentities, 但会话正在恢复时不等, 请求受 ctx 限时。
func (a *App) identitiesNow(ctx context.Context, accountID string) ([]Identity, error) {
	rt, err := a.routeWithin(accountID, 0)
	if err != nil {
		return nil, err
	}
	list, err := rt.sync.Identities(ctx, accountID)
	if err != nil {
		return nil, err
	}
	out := make([]Identity, 0, len(list))
	for _, id := range list {
		if id != nil {
			out = append(out, Identity{ID: string(id.ID), Name: id.Name, Email: id.Email})
		}
	}
	return out, nil
}

// mailMuted 报告某账号(通常是共享邮箱)是否关闭了新邮件通知。
func (a *App) mailMuted(accountID string) bool {
	st, err := a.currentStore()
	if err != nil {
		return false
	}
	raw, err := st.StringSetting(a.ctx, "mutedMailAccounts")
	if err != nil || raw == "" {
		return false
	}
	var muted []string
	_ = json.Unmarshal([]byte(raw), &muted)
	return slices.Contains(muted, accountID)
}

// formatSender 优先显示姓名, 没有姓名时退回地址。
func formatSender(addrs []store.Address) string {
	if len(addrs) == 0 {
		return ""
	}
	if addrs[0].Name != "" {
		return addrs[0].Name
	}
	return addrs[0].Email
}
