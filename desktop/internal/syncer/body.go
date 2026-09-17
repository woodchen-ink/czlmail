package syncer

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/mail/email"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// maxBodyBytes 限制单封邮件取回的正文大小。
//
// 营销邮件的 HTML 正文动辄几 MB, 而阅读窗格根本渲染不完。截断后仍然可读,
// 需要完整原文的场景走下载 blob, 不走这条路径。
const maxBodyBytes = 1 << 20 // 1 MiB

// FetchBody 按需拉取单封邮件的正文并写入缓存。
//
// 只在用户真正打开某封邮件时调用。元数据同步刻意不带正文, 否则一次增量同步
// 会从几十 KB 膨胀到几十 MB。
func (s *Syncer) FetchBody(ctx context.Context, accountID, emailID string) error {
	n, err := s.FetchBodies(ctx, accountID, []string{emailID})
	if err == nil && n == 0 {
		return &Error{Code: CodeUnhandled, Msg: "email not found on server"}
	}
	return err
}

// bodyBatch 是一次请求取回的正文封数。正文单封上限 1 MiB, 批次过大会让单个响应动辄十几 MB。
const bodyBatch = 10

// FetchBodies 批量拉取正文并写入缓存, 返回实际写入的封数。用于打开邮件与后台预取。
func (s *Syncer) FetchBodies(ctx context.Context, accountID string, emailIDs []string) (int, error) {
	written := 0
	for start := 0; start < len(emailIDs); start += bodyBatch {
		batch := emailIDs[start:min(start+bodyBatch, len(emailIDs))]
		ids := make([]jmap.ID, len(batch))
		for i, id := range batch {
			ids[i] = jmap.ID(id)
		}
		req := &jmap.Request{Context: ctx}
		req.Invoke(&email.Get{
			Account:             jmap.ID(accountID),
			IDs:                 ids,
			Properties:          []string{"id", "bodyStructure", "textBody", "htmlBody", "bodyValues", "attachments", "headers"},
			FetchTextBodyValues: true,
			FetchHTMLBodyValues: true,
			MaxBodyValueBytes:   maxBodyBytes,
		})
		resp, err := s.do(req)
		if err != nil {
			return written, err
		}
		got, ok := resp.Responses[0].Args.(*email.GetResponse)
		if !ok {
			return written, &Error{Code: CodeUnhandled, Msg: "unexpected response to Email/get"}
		}
		if err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
			for _, msg := range got.List {
				structure := "{}"
				if msg.BodyStructure != nil {
					if b, err := json.Marshal(msg.BodyStructure); err == nil {
						structure = string(b)
					}
				}
				if err := store.SetEmailContent(ctx, tx, accountID, string(msg.ID),
					joinBodyValues(msg, msg.TextBody), joinBodyValues(msg, msg.HTMLBody),
					structure, toStoreAttachments(msg.Attachments)); err != nil {
					return err
				}
				if err := store.SetEmailUnsubscribe(ctx, tx, accountID, string(msg.ID), ParseUnsubscribe(msg.Headers)); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return written, err
		}
		written += len(got.List)
	}
	return written, nil
}

// toStoreAttachments 把 JMAP 的 attachments 属性转成存储形态。
//
// JMAP 的 attachments 包含"不属于正文"的全部部件, 其中有内嵌在 HTML 里的
// cid 图片。它们要保留(正文渲染时需要按 cid 找到 blob), 但标为 Inline,
// 界面据此不在附件栏里重复列出。
func toStoreAttachments(parts []*email.BodyPart) []store.Attachment {
	out := make([]store.Attachment, 0, len(parts))
	for _, p := range parts {
		if p == nil || p.BlobID == "" {
			continue
		}
		cid := strings.Trim(p.CID, "<>")
		out = append(out, store.Attachment{
			BlobID: string(p.BlobID),
			PartID: p.PartID,
			Name:   p.Name,
			Type:   strings.ToLower(p.Type),
			Size:   int64(p.Size),
			CID:    cid,
			Inline: cid != "" && p.Disposition != "attachment",
		})
	}
	return out
}

// joinBodyValues 按 parts 给出的顺序拼接正文片段。
//
// JMAP 把正文拆成若干 part, bodyValues 以 partId 为键单独返回。一封邮件的
// textBody 可能由多段组成(例如正文加签名), 只取第一段会丢内容。
func joinBodyValues(msg *email.Email, parts []*email.BodyPart) string {
	if len(parts) == 0 || msg.BodyValues == nil {
		return ""
	}

	var out []byte
	for _, part := range parts {
		if part == nil || part.PartID == "" {
			continue
		}
		val, ok := msg.BodyValues[part.PartID]
		if !ok || val == nil {
			continue
		}
		if len(out) > 0 {
			out = append(out, '\n')
		}
		out = append(out, val.Value...)
	}
	return string(out)
}

// FetchListHeaders 只取邮件头来补齐退订信息, 用于正文在加这项功能之前就已缓存的邮件。
func (s *Syncer) FetchListHeaders(ctx context.Context, accountID, emailID string) (store.Unsubscribe, error) {
	req := &jmap.Request{Context: ctx}
	req.Invoke(&email.Get{Account: jmap.ID(accountID), IDs: []jmap.ID{jmap.ID(emailID)}, Properties: []string{"id", "headers"}})
	resp, err := s.do(req)
	if err != nil {
		return store.Unsubscribe{}, err
	}
	got, ok := resp.Responses[0].Args.(*email.GetResponse)
	if !ok || len(got.List) == 0 {
		return store.Unsubscribe{}, &Error{Code: CodeUnhandled, Msg: "email not found on server"}
	}
	u := ParseUnsubscribe(got.List[0].Headers)
	err = s.store.WithTx(ctx, func(tx *sql.Tx) error {
		return store.SetEmailUnsubscribe(ctx, tx, accountID, emailID, u)
	})
	return u, err
}

// ParseUnsubscribe 从原始邮件头解析 List-Unsubscribe 与 List-Unsubscribe-Post。
// 只接受 https(或 http) 与 mailto 两种地址; 一键退订(RFC 8058)要求 https。
func ParseUnsubscribe(headers []*email.Header) store.Unsubscribe {
	var u store.Unsubscribe
	var post bool
	for _, h := range headers {
		if h == nil {
			continue
		}
		switch strings.ToLower(h.Name) {
		case "list-unsubscribe":
			for _, part := range strings.Split(unfold(h.Value), ",") {
				v := strings.TrimSpace(part)
				v = strings.TrimSuffix(strings.TrimPrefix(v, "<"), ">")
				lower := strings.ToLower(v)
				switch {
				case u.HTTP == "" && (strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://")):
					u.HTTP = v
				case u.Mailto == "" && strings.HasPrefix(lower, "mailto:"):
					u.Mailto = v
				}
			}
		case "list-unsubscribe-post":
			post = strings.Contains(strings.ToLower(unfold(h.Value)), "list-unsubscribe=one-click")
		}
	}
	u.OneClick = post && strings.HasPrefix(strings.ToLower(u.HTTP), "https://")
	return u
}

func unfold(v string) string {
	return strings.Join(strings.Fields(v), " ")
}
