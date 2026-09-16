// Package syncer 把服务端的 JMAP 状态增量拉进本地 store。
//
// 全部增量由一条 EventSource 长连接驱动: 服务端推 StateChange, 其中带着每个账号
// 每类对象的最新 state token; 与本地记录的 token 不一致的类型才会触发一次拉取。
// 不做定时轮询 —— 轮询既拖慢新邮件到达, 又在无变化时空转。
package syncer

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/core"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 服务端未公布 Core 限制时的保守取值。RFC 8620 不保证这些字段一定存在,
// 取值偏小只影响批次数量, 取值偏大会直接被服务端拒绝整个请求。
const (
	defaultMaxObjectsInGet   = 100
	defaultMaxCallsInRequest = 8
)

// bootstrapWindow 是首次同步时预取的邮件条数。
//
// 邮件账号的历史可以有几十万封, 首次全量拉取既慢又没必要 —— 用户打开客户端先看到的
// 永远是最近的邮件。这里只预取一个窗口, 随后的 state token 仍然是完整的, 增量同步
// 照常工作; 更早的邮件在用户向下滚动时按需分页拉取。
const bootstrapWindow = 500

// Syncer 针对单个 JMAP 会话(一组凭据)工作。该会话下的多个账号
// (个人账号与共享账号)共用同一个 Syncer。
type Syncer struct {
	client *jmap.Client
	store  *store.Store
	log    *slog.Logger

	maxObjectsInGet   int
	maxCallsInRequest int

	// OnNewMail 在同步到新邮件时回调, 供上层弹系统通知。
	// 只在真正新建的邮件上触发, 标记已读之类的更新不会触发。
	// 回调在同步协程上执行, 实现方不应阻塞。
	OnNewMail func(accountID string, emails []store.Email)

	// OnChanged 在某账号的一轮同步成功落库后回调, 不论是否有新邮件。
	//
	// 与 OnNewMail 分开且必须有: 首次引导、邮箱树变化、标记已读都不产生"新邮件",
	// 只挂 OnNewMail 的话界面在首次登录后永远等不到刷新信号, 表现为一片空白。
	OnChanged func(accountID string)

	// OnPIMChanged 在日历、通讯录、文件某一类型同步落库后回调, typeName 为 JMAP 类型名。
	// 与 OnChanged 分开: 日历变了不该让邮件列表重载。
	OnPIMChanged func(accountID, typeName string)

	// mu 串行化同一账号上的同步, 避免推送密集时同一类型被并发拉取,
	// 造成两条协程拿着同一个旧 state token 各拉一遍。
	mu sync.Mutex
}

// New 构造 Syncer。client 必须已完成 Authenticate, 因为 Core 限制取自 Session。
func New(client *jmap.Client, st *store.Store, log *slog.Logger) *Syncer {
	s := &Syncer{
		client:            client,
		store:             st,
		log:               log,
		maxObjectsInGet:   defaultMaxObjectsInGet,
		maxCallsInRequest: defaultMaxCallsInRequest,
	}
	s.readCoreLimits()
	return s
}

// readCoreLimits 从会话里取服务端公布的批量上限。超出上限的请求会被整个拒绝,
// 因此这些值必须在构造批次前就位, 不能等到第一次 413 再退让。
func (s *Syncer) readCoreLimits() {
	if s.client.Session == nil {
		return
	}
	c, ok := s.client.Session.Capabilities[jmap.CoreURI].(*core.Core)
	if !ok {
		return
	}
	if c.MaxObjectsInGet > 0 {
		s.maxObjectsInGet = int(c.MaxObjectsInGet)
	}
	if c.MaxCallsInRequest > 0 {
		s.maxCallsInRequest = int(c.MaxCallsInRequest)
	}
	s.log.Debug("core limits",
		"maxObjectsInGet", s.maxObjectsInGet,
		"maxCallsInRequest", s.maxCallsInRequest)
}

// Accounts 列出会话下全部账号 ID。
func (s *Syncer) Accounts() []string {
	if s.client.Session == nil {
		return nil
	}
	out := make([]string, 0, len(s.client.Session.Accounts))
	for id := range s.client.Session.Accounts {
		out = append(out, string(id))
	}
	return out
}

