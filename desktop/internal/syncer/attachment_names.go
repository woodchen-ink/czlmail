package syncer

import (
	"context"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/mail/email"
)

// attachmentNamesBatch 是补附件名时一次请求的封数; 只取 id 与结构树, 每封几十字节。
const attachmentNamesBatch = 200

// BackfillAttachmentNames 给升级前缓存、还没有附件名的带附件邮件补上附件名, 返回补过的封数。
// 新同步的邮件在元数据里就带着, 这里只补历史数据; 补完一封后它不再满足筛选条件, 所以每次启动跑一遍即可。
// 不写 state、不拿同步锁: 只改 attachment_names 一列, 与元数据同步写的是同一个值。
func (s *Syncer) BackfillAttachmentNames(ctx context.Context, accountID string) (int, error) {
	total := 0
	for {
		ids, err := s.store.EmailsMissingAttachmentNames(ctx, accountID, attachmentNamesBatch)
		if err != nil || len(ids) == 0 {
			return total, err
		}
		want := make([]jmap.ID, len(ids))
		for i, id := range ids {
			want[i] = jmap.ID(id)
		}
		req := &jmap.Request{Context: ctx}
		req.Invoke(&email.Get{
			Account:        s.server(accountID),
			IDs:            want,
			Properties:     []string{"id", "bodyStructure"},
			BodyProperties: emailBodyProperties,
		})
		resp, err := s.do(req)
		if err != nil {
			return total, err
		}
		got, ok := resp.Responses[0].Args.(*email.GetResponse)
		if !ok {
			return total, &Error{Code: CodeUnhandled, Msg: "unexpected response to Email/get"}
		}
		names := make(map[string][]string, len(got.List))
		for _, e := range got.List {
			names[string(e.ID)] = attachmentNames(e.BodyStructure, nil)
		}
		// 服务器没返回的 (已删除) 写空数组, 否则每批都会重新查到它们, 循环停不下来。
		if err := s.writing(func() error { return s.store.SetAttachmentNames(ctx, accountID, ids, names) }); err != nil {
			return total, err
		}
		total += len(ids)
	}
}
