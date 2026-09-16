package main

import (
	"context"
	"sync"
	"time"
)

// 正文预取。
//
// 邮件列表只同步元数据, 正文第一次打开时才从服务器拉取, 表现为阅读窗格里短暂的
// "正在取回正文"。两种预取让大多数邮件点开即显示:
//   - 每轮同步后, 在后台把最近 14 天内、每个账号最新 200 封的正文拉下来;
//   - 打开一封邮件时, 顺带拉取列表里相邻的几封(用户最可能接着看的)。
//
// 后台预取串行进行、批量请求, 与界面发起的请求互不阻塞。

const (
	prefetchWindow = 14 * 24 * time.Hour
	prefetchLimit  = 200
)

type prefetcher struct {
	mu      sync.Mutex
	running map[string]bool
}

// prefetchRecent 在后台预取某账号最近邮件的正文。同一账号同时只跑一个。
func (a *App) prefetchRecent(accountID string) {
	a.prefetch.mu.Lock()
	if a.prefetch.running == nil {
		a.prefetch.running = map[string]bool{}
	}
	if a.prefetch.running[accountID] {
		a.prefetch.mu.Unlock()
		return
	}
	a.prefetch.running[accountID] = true
	a.prefetch.mu.Unlock()

	go func() {
		defer func() {
			a.prefetch.mu.Lock()
			delete(a.prefetch.running, accountID)
			a.prefetch.mu.Unlock()
		}()
		st, err := a.currentStore()
		if err != nil {
			return
		}
		s, err := a.currentSyncer()
		if err != nil {
			return
		}
		ids, err := st.EmailsWithoutBody(a.ctx, accountID, time.Now().Add(-prefetchWindow).Unix(), prefetchLimit)
		if err != nil || len(ids) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
		defer cancel()
		if n, err := s.FetchBodies(ctx, accountID, ids); err != nil {
			a.log.Warn("prefetch bodies", "account", accountID, "fetched", n, "err", err)
		}
	}()
}

// PrefetchBodies 预取指定邮件的正文(打开邮件时由界面传入相邻的几封)。立即返回。
func (a *App) PrefetchBodies(accountID string, emailIDs []string) {
	if len(emailIDs) == 0 || len(emailIDs) > 20 {
		return
	}
	go func() {
		st, err := a.currentStore()
		if err != nil {
			return
		}
		cached, err := st.BodyCached(a.ctx, accountID, emailIDs)
		if err != nil {
			return
		}
		var missing []string
		for _, id := range emailIDs {
			if !cached[id] {
				missing = append(missing, id)
			}
		}
		s, err := a.currentSyncer()
		if err != nil || len(missing) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
		defer cancel()
		_, _ = s.FetchBodies(ctx, accountID, missing)
	}()
}
