package syncer

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/mail/email"
)

// maxRawMessageBytes 限制单封原始邮件读入内存的大小。
//
// 查看源码与导出都要拿到完整 RFC 5322 原文。服务器的附件上限是 50 MB 量级,
// 不设上限的话一封带大附件的邮件会整个载入内存再丢给前端渲染成文本。
const maxRawMessageBytes = 64 << 20

// RawMessage 下载邮件原文(message/rfc822)。
func (s *Syncer) RawMessage(ctx context.Context, accountID, blobID string) ([]byte, error) {
	if blobID == "" {
		return nil, fmt.Errorf("1130 email has no blob id")
	}

	rc, err := s.client.DownloadWithContext(ctx, jmap.ID(accountID), jmap.ID(blobID))
	if err != nil {
		return nil, wrap(CodeRequest, "download message", err)
	}
	defer rc.Close()

	// 多读一个字节用来判断是否超限, 而不是静默截断 —— 截断的 .eml 导出后
	// 看起来像个正常文件, 实际缺了附件尾部, 这种损坏最难被发现。
	data, err := io.ReadAll(io.LimitReader(rc, maxRawMessageBytes+1))
	if err != nil {
		return nil, wrap(CodeRequest, "read message", err)
	}
	if len(data) > maxRawMessageBytes {
		return nil, fmt.Errorf("1131 message exceeds %d MB", maxRawMessageBytes>>20)
	}
	return data, nil
}

// ImportMessage 把一封 RFC 5322 原文导入到指定邮箱。
//
// 走 Email/import 而不是 Email/set: import 让服务器按原文解析出完整结构,
// 保留原始的头部、编码与附件; set 需要客户端自己拆 MIME, 必然丢信息。
func (s *Syncer) ImportMessage(ctx context.Context, accountID, mailboxID string, raw []byte, receivedAt *time.Time) error {
	up, err := s.client.UploadWithContext(ctx, jmap.ID(accountID), bytes.NewReader(raw))
	if err != nil {
		return wrap(CodeRequest, "upload message", err)
	}

	// 导入的是历史邮件, 默认标记已读, 否则一次导入几百封会让未读数暴涨。
	req := &jmap.Request{Context: ctx}
	req.Invoke(&email.Import{
		Account: jmap.ID(accountID),
		Emails: map[string]*email.EmailImport{
			"imp": {
				BlobID:     up.ID,
				MailboxIDs: map[jmap.ID]bool{jmap.ID(mailboxID): true},
				Keywords:   map[string]bool{"$seen": true},
				ReceivedAt: receivedAt,
			},
		},
	})

	resp, err := s.do(req)
	if err != nil {
		return err
	}
	res, ok := resp.Responses[0].Args.(*email.ImportResponse)
	if !ok {
		return &Error{Code: CodeUnhandled, Msg: "unexpected response to Email/import"}
	}
	return firstSetError(res.NotCreated)
}

// SetKeyword 设置或清除任意关键字(即标签)。
func (s *Syncer) SetKeyword(ctx context.Context, accountID string, emailIDs []string, keyword string, set bool) error {
	if keyword == "" {
		return fmt.Errorf("1132 keyword is empty")
	}
	return s.patchKeyword(ctx, accountID, emailIDs, keyword, set)
}

// MarkJunk 把邮件移入目标邮箱并同时设置垃圾标记。
//
// $junk / $notjunk 是给服务器反垃圾学习用的信号(RFC 8621 注册的关键字),
// 只移动文件夹不打标记, 服务器的过滤器学不到这次纠正。两者必须在同一个
// patch 里完成, 否则中途失败会留下"在垃圾箱里却标着不是垃圾"的矛盾状态。
func (s *Syncer) MarkJunk(ctx context.Context, accountID string, emailIDs []string, targetMailboxID string, junk bool) error {
	if len(emailIDs) == 0 {
		return nil
	}
	if targetMailboxID == "" {
		return fmt.Errorf("1110 target mailbox is empty")
	}

	set, clear := "$junk", "$notjunk"
	if !junk {
		set, clear = clear, set
	}

	updates := make(map[jmap.ID]jmap.Patch, len(emailIDs))
	for _, id := range emailIDs {
		updates[jmap.ID(id)] = jmap.Patch{
			"mailboxIds":        map[string]bool{targetMailboxID: true},
			"keywords/" + set:   true,
			"keywords/" + clear: nil,
		}
	}
	return s.applySet(ctx, accountID, &email.Set{
		Account: jmap.ID(accountID),
		Update:  updates,
	})
}
