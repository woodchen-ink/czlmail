package syncer

import (
	"context"
	"time"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/mail/email"
)

// SearchServer 交给服务端做全文检索(Email/query 的 text 条件), 覆盖整个账号的全部文件夹。
//
// 本地缓存只有首次引导的最近一批与翻过的页, 归档里的旧邮件多半不在本地, 只搜本地就会搜不到;
// 服务端还能命中没拉过正文的邮件。命中而本地没有的邮件把元数据补进缓存(不拉正文),
// 返回 id 按收件时间倒序。与翻页补取一样不写 state、不拿同步锁。
func (s *Syncer) SearchServer(ctx context.Context, accountID, text string, limit int) ([]string, error) {
	return s.QueryServer(ctx, accountID, EmailFilter{Text: text}, limit)
}

// EmailFilter 是服务端 Email/query 的条件子集, 各项之间为"且"。零值字段不参与过滤。
type EmailFilter struct {
	Text      string
	From      string
	To        string
	Subject   string
	MailboxID string
	After     time.Time
	Before    time.Time
	// Unread / Flagged / HasAttachment 为真时才过滤, 不能表达"必须没有"。
	Unread        bool
	Flagged       bool
	HasAttachment bool
}

// Empty 表示没有任何条件, 这时查询等于列出整个账号, 调用方应拒绝。
func (f EmailFilter) Empty() bool {
	return f == EmailFilter{}
}

// QueryServer 按条件查询服务端, 语义同 SearchServer: 命中而本地没有的补元数据进缓存, 返回 id 按收件时间倒序。
func (s *Syncer) QueryServer(ctx context.Context, accountID string, f EmailFilter, limit int) ([]string, error) {
	cond := &email.FilterCondition{
		Text: f.Text, From: f.From, To: f.To, Subject: f.Subject,
		InMailbox: jmap.ID(f.MailboxID), HasAttachment: f.HasAttachment,
	}
	if !f.After.IsZero() {
		cond.After = &f.After
	}
	if !f.Before.IsZero() {
		cond.Before = &f.Before
	}
	if f.Unread {
		cond.NotKeyword = "$seen"
	}
	if f.Flagged {
		cond.HasKeyword = "$flagged"
	}

	req := &jmap.Request{Context: ctx}
	req.Invoke(&email.Query{
		Account: jmap.ID(accountID),
		Filter:  cond,
		Sort:    []*email.SortComparator{{Property: "receivedAt", IsAscending: false}},
		Limit:   uint64(limit),
	})

	resp, err := s.do(req)
	if err != nil {
		return nil, err
	}
	q, ok := resp.Responses[0].Args.(*email.QueryResponse)
	if !ok {
		return nil, &Error{Code: CodeUnhandled, Msg: "unexpected response to Email/query"}
	}
	if _, err := s.cacheMissing(ctx, accountID, q.IDs); err != nil {
		return nil, err
	}
	return idsToStrings(q.IDs), nil
}
