package syncer

import (
	"context"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/mail/email"
)

// SearchServer 交给服务端做全文检索(Email/query 的 text 条件), 覆盖整个账号的全部文件夹。
//
// 本地缓存只有首次引导的最近一批与翻过的页, 归档里的旧邮件多半不在本地, 只搜本地就会搜不到;
// 服务端还能命中没拉过正文的邮件。命中而本地没有的邮件把元数据补进缓存(不拉正文),
// 返回 id 按收件时间倒序。与翻页补取一样不写 state、不拿同步锁。
func (s *Syncer) SearchServer(ctx context.Context, accountID, text string, limit int) ([]string, error) {
	req := &jmap.Request{Context: ctx}
	req.Invoke(&email.Query{
		Account: jmap.ID(accountID),
		Filter:  &email.FilterCondition{Text: text},
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
