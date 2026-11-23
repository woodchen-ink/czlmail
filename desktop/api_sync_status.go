package main

import (
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// MailSyncStatus 是界面用来区分「还没同步完」与「真的没有邮件」的状态。
type MailSyncStatus struct {
	syncer.RoundStatus
	// Pending 是还没完成首次邮件同步的账号 (本机没有它的邮件 state)。
	// 这些账号的列表为空不代表没有邮件。
	Pending []string `json:"pending"`
}

// GetMailSyncStatus 返回主登录当前的同步进度。未登录时为空状态。
func (a *App) GetMailSyncStatus() MailSyncStatus {
	a.mu.RLock()
	s, st := a.primary.sync, a.store
	a.mu.RUnlock()
	if s == nil || st == nil {
		return MailSyncStatus{Pending: []string{}}
	}
	return a.mailSyncStatus(s, st, s.Round())
}

func (a *App) mailSyncStatus(s *syncer.Syncer, st *store.Store, round syncer.RoundStatus) MailSyncStatus {
	out := MailSyncStatus{RoundStatus: round, Pending: []string{}}
	for _, id := range s.Accounts() {
		// 读不出来时不算进去: 宁可显示空列表, 也不把界面卡在「正在同步」上。
		if state, err := st.State(a.ctx, id, store.TypeEmail); err == nil && state == "" {
			out.Pending = append(out.Pending, id)
		}
	}
	return out
}
