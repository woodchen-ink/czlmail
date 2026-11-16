package syncer

import (
	"sort"
	"sync"
)

// RoundStatus 是一轮全账号同步 (连上或重连后那一轮) 的进度。
//
// 首次登录时缓存是空的, 这一轮要逐个账号引导; 界面据此显示「正在同步」而不是空列表。
type RoundStatus struct {
	Running bool `json:"running"`
	// Done / Total 按账号计: 已经同步完邮件的账号数与账号总数。
	Done  int `json:"done"`
	Total int `json:"total"`
	// Error 是这一轮里第一个失败账号的错误, 一轮结束后才有意义。
	Error string `json:"error"`
}

// roundState 与同步锁分开: 界面随时来读, 不该等一次网络请求。
type roundState struct {
	mu     sync.Mutex
	status RoundStatus
}

// Round 返回当前这一轮全账号同步的进度。
func (s *Syncer) Round() RoundStatus {
	s.round.mu.Lock()
	defer s.round.mu.Unlock()
	return s.round.status
}

func (s *Syncer) setRound(st RoundStatus) {
	s.round.mu.Lock()
	s.round.status = st
	s.round.mu.Unlock()
	if s.OnRound != nil {
		s.OnRound(st)
	}
}

// orderedAccounts 个人账号排最前: 用户登录后先看的是自己的收件箱, 不该排在共享邮箱后面等。
func (s *Syncer) orderedAccounts() []string {
	ids := s.Accounts()
	sort.SliceStable(ids, func(i, j int) bool {
		pi, pj := s.isPersonal(ids[i]), s.isPersonal(ids[j])
		if pi != pj {
			return pi
		}
		return ids[i] < ids[j]
	})
	return ids
}