// SyncAccount 把一个账号的全部已支持类型拉到最新。
func (s *Syncer) SyncAccount(ctx context.Context, accountID string) error {
	err := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()

		// 邮箱必须先于邮件同步: 邮件携带的 mailboxIds 指向邮箱, 反过来会让界面在
		// 邮件已入库而邮箱树尚未建立的窗口期里显示出无归属的邮件。
		if err := s.syncMailboxes(ctx, accountID); err != nil {
			return err
		}
		if err := s.syncEmails(ctx, accountID); err != nil {
			return err
		}
		// 日历、通讯录、文件放在邮件之后且不影响返回值: 邮件是主业务,
		// 某个共享账号没有日历权限不该让整个账号显示同步失败。
		s.syncPIM(ctx, accountID)
		return nil
	}()

	// 回调放在锁外: 回调方可能反过来调用本 Syncer, 持锁回调会死锁。
	if err == nil {
		s.notifyChanged(accountID)
		s.notifyPIM(accountID, "")
	}
	return err
}

// notifyPIM 通知 PIM 变化, typeName 为空表示全部类型。
func (s *Syncer) notifyPIM(accountID, typeName string) {
	if s.OnPIMChanged != nil {
		s.OnPIMChanged(accountID, typeName)
	}
}

func (s *Syncer) notifyChanged(accountID string) {
	if s.OnChanged != nil {
		s.OnChanged(accountID)
	}
}

// applyStateChange 收到推送后按类型比对本地 state, 只拉真正落后的部分。
func (s *Syncer) applyStateChange(ctx context.Context, change *jmap.StateChange) {
	for accountID, typeStates := range change.Changed {
		for typeName, remoteState := range typeStates {
			dt := store.DataType(typeName)
			if !s.handles(dt) {
				continue
			}

			localState, err := s.store.State(ctx, string(accountID), dt)
			if err != nil {
				s.log.Error("read local state", "account", accountID, "type", typeName, "err", err)
				continue
			}
			if localState == remoteState {
				continue
			}

			if err := s.syncType(ctx, string(accountID), dt); err != nil {
				s.log.Error("sync type", "account", accountID, "type", typeName, "err", err)
			}
		}
	}
}

// handles 报告当前是否实现了该类型的同步。
// 日历/通讯录/文件的 capability 尚未接入, 推送里的这些类型先忽略而不是报错。
func (s *Syncer) handles(dt store.DataType) bool {
	switch dt {
	case store.TypeMailbox, store.TypeEmail:
		return true
	default:
		_, ok := pimKindFor(dt)
		return ok
	}
}

func (s *Syncer) syncType(ctx context.Context, accountID string, dt store.DataType) error {
	err := func() error {
		s.mu.Lock()
		defer s.mu.Unlock()

		switch dt {
		case store.TypeMailbox:
			return s.syncMailboxes(ctx, accountID)
		case store.TypeEmail:
			return s.syncEmails(ctx, accountID)
		default:
			if k, ok := pimKindFor(dt); ok {
				return s.syncPIMKind(ctx, accountID, k)
			}
			return nil
		}
	}()
	if err == nil {
		if k, ok := pimKindFor(dt); ok {
			s.notifyPIM(accountID, k.typeName)
		} else {
			s.notifyChanged(accountID)
		}
	}
	return err
}

// do 发起请求并把方法级错误提升为 Go error。
//
// JMAP 的方法错误不体现在 HTTP 状态码上: 请求整体返回 200, 失败的方法在
// methodResponses 里被替换成一个 error 调用。不显式检查就会把错误当成空结果处理。
func (s *Syncer) do(req *jmap.Request) (*jmap.Response, error) {
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, wrap(CodeRequest, "jmap request", err)
	}
	for _, inv := range resp.Responses {
		if me, ok := inv.Args.(*jmap.MethodError); ok {
			return resp, &Error{
				Code: CodeMethod,
				Msg:  "method " + inv.Name + " failed",
				Err:  me,
			}
		}
	}
	return resp, nil
}

// methodErrorType 取出错误的 JMAP 类型名, 非方法错误返回空串。
func methodErrorType(err error) string {
	var e *Error
	if !errors.As(err, &e) {
		return ""
	}
	var me *jmap.MethodError
	if !errors.As(e.Err, &me) {
		return ""
	}
	return me.Type
}

// chunk 把 ids 切成不超过 size 的批次。
func chunk(ids []jmap.ID, size int) [][]jmap.ID {
	if size <= 0 {
		size = defaultMaxObjectsInGet
	}
	var out [][]jmap.ID
	for start := 0; start < len(ids); start += size {
		end := start + size
		if end > len(ids) {
			end = len(ids)
		}
		out = append(out, ids[start:end])
	}
	return out
}
