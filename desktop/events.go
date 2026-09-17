package main

import (
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 前端订阅的事件名。同步是后台持续发生的, 界面不能靠轮询去发现变化。
const (
	// EventConnected 会话建立, 负载为用户名。
	EventConnected = "session:connected"
	// EventSignInRequired 令牌失效或从未登录, 负载为原因。
	EventSignInRequired = "session:sign-in-required"
	// EventMailChanged 某账号的邮件有变动, 负载为 accountID。
	// 前端据此重新拉取当前列表, 而不是全量刷新界面。
	EventMailChanged = "mail:changed"
	// EventOpenEmail 用户点了系统通知, 负载为 "accountID/emailID"。
	EventOpenEmail = "mail:open"
	// EventNewMail 到达新邮件, 负载为摘要列表。
	EventNewMail = "mail:new"
	// EventPIMChanged 日历/通讯录/文件有变动, 负载为 PIMChange。
	EventPIMChanged = "pim:changed"
)

// PIMChange 是 EventPIMChanged 的负载。Type 为空表示该账号全部类型都可能变了。
type PIMChange struct {
	AccountID string `json:"accountId"`
	Type      string `json:"type"`
}

// emit 向前端广播事件。启动早期 ctx 尚未就绪时静默丢弃 ——
// 此时窗口还没有监听者, 事件本身也没有意义。
func (a *App) emit(name string, data ...any) {
	if a.ctx == nil {
		return
	}
	runtime.EventsEmit(a.ctx, name, data...)
}

// onNewMail 由 syncer 在新邮件落库后回调。
//
// 只处理个人账号与共享账号中真正新增的邮件; 标记已读之类的更新不会走到这里。
func (a *App) onNewMail(accountID string, emails []store.Email) {
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
	if a.settingOn("notifyMail", true) && !a.mailMuted(accountID) {
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

func (a *App) username() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg.Username
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

// ownCache 缓存自己的全部发件地址(登录名 + 各账号的发件身份), 避免每批新邮件都请求一次服务器。
var ownCache struct {
	sync.Mutex
	at    time.Time
	addrs map[string]bool
}

func (a *App) ownAddresses() map[string]bool {
	ownCache.Lock()
	defer ownCache.Unlock()
	if ownCache.addrs != nil && time.Since(ownCache.at) < 10*time.Minute {
		return ownCache.addrs
	}
	out := map[string]bool{}
	if u := strings.ToLower(a.username()); u != "" {
		out[u] = true
	}
	if st, err := a.currentStore(); err == nil {
		if accounts, err := st.Accounts(a.ctx); err == nil {
			for _, acc := range accounts {
				ids, err := a.ListIdentities(acc.ID)
				if err != nil {
					continue
				}
				for _, id := range ids {
					out[strings.ToLower(id.Email)] = true
				}
			}
		}
	}
	ownCache.addrs, ownCache.at = out, time.Now()
	return out
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
