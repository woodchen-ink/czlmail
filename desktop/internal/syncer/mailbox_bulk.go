package syncer

import (
	"context"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/mail/email"
)

// 整个文件夹的批量操作: 标记为已读、清空。
//
// 都在服务端按 Email/query 分批取 id 再 Email/set, 不依赖本地缓存 ——
// 本地只有最近一段邮件, 按本地 id 操作会漏掉窗口之外的。
// 每批处理完的邮件不再满足查询条件, 所以每次都从 position 0 取。

// bulkBatch 是单次 Email/set 的条数上限。
const bulkBatch = 500

// MarkMailboxRead 把文件夹里全部未读邮件标为已读, 返回处理的封数。
func (s *Syncer) MarkMailboxRead(ctx context.Context, accountID, mailboxID string) (int, error) {
	filter := &email.FilterCondition{InMailbox: jmap.ID(mailboxID), NotKeyword: "$seen"}
	n, err := s.bulkMailbox(ctx, accountID, filter, func(ids []jmap.ID) *email.Set {
		updates := make(map[jmap.ID]jmap.Patch, len(ids))
		for _, id := range ids {
			updates[id] = jmap.Patch{"keywords/$seen": true}
		}
		return &email.Set{Account: jmap.ID(accountID), Update: updates}
	})
	return n, s.finishBulk(ctx, accountID, n, err)
}

// EmptyMailbox 彻底删除文件夹里的全部邮件(与 Bulwark 相同, 不经回收站), 返回删除的封数。
func (s *Syncer) EmptyMailbox(ctx context.Context, accountID, mailboxID string) (int, error) {
	filter := &email.FilterCondition{InMailbox: jmap.ID(mailboxID)}
	n, err := s.bulkMailbox(ctx, accountID, filter, func(ids []jmap.ID) *email.Set {
		return &email.Set{Account: jmap.ID(accountID), Destroy: ids}
	})
	return n, s.finishBulk(ctx, accountID, n, err)
}

// bulkMailbox 反复取满足 filter 的前一批 id 交给 build 生成的 Email/set, 直到取空。
// 某批一条都没处理成功(权限不足等)时停止, 避免在同一批 id 上死循环。
func (s *Syncer) bulkMailbox(
	ctx context.Context, accountID string, filter *email.FilterCondition, build func([]jmap.ID) *email.Set,
) (int, error) {
	batch := min(bulkBatch, s.maxObjectsInGet)
	total := 0
	for {
		req := &jmap.Request{Context: ctx}
		req.Invoke(&email.Query{Account: jmap.ID(accountID), Filter: filter, Limit: uint64(batch)})
		resp, err := s.do(req)
		if err != nil {
			return total, err
		}
		q, ok := resp.Responses[0].Args.(*email.QueryResponse)
		if !ok {
			return total, &Error{Code: CodeUnhandled, Msg: "unexpected response to Email/query"}
		}
		if len(q.IDs) == 0 {
			return total, nil
		}

		res, err := s.setEmails(ctx, build(q.IDs))
		done := 0
		if res != nil {
			done = len(res.Updated) + len(res.Destroyed)
		}
		total += done
		if err != nil {
			return total, err
		}
		if done == 0 || len(q.IDs) < batch {
			return total, nil
		}
	}
}

// finishBulk 在处理过邮件时同步一次, 同步失败不掩盖操作本身的错误。
func (s *Syncer) finishBulk(ctx context.Context, accountID string, n int, err error) error {
	if n > 0 {
		if serr := s.SyncMail(ctx, accountID); serr != nil && err == nil {
			err = serr
		}
	}
	return err
}
