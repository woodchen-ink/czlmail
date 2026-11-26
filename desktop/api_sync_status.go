package main

import (
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

// MailSyncStatus 是界面用来区分「还没同步完」与「真的没有邮件」的状态。
type MailSyncStatus struct {
	syncer.RoundStatus
	// Pending 是还没完成首次邮件同步的账号 (本机没有它的邮件 state)。
	// 这些账号的列表为空不代表没有邮件。网关上游邮箱不可用的账号不算: 它要等上游恢复才会同步,
	// 界面按账号的上游状态显示原因 (ListAccounts 的 upstream), 不停在「正在同步」上。
	Pending []string `json:"pending"`
}

// GetMailSyncStatus 返回全部登录当前的同步进度。未登录时为空状态。
func (a *App) GetMailSyncStatus() MailSyncStatus {
	return a.mailSyncStatusWith(nil, syncer.RoundStatus{})
}

// mailSyncStatusWith 合并各登录的同步进度。cur 非空时它的进度取 round (回调里刚发生的那一轮), 其余取各自的当前值。
func (a *App) mailSyncStatusWith(cur *syncer.Syncer, round syncer.RoundStatus) MailSyncStatus {
	out := MailSyncStatus{Pending: []string{}}
	a.mu.RLock()
	st := a.store
	var syncers []*syncer.Syncer
	for _, si := range a.signInsLocked() {
		if si.sync != nil {
			syncers = append(syncers, si.sync)
		}
	}
	a.mu.RUnlock()
	if st == nil {
		return out
	}
	for _, s := range syncers {
		r := s.Round()
		if s == cur {
			r = round
		}
		out.Running = out.Running || r.Running
		out.Done += r.Done
		out.Total += r.Total
		if out.Error == "" {
			out.Error = r.Error
		}
		for _, id := range s.Accounts() {
			if !s.Upstream(id).OK() {
				continue
			}
			// 读不出来时不算进去: 宁可显示空列表, 也不把界面卡在「正在同步」上。
			if state, err := st.State(a.ctx, id, store.TypeEmail); err == nil && state == "" {
				out.Pending = append(out.Pending, id)
			}
		}
	}
	return out
}
