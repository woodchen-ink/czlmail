package main

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/config"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 本文件是暴露给前端的全部方法, Wails 据此生成 TypeScript 声明。
//
// 所有读取一律走本地缓存, 不直接打服务器: 界面必须在离线时也能翻邮件,
// 且列表滚动不该受网络抖动影响。联网只发生在后台同步循环与显式的按需拉取里。

var (
	// errNotReady 缓存尚未打开。前端据此显示启动中而不是空列表。
	errNotReady = fmt.Errorf("2001 store not ready")
	// errNotSignedIn 尚未建立会话。
	errNotSignedIn = fmt.Errorf("2002 not signed in")
)

func (a *App) currentStore() (*store.Store, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.store == nil {
		return nil, errNotReady
	}
	return a.store, nil
}

// SessionStatus 是界面启动时判断该显示登录页还是邮件页的依据。
type SessionStatus struct {
	// Configured 表示配置文件里已有服务器与客户端信息。
	Configured bool `json:"configured"`
	// Connected 表示会话已建立, 可以收发。
	Connected bool   `json:"connected"`
	Username  string `json:"username"`
	Server    string `json:"server"`
}

func (a *App) GetSessionStatus() SessionStatus {
	a.waitReady(15 * time.Second)
	a.mu.RLock()
	defer a.mu.RUnlock()
	return SessionStatus{
		Configured: a.cfg.Configured(),
		Connected:  a.client != nil,
		Username:   a.cfg.Username,
		Server:     a.cfg.ServerHost,
	}
}

// SignOut 停掉同步并清除令牌。
//
// 本地缓存保留: 重新登录后不必重新引导, 且清库是破坏性操作,
// 应由界面上单独的入口触发而不是搭在退出登录里。
func (a *App) SignOut() error {
	a.mu.Lock()
	cfg := a.cfg
	if a.cancelSync != nil {
		a.cancelSync()
		a.cancelSync = nil
	}
	a.client = nil
	a.sync = nil
	a.mu.Unlock()

	var err error
	if cfg.AuthMethod == config.AuthPassword {
		err = config.DeletePassword(cfg.SessionEndpoint, cfg.Username)
	} else if cfg.Issuer != "" {
		err = config.DeleteToken(cfg.Issuer)
	}
	if err != nil {
		return err
	}

	// 配置一并清掉, 否则下次启动 Configured() 仍为真, 界面会进入一个
	// 连不上服务器的邮件页, 而不是回到登录页。
	a.mu.Lock()
	a.cfg = config.Config{}
	a.mu.Unlock()
	return config.Save(config.Config{})
}

// ListAccounts 列出全部账号, 个人账号在前。
func (a *App) ListAccounts() ([]store.Account, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	return st.Accounts(a.ctx)
}

// ListMailboxes 返回某账号的邮箱平表, 建树交给前端。
func (a *App) ListMailboxes(accountID string) ([]store.Mailbox, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	return st.Mailboxes(a.ctx, accountID)
}

// ListEmails 按收件时间倒序分页取邮件。
func (a *App) ListEmails(accountID, mailboxID string, limit, offset int) ([]store.EmailSummary, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	// 限制单页上限, 避免前端传入一个大数把整个邮箱拉进内存。
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	a.fillMailboxPage(st, accountID, mailboxID, limit, offset)
	return st.EmailsByMailbox(a.ctx, accountID, mailboxID, limit, offset)
}

// fillPageTimeout 限制翻页时向服务端补取的等待。超时就先显示本地已有的,
// 列表不能因为网络慢而卡在加载态。
const fillPageTimeout = 10 * time.Second

// fillMailboxPage 在本地缓存不完整时, 从服务端补齐这一页。
//
// 失败只记日志: 离线时照样要能翻已缓存的邮件。
func (a *App) fillMailboxPage(st *store.Store, accountID, mailboxID string, limit, offset int) {
	local, total, err := st.MailboxCoverage(a.ctx, accountID, mailboxID)
	if err != nil || local >= total {
		return
	}
	s, err := a.currentSyncer()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(a.ctx, fillPageTimeout)
	defer cancel()
	if err := s.FillMailboxPage(ctx, accountID, mailboxID, offset, limit); err != nil {
		a.log.Warn("fill mailbox page", "account", accountID, "mailbox", mailboxID, "offset", offset, "err", err)
	}
}

// GetEmail 读单封邮件。BodyFetched 为 false 时正文尚未缓存,
// 前端应接着调用 FetchBody。
func (a *App) GetEmail(accountID, emailID string) (*store.EmailDetail, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	return st.Email(a.ctx, accountID, emailID)
}

// SearchEmails 在本地缓存里做全文检索。
func (a *App) SearchEmails(accountID, query string, limit int) ([]store.EmailSummary, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	list, err := st.SearchEmails(a.ctx, accountID, query, limit)
	if err != nil {
		return nil, err
	}
	return list, st.FillMailboxIDs(a.ctx, accountID, list)
}

// SearchEmailsOnServer 在本地搜索之外再让服务端全文检索整个账号, 合并两边结果。
//
// 界面先用 SearchEmails 立即出本地结果, 再调这个补上不在缓存里的邮件(如归档里的旧邮件)。
// 服务端不可达或超时只记日志, 退回本地结果。
func (a *App) SearchEmailsOnServer(accountID, query string, limit int) ([]store.EmailSummary, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	var remote []string
	if s, err := a.currentSyncer(); err == nil {
		ctx, cancel := context.WithTimeout(a.ctx, fillPageTimeout)
		remote, err = s.SearchServer(ctx, accountID, query, limit)
		cancel()
		if err != nil {
			a.log.Warn("server search", "account", accountID, "err", err)
		}
	}
	local, err := st.SearchEmails(a.ctx, accountID, query, limit)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(local))
	for _, e := range local {
		seen[e.ID] = true
	}
	var extra []string
	for _, id := range remote {
		if !seen[id] {
			extra = append(extra, id)
		}
	}
	more, err := st.EmailsByIDs(a.ctx, accountID, extra)
	if err != nil {
		return nil, err
	}
	list := append(local, more...)
	slices.SortStableFunc(list, func(x, y store.EmailSummary) int { return y.ReceivedAt.Compare(x.ReceivedAt) })
	if len(list) > limit {
		list = list[:limit]
	}
	return list, st.FillMailboxIDs(a.ctx, accountID, list)
}

// LocateEmail 告诉界面某封邮件在哪个文件夹的列表里、排第几。
// preferMailboxID 是界面当前打开的文件夹, 邮件在其中时就不切走。
func (a *App) LocateEmail(accountID, emailID, preferMailboxID string) (store.EmailLocation, error) {
	st, err := a.currentStore()
	if err != nil {
		return store.EmailLocation{}, err
	}
	return st.LocateEmail(a.ctx, accountID, emailID, preferMailboxID)
}

// SyncNow 立即同步一个账号, 供界面上的手动刷新使用。
func (a *App) SyncNow(accountID string) error {
	s, err := a.currentSyncer()
	if err != nil {
		return err
	}
	return s.SyncAccount(a.ctx, accountID)
}
