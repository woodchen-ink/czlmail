package syncer

import (
	"context"
	"fmt"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/mail/email"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 邮件操作一律走服务端的 Email/set, 不在本地先改。
//
// 本地先改再同步会引入一致性问题: 服务端可能拒绝(权限不足、邮件已被别处删除),
// 此时本地已经改过, 要么回滚要么留着一条与服务器不一致的记录。而 Email/set 成功后
// 服务端会推 StateChange, 增量同步随即把结果带回来 —— 路径唯一, 没有两份真相。
//
// 代价是操作反馈要等一个来回。界面可以做乐观更新, 但那属于展示层的事,
// 本层不参与。

// MarkRead 设置或清除已读标志。
func (s *Syncer) MarkRead(ctx context.Context, accountID string, emailIDs []string, read bool) error {
	return s.patchKeyword(ctx, accountID, emailIDs, "$seen", read)
}

// MarkFlagged 设置或清除星标。
func (s *Syncer) MarkFlagged(ctx context.Context, accountID string, emailIDs []string, flagged bool) error {
	return s.patchKeyword(ctx, accountID, emailIDs, "$flagged", flagged)
}

// patchKeyword 用 JSON Patch 只改一个关键字。
//
// 走 patch 而不是整体重写 keywords: 后者会把本次请求之后、服务端处理之前
// 由其它客户端加上的标签覆盖掉。
func (s *Syncer) patchKeyword(ctx context.Context, accountID string, emailIDs []string, keyword string, set bool) error {
	if len(emailIDs) == 0 {
		return nil
	}

	var value any
	if set {
		value = true
	} else {
		// JMAP 的 patch 用 null 表示删除该键。
		value = nil
	}

	updates := make(map[jmap.ID]jmap.Patch, len(emailIDs))
	for _, id := range emailIDs {
		updates[jmap.ID(id)] = jmap.Patch{
			"keywords/" + keyword: value,
		}
	}
	return s.applySet(ctx, accountID, &email.Set{
		Account: jmap.ID(accountID),
		Update:  updates,
	})
}

// Move 把邮件移到目标邮箱。
//
// JMAP 里"移动"就是整体替换 mailboxIds —— 邮件与邮箱是多对多关系, 没有单独的
// move 操作。这里用整体替换而非 patch, 因为移动的语义本就是"只在目标邮箱里"。
func (s *Syncer) Move(ctx context.Context, accountID string, emailIDs []string, targetMailboxID string) error {
	if len(emailIDs) == 0 {
		return nil
	}
	if targetMailboxID == "" {
		return fmt.Errorf("1110 target mailbox is empty")
	}

	updates := make(map[jmap.ID]jmap.Patch, len(emailIDs))
	for _, id := range emailIDs {
		updates[jmap.ID(id)] = jmap.Patch{
			"mailboxIds": map[string]bool{targetMailboxID: true},
		}
	}
	return s.applySet(ctx, accountID, &email.Set{
		Account: jmap.ID(accountID),
		Update:  updates,
	})
}

// Restore 把邮件放回 places 记下的文件夹, 并还原垃圾邮件标记, 用于撤销移动类操作。
//
// 与 Move 一样整体替换 mailboxIds; 关键字只动 $junk / $notjunk, 其余(已读、星标、标签)
// 在撤销前后可能已被用户改过, 不能拿旧值覆盖。没有文件夹的条目跳过 ——
// JMAP 不允许邮件不属于任何邮箱。
func (s *Syncer) Restore(ctx context.Context, accountID string, places []store.EmailPlacement) error {
	updates := restoreUpdates(places)
	if len(updates) == 0 {
		return nil
	}
	return s.applySet(ctx, accountID, &email.Set{
		Account: jmap.ID(accountID),
		Update:  updates,
	})
}

// restoreUpdates 是 Restore 的 Email/set update 部分。
func restoreUpdates(places []store.EmailPlacement) map[jmap.ID]jmap.Patch {
	updates := make(map[jmap.ID]jmap.Patch, len(places))
	for _, p := range places {
		if p.ID == "" || len(p.MailboxIDs) == 0 {
			continue
		}
		boxes := make(map[string]bool, len(p.MailboxIDs))
		for _, b := range p.MailboxIDs {
			boxes[b] = true
		}
		updates[jmap.ID(p.ID)] = jmap.Patch{
			"mailboxIds":        boxes,
			"keywords/$junk":    keywordValue(p.Junk),
			"keywords/$notjunk": keywordValue(p.NotJunk),
		}
	}
	return updates
}

// keywordValue 是 patch 里关键字的值: 设置为 true, 清除为 null。
func keywordValue(set bool) any {
	if set {
		return true
	}
	return nil
}

// Delete 彻底销毁邮件。
//
// 调用方应当先把邮件移入回收站, 只在用户从回收站里再次删除时才调用本方法 ——
// JMAP 的 destroy 不可撤销, 服务端不保留副本。
func (s *Syncer) Delete(ctx context.Context, accountID string, emailIDs []string) error {
	if len(emailIDs) == 0 {
		return nil
	}

	ids := make([]jmap.ID, 0, len(emailIDs))
	for _, id := range emailIDs {
		ids = append(ids, jmap.ID(id))
	}
	return s.applySet(ctx, accountID, &email.Set{
		Account: jmap.ID(accountID),
		Destroy: ids,
	})
}

// applySet 执行 Email/set 并把逐条失败提升为错误, 成功后同步邮件。
func (s *Syncer) applySet(ctx context.Context, accountID string, set *email.Set) error {
	if _, err := s.setEmails(ctx, set); err != nil {
		return err
	}
	// 服务端已接受变更并会推送 StateChange, 但推送到达有延迟。
	// 主动同步一次邮件(不含 PIM), 让界面刷新时读到的就是结果。
	return s.SyncMail(ctx, accountID)
}

// setEmails 执行 Email/set, 不同步。
//
// Email/set 的部分失败不体现在方法错误里: 整个调用返回成功, 失败的条目落在
// notUpdated / notDestroyed 里。不检查这两个字段, 一次被服务端拒绝的删除
// 在界面上会表现为"操作成功但邮件还在"。
func (s *Syncer) setEmails(ctx context.Context, set *email.Set) (*email.SetResponse, error) {
	req := &jmap.Request{Context: ctx}
	req.Invoke(set)

	resp, err := s.do(req)
	if err != nil {
		return nil, err
	}

	res, ok := resp.Responses[0].Args.(*email.SetResponse)
	if !ok {
		return nil, &Error{Code: CodeUnhandled, Msg: "unexpected response to Email/set"}
	}

	if err := firstSetError(res.NotUpdated); err != nil {
		return res, err
	}
	if err := firstSetError(res.NotDestroyed); err != nil {
		return res, err
	}
	if err := firstSetError(res.NotCreated); err != nil {
		return res, err
	}
	return res, nil
}

func firstSetError(m map[jmap.ID]*jmap.SetError) error {
	for id, e := range m {
		if e == nil {
			continue
		}
		desc := ""
		if e.Description != nil {
			desc = ": " + *e.Description
		}
		return &Error{
			Code: CodeMethod,
			Msg:  fmt.Sprintf("set rejected for %s: %s%s", id, e.Type, desc),
		}
	}
	return nil
}
