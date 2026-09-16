package syncer

import (
	"context"
	"database/sql"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/mail/email"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 按邮箱补取历史邮件。
//
// 首次引导只取整个账号最近的一个窗口, 窗口之外的邮件只有经这里才会进缓存。
// 做法是先取一段 id(很轻), 只对本地缺的那部分再 Email/get。
//
// 不写 state: 取回的是服务端当前的数据, 不会比本地 state 旧, 之后的增量对它们照常生效。
// 也不拿同步锁, 否则翻页要等一轮同步结束。

// pullBatch 是整夹拉取时每轮取的 id 数。取回元数据时还会按 maxObjectsInGet 再分批。
const pullBatch = 500

// FillMailboxPage 保证某邮箱按收件时间倒序的 [position, position+limit) 区间已在本地。
//
// 不能只在"本地这一页不满"时才补: 移进归档的新邮件会让本地看起来满页,
// 实际中间缺着一段历史, 那一段从此永远翻不到。
func (s *Syncer) FillMailboxPage(ctx context.Context, accountID, mailboxID string, position, limit int) error {
	_, _, _, err := s.fillRange(ctx, accountID, mailboxID, position, limit, false)
	return err
}

// PullProgress 是整夹拉取的进度。
type PullProgress struct {
	Scanned int `json:"scanned"`
	Fetched int `json:"fetched"`
	Total   int `json:"total"`
}

// PullMailbox 把某邮箱在服务端的全部邮件元数据拉进本地。正文仍按需拉取 ——
// 万封邮件的正文是 GB 级, 而用户要的是能翻到、能搜到。
//
// progress 在每批落库后回调。中途失败时已落库的部分保留, 重试会跳过它们。
func (s *Syncer) PullMailbox(ctx context.Context, accountID, mailboxID string, progress func(PullProgress)) error {
	var p PullProgress
	for {
		ids, fetched, total, err := s.fillRange(ctx, accountID, mailboxID, p.Scanned, pullBatch, true)
		if err != nil {
			return err
		}
		p.Scanned += ids
		p.Fetched += fetched
		p.Total = total
		if progress != nil {
			progress(p)
		}
		// 以服务端返回空页为终止条件, 而不是 total: 拉取期间有新邮件进出,
		// total 会变, 按它截断会漏掉末尾几封。
		if ids == 0 || ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

// fillRange 返回服务端这一段的 id 数、实际新取回的封数与邮箱总数(calculateTotal 为假时为 0)。
func (s *Syncer) fillRange(
	ctx context.Context, accountID, mailboxID string, position, limit int, calculateTotal bool,
) (ids, fetched, total int, err error) {
	req := &jmap.Request{Context: ctx}
	req.Invoke(&email.Query{
		Account:        jmap.ID(accountID),
		Filter:         &email.FilterCondition{InMailbox: jmap.ID(mailboxID)},
		Sort:           []*email.SortComparator{{Property: "receivedAt", IsAscending: false}},
		Position:       int64(position),
		Limit:          uint64(limit),
		CalculateTotal: calculateTotal,
	})

	resp, err := s.do(req)
	if err != nil {
		return 0, 0, 0, err
	}
	q, ok := resp.Responses[0].Args.(*email.QueryResponse)
	if !ok {
		return 0, 0, 0, &Error{Code: CodeUnhandled, Msg: "unexpected response to Email/query"}
	}

	missing, err := s.store.MissingEmailIDs(ctx, accountID, idsToStrings(q.IDs))
	if err != nil {
		return 0, 0, 0, err
	}
	if len(missing) > 0 {
		want := make([]jmap.ID, len(missing))
		for i, id := range missing {
			want[i] = jmap.ID(id)
		}
		emails, err := s.fetchEmails(ctx, accountID, want)
		if err != nil {
			return 0, 0, 0, err
		}
		if err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
			return store.UpsertEmails(ctx, tx, accountID, emails)
		}); err != nil {
			return 0, 0, 0, err
		}
		fetched = len(emails)
	}
	return len(q.IDs), fetched, int(q.Total), nil
}
