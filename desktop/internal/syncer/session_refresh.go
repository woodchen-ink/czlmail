package syncer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"time"

	"git.sr.ht/~rockorager/go-jmap"
)

// 会话里的账号列表会在运行中变化: 别人把邮箱共享给自己、撤销共享、改了共享邮箱的名字。
// 会话资源只在连上时读一次, 不重读的话新共享的邮箱要等程序重启才出现。
//
// 重读的时机:
//   - 推送里出现会话里没有的账号 (新共享的邮箱一有变化就会推过来, 这是最快的信号)
//   - 每次断线重连 (重连前那段空窗里的变化推送不会补发)
//   - 用户点「立即同步」
//   - 兜底每 sessionCheckInterval 一次 (撤销共享、改名不会产生推送)
const (
	sessionCheckInterval = 10 * time.Minute
	sessionFetchTimeout  = 30 * time.Second
)

// AccountDiff 是一次重读会话前后账号列表的差异。
type AccountDiff struct {
	Added []string
	// Removed 是确认已失去访问、缓存已删掉的账号 (见 pruneAccounts), 不是这一次会话里刚好少了的账号。
	Removed []string
	// Renamed 是名称或个人/共享属性变了的账号, 只需要更新缓存里的账号行。
	Renamed []string
	// Capabilities 是公布的能力变了的账号 (服务器升级、管理员开关了日历等): 更新账号行让界面重新按能力显示功能,
	// 再同步一次, 新支持的日历/通讯录/网盘随即引导。
	Capabilities []string
}

// Empty 报告账号列表是否没有变化。
func (d AccountDiff) Empty() bool {
	return len(d.Added) == 0 && len(d.Removed) == 0 && len(d.Renamed) == 0 && len(d.Capabilities) == 0
}

// RequestSessionRefresh 请求尽快重读一次会话, 不阻塞; 已有待处理的请求时合并。
func (s *Syncer) RequestSessionRefresh() {
	select {
	case s.sessionRefresh <- struct{}{}:
	default:
	}
}

// knowsAccount 报告当前会话里是否有该账号。
func (s *Syncer) knowsAccount(accountID jmap.ID) bool {
	sess := s.session()
	if sess == nil {
		return false
	}
	_, ok := sess.Accounts[accountID]
	return ok
}

// watchSession 处理重读请求与定时兜底, 直到 ctx 取消。
func (s *Syncer) watchSession(ctx context.Context) {
	ticker := time.NewTicker(sessionCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.sessionRefresh:
		}
		if err := s.refreshSession(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("refresh session", "err", err)
		}
	}
}

// refreshSession 重读会话资源, 账号有变化时回调 OnAccountsChanged 并同步新出现的账号。
//
// 定时兜底与重连可能同时触发, 串行执行: 并发时两次都会拿旧会话做对比, 同一个新账号被回调、引导两遍。
func (s *Syncer) refreshSession(ctx context.Context) error {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	before := s.session()
	if before == nil {
		return nil
	}

	fresh, err := s.fetchSession(ctx)
	if err != nil || fresh == nil {
		return err
	}
	// 撤销共享会删掉那个账号的整份缓存, 所以只信看起来完整的会话: 个人账号不见了一定是服务端异常
	// (维护中、目录后端暂时不可用), 不换上, 等下一次再读。
	if !hasPersonal(fresh.Accounts) {
		return wrap(CodeSession, "refresh session", fmt.Errorf("session has no personal account, keeping the previous one"))
	}
	s.client.Lock()
	s.client.Session = fresh
	s.client.Unlock()

	diff := diffAccounts(before.Accounts, fresh.Accounts)
	diff.Added, diff.Renamed, diff.Capabilities = s.locals(diff.Added), s.locals(diff.Renamed), s.locals(diff.Capabilities)
	// 会话里少了的账号先不删缓存, 交给 pruneAccounts 连续两次确认; diff.Removed 只放确认删掉的。
	diff.Removed = s.pruneAccounts(ctx)
	if diff.Empty() {
		return nil
	}
	s.log.Info("session accounts changed", "added", diff.Added, "removed", diff.Removed, "renamed", diff.Renamed, "capabilities", diff.Capabilities)
	if s.OnAccountsChanged != nil {
		s.OnAccountsChanged(diff)
	}
	for _, id := range slices.Concat(diff.Added, diff.Capabilities) {
		if ctx.Err() != nil {
			return nil
		}
		if err := s.SyncAccount(ctx, id); err != nil {
			s.log.Error("sync new account", "account", id, "err", err)
		}
	}
	return nil
}

