package main

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// 待办模块 (界面 components/todo): 任务 (ListTasks, 存在日历里的 Task) 与打了待办旗标的邮件合在一起看。
// 旗标邮件只在设置里开了「用旗标标记待办」(taskFlags) 时算待办, 否则 $flagged 是星标, 界面不取。

// todoEmailLimit 是待办里列出的旗标邮件上限。
const todoEmailLimit = 500

// todoSkipKinds 里的旗标邮件不算待办: 已删除、垃圾邮件里的不必再处理。
var todoSkipKinds = []string{store.KindJunk, store.KindTrash}

// ListTodoEmails 列出全部账号里打了旗标的邮件 (本机缓存, 已删除与垃圾邮件除外), 按收件时间倒序。
// 移出统一视图的账号 (出站归档这类) 也算: 旗标是用户自己打的。
func (a *App) ListTodoEmails() ([]store.EmailSummary, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	accounts, err := st.Accounts(a.ctx)
	if err != nil {
		return nil, err
	}
	var boxes []store.MailboxRef
	for _, acc := range accounts {
		list, err := st.Mailboxes(a.ctx, acc.ID)
		if err != nil {
			return nil, err
		}
		for _, b := range list {
			if !slices.Contains(todoSkipKinds, b.Kind) {
				boxes = append(boxes, store.MailboxRef{AccountID: acc.ID, MailboxID: b.ID})
			}
		}
	}
	out, err := st.FlaggedInMailboxes(a.ctx, boxes, todoEmailLimit)
	if out == nil {
		out = []store.EmailSummary{}
	}
	return out, err
}

// todoServerTimeout 是向各账号服务端补查旗标邮件的超时。
const todoServerTimeout = 10 * time.Second

// RefreshTodoEmails 让各账号服务端按 $flagged 查一遍, 本机缓存里没有的旗标邮件补进缓存 (只补元数据)。
// 本机只缓存最近的邮件与翻过的页, 很早以前打了旗标的邮件只靠本地列不出来。各账号并行, 失败的跳过;
// 界面先显示 ListTodoEmails 的本地结果, 这里完成后再读一次。
func (a *App) RefreshTodoEmails() error {
	st, err := a.currentStore()
	if err != nil {
		return err
	}
	accounts, err := st.Accounts(a.ctx)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	for _, acc := range accounts {
		s, err := a.syncerFor(acc.ID)
		if err != nil {
			continue
		}
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(a.ctx, todoServerTimeout)
			defer cancel()
			if _, err := s.QueryServer(ctx, acc.ID, syncer.EmailFilter{Flagged: true}, todoEmailLimit); err != nil {
				a.log.Debug("query flagged emails", "account", acc.ID, "err", err)
			}
		})
	}
	wg.Wait()
	return nil
}
