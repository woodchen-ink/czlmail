package main

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 统一视图: 所有账号(个人 + 共享)的同类文件夹合成一个列表, 不用逐个点进共享邮箱查看。
// view 是文件夹类别(inbox/drafts/...), 或 unifiedUnread 表示各账号所有需要处理的未读。
const unifiedUnread = "unread"

// unifiedKinds 是可以合并查看的文件夹类别, 自建文件夹各账号互不对应, 不参与。
var unifiedKinds = []string{
	store.KindInbox, store.KindDrafts, store.KindScheduled, store.KindSent,
	store.KindArchive, store.KindJunk, store.KindTrash,
}

// unreadSkipKinds 里的未读不算待处理: 垃圾邮件与已删除不必看, 已发送与草稿的"未读"没有意义。
var unreadSkipKinds = []string{store.KindJunk, store.KindTrash, store.KindSent, store.KindDrafts, store.KindScheduled}

// ListUnified 分页列出统一视图里的邮件, 按收件时间倒序。
//
// 各账号的文件夹本地都可能只缓存了一部分。按文件夹视图的做法先让每个账号补齐自己的
// [offset, offset+limit) 页: 从第一页依次翻下来时, 每个账号的前 offset+limit 封都已在本地,
// 合并后的这一页只会取自这些邮件, 不会因为某个账号缓存得少而漏掉中间的邮件。
// 未读视图不补取(服务端没有对应的单个文件夹可翻), 只看本地缓存。
func (a *App) ListUnified(view string, limit, offset int) ([]store.EmailSummary, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	boxes, err := a.unifiedMailboxes(st, view)
	if err != nil {
		return nil, err
	}
	if view != unifiedUnread {
		var wg sync.WaitGroup
		for _, b := range boxes {
			wg.Go(func() { a.fillMailboxPage(st, b.AccountID, b.MailboxID, limit, offset) })
		}
		wg.Wait()
	}
	return st.EmailsInMailboxes(a.ctx, boxes, view == unifiedUnread, limit, offset)
}

// unifiedMailboxes 找出各账号里属于该视图的文件夹。
func (a *App) unifiedMailboxes(st *store.Store, view string) ([]store.MailboxRef, error) {
	if view != unifiedUnread && !slices.Contains(unifiedKinds, view) {
		return nil, fmt.Errorf("2250 unknown unified view %q", view)
	}
	accounts, err := st.Accounts(a.ctx)
	if err != nil {
		return nil, err
	}
	var out []store.MailboxRef
	for _, acc := range accounts {
		boxes, err := st.Mailboxes(a.ctx, acc.ID)
		if err != nil {
			return nil, err
		}
		for _, b := range boxes {
			match := b.Kind == view
			if view == unifiedUnread {
				match = !slices.Contains(unreadSkipKinds, b.Kind)
			}
			if match {
				out = append(out, store.MailboxRef{AccountID: acc.ID, MailboxID: b.ID})
			}
		}
	}
	return out, nil
}

// SearchAllEmails 在全部账号的本地缓存里检索, 供统一视图的搜索框使用。
func (a *App) SearchAllEmails(query string, limit int) ([]store.EmailSummary, error) {
	return a.searchAllAccounts(query, limit, func(st *store.Store, acc string, limit int) ([]store.EmailSummary, error) {
		list, err := st.SearchEmails(a.ctx, acc, query, limit)
		if err != nil {
			return nil, err
		}
		return list, st.FillMailboxIDs(a.ctx, acc, list)
	})
}

// SearchAllEmailsOnServer 让每个账号的服务端各检索一遍, 与本地结果合并; 各账号并行。
func (a *App) SearchAllEmailsOnServer(query string, limit int) ([]store.EmailSummary, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	return a.searchAllAccounts(query, limit, func(st *store.Store, acc string, limit int) ([]store.EmailSummary, error) {
		return a.searchAccountOnServer(st, acc, query, limit)
	})
}

// searchAllAccounts 对每个账号执行 search, 合并后按时间倒序取前 limit 封。
// 单个账号失败只记日志: 一个共享邮箱出错不该让整个搜索没有结果。
func (a *App) searchAllAccounts(
	query string, limit int,
	search func(st *store.Store, acc string, limit int) ([]store.EmailSummary, error),
) ([]store.EmailSummary, error) {
	st, err := a.currentStore()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	accounts, err := st.Accounts(a.ctx)
	if err != nil {
		return nil, err
	}
	results := make([][]store.EmailSummary, len(accounts))
	var wg sync.WaitGroup
	for i, acc := range accounts {
		wg.Go(func() {
			list, err := search(st, acc.ID, limit)
			if err != nil {
				a.log.Warn("search account", "account", acc.ID, "err", err)
				return
			}
			results[i] = list
		})
	}
	wg.Wait()
	merged := slices.Concat(results...)
	slices.SortStableFunc(merged, func(x, y store.EmailSummary) int { return y.ReceivedAt.Compare(x.ReceivedAt) })
	if len(merged) > limit {
		merged = merged[:limit]
	}
	return merged, nil
}