// fetchSession 用一个临时客户端读会话资源, 不碰正在用的会话。
//
// go-jmap 的 Authenticate 不接受 context 且会直接改写客户端的 Session: 在正在用的客户端上调用,
// 超时后迟到的结果会绕过下面的校验被装上, 而且装上到校验之间别的协程会读到未经校验的会话。
// 临时客户端共用同一个 HTTP 客户端, 凭据与失效检测 (auth.Watch) 照常生效。
func (s *Syncer) fetchSession(ctx context.Context) (*jmap.Session, error) {
	tmp := &jmap.Client{SessionEndpoint: s.client.SessionEndpoint, HttpClient: s.client.HttpClient}
	done := make(chan error, 1)
	go func() { done <- tmp.Authenticate() }()
	select {
	case err := <-done:
		if err != nil {
			return nil, wrap(CodeSession, "refresh session", err)
		}
		return tmp.Session, nil
	case <-time.After(sessionFetchTimeout):
		return nil, wrap(CodeSession, "refresh session", fmt.Errorf("timed out after %s", sessionFetchTimeout))
	case <-ctx.Done():
		return nil, nil
	}
}

// PruneAccounts 在连上时对比缓存与会话, 记下缓存里有、会话里已没有的账号 (程序关着时被撤销的共享)。
// 只记一次, 由之后的重读会话确认后再删。
func (s *Syncer) PruneAccounts(ctx context.Context) []string {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	return s.pruneAccounts(ctx)
}

// missingConfirmations 是账号连续几次不在会话里才删它的缓存。
// 一次缺失可能是服务端的瞬时异常 (改 ACL 的中途、目录后端只返回了一部分), 而信任名单、译文只存在本机, 删了找不回来。
const missingConfirmations = 2

// pruneAccounts 删掉连续 missingConfirmations 次不在会话里的账号的缓存, 返回删掉的账号。调用方持有 sessionMu。
func (s *Syncer) pruneAccounts(ctx context.Context) []string {
	sess := s.session()
	if sess == nil {
		return nil
	}
	cached, err := s.store.Accounts(ctx)
	if err != nil {
		s.log.Warn("list cached accounts", "err", err)
		return nil
	}
	if s.missing == nil {
		s.missing = map[string]int{}
	}
	var removed []string
	seen := map[string]bool{}
	for _, acc := range cached {
		// 缓存库里还有别的登录的账号, 它们不在这个会话里是正常的。
		if !s.owns(acc.ID) {
			continue
		}
		seen[acc.ID] = true
		if _, ok := sess.Accounts[s.server(acc.ID)]; ok {
			delete(s.missing, acc.ID)
			continue
		}
		s.missing[acc.ID]++
		if s.missing[acc.ID] < missingConfirmations {
			s.log.Info("account missing from session, waiting for confirmation", "account", acc.ID)
			continue
		}
		// 持同步锁删除: 这个账号可能还有一次拉取在途, 删完它又写回来就成了没有账号行的孤儿数据。
		s.mu.Lock()
		err := s.writing(func() error { return s.store.RemoveAccount(ctx, acc.ID) })
		s.mu.Unlock()
		if err != nil {
			s.log.Error("remove account cache", "account", acc.ID, "err", err)
			continue
		}
		delete(s.missing, acc.ID)
		removed = append(removed, acc.ID)
	}
	for id := range s.missing {
		if !seen[id] {
			delete(s.missing, id)
		}
	}
	return removed
}

// locals 把一组服务器账号 id 换成本机账号 id。
func (s *Syncer) locals(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, s.local(jmap.ID(id)))
	}
	return out
}

// diffAccounts 对比两份会话账号表, 返回服务器账号 id。
func diffAccounts(before, after map[jmap.ID]jmap.Account) AccountDiff {
	var d AccountDiff
	for id, acc := range after {
		old, ok := before[id]
		switch {
		case !ok:
			d.Added = append(d.Added, string(id))
		case old.Name != acc.Name || old.IsPersonal != acc.IsPersonal:
			d.Renamed = append(d.Renamed, string(id))
		}
		if ok && !sameCapabilities(old.RawCapabilities, acc.RawCapabilities) {
			d.Capabilities = append(d.Capabilities, string(id))
		}
	}
	for id := range before {
		if _, ok := after[id]; !ok {
			d.Removed = append(d.Removed, string(id))
		}
	}
	// map 遍历无序, 排序后日志与测试稳定。
	slices.Sort(d.Added)
	slices.Sort(d.Removed)
	slices.Sort(d.Renamed)
	slices.Sort(d.Capabilities)
	return d
}

// sameCapabilities 比较两份账号能力表: 能力集合与各能力对象 (定时发送上限等参数) 都相同。
// 按 JSON 值比较而不是按字节: 服务器每次输出的键顺序不保证一致, 按字节比会让每次重读会话都当作变化、多跑一轮同步。
func sameCapabilities(a, b map[jmap.URI]json.RawMessage) bool {
	if len(a) != len(b) {
		return false
	}
	for uri, x := range a {
		y, ok := b[uri]
		if !ok {
			return false
		}
		if bytes.Equal(x, y) {
			continue
		}
		var vx, vy any
		if json.Unmarshal(x, &vx) != nil || json.Unmarshal(y, &vy) != nil || !reflect.DeepEqual(vx, vy) {
			return false
		}
	}
	return true
}

func hasPersonal(accounts map[jmap.ID]jmap.Account) bool {
	for _, acc := range accounts {
		if acc.IsPersonal {
			return true
		}
	}
	return false
}
