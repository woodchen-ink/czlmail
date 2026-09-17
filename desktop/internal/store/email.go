package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// Address 是邮件地址的存储形态, 与 JMAP 的 EmailAddress 同构。
type Address struct {
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
}

// Email 是邮件的元数据行。正文不在此结构内: 列表视图只消费元数据,
// 正文由 SetEmailBody 在用户真正打开某封信时单独落库。
type Email struct {
	ID            string
	BlobID        string
	ThreadID      string
	Subject       string
	From          []Address
	To            []Address
	CC            []Address
	BCC           []Address
	ReplyTo       []Address
	SentAt        *time.Time
	ReceivedAt    time.Time
	Size          int64
	Preview       string
	HasAttachment bool
	MessageID     string
	InReplyTo     string
	MailboxIDs    []string
	Keywords      []string
}

// UpsertEmails 批量写入邮件元数据, 以及它们与邮箱、关键字的关联。
//
// 刻意不触碰 body_text / body_html / body_fetched_at: 一封邮件的元数据会因为
// 标记已读、移动邮箱等操作被反复更新, 而正文是不变的。若在元数据同步时一并写入
// 这三列, 已缓存的正文会被空值覆盖, 用户每次收到一个标志位变更都要重新下载全文。
func UpsertEmails(ctx context.Context, tx *sql.Tx, accountID string, emails []Email) error {
	if len(emails) == 0 {
		return nil
	}

	upsert, err := tx.PrepareContext(ctx, `
		INSERT INTO emails (
			account_id, id, blob_id, thread_id, subject,
			from_json, to_json, cc_json, bcc_json, reply_to_json,
			sent_at, received_at, size, preview, has_attachment,
			message_id, in_reply_to
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (account_id, id) DO UPDATE SET
			blob_id        = excluded.blob_id,
			thread_id      = excluded.thread_id,
			subject        = excluded.subject,
			from_json      = excluded.from_json,
			to_json        = excluded.to_json,
			cc_json        = excluded.cc_json,
			bcc_json       = excluded.bcc_json,
			reply_to_json  = excluded.reply_to_json,
			sent_at        = excluded.sent_at,
			received_at    = excluded.received_at,
			size           = excluded.size,
			preview        = excluded.preview,
			has_attachment = excluded.has_attachment,
			message_id     = excluded.message_id,
			in_reply_to    = excluded.in_reply_to`)
	if err != nil {
		return wrap(CodeQuery, "prepare email upsert", err)
	}
	defer upsert.Close()

	for i := range emails {
		e := &emails[i]

		addrs, err := encodeAddressLists(e)
		if err != nil {
			return err
		}

		var sentAt any
		if e.SentAt != nil {
			sentAt = e.SentAt.Unix()
		}

		if _, err := upsert.ExecContext(ctx,
			accountID, e.ID, e.BlobID, e.ThreadID, e.Subject,
			addrs[0], addrs[1], addrs[2], addrs[3], addrs[4],
			sentAt, e.ReceivedAt.Unix(), e.Size, e.Preview, boolToInt(e.HasAttachment),
			e.MessageID, e.InReplyTo,
		); err != nil {
			return wrap(CodeQuery, "upsert email", err)
		}

		// 邮箱归属与关键字都是集合语义: 服务端每次给的是全量, 不是增量。
		// 先删后插, 否则取消标记星标、移出某邮箱这类操作在本地永远不会消失。
		if err := replaceEmailMailboxes(ctx, tx, accountID, e.ID, e.MailboxIDs); err != nil {
			return err
		}
		if err := replaceEmailKeywords(ctx, tx, accountID, e.ID, e.Keywords); err != nil {
			return err
		}
	}
	return nil
}

func replaceEmailMailboxes(ctx context.Context, tx *sql.Tx, accountID, emailID string, mailboxIDs []string) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM email_mailboxes WHERE account_id = ? AND email_id = ?`,
		accountID, emailID,
	); err != nil {
		return wrap(CodeQuery, "clear email mailboxes", err)
	}

	for _, mid := range mailboxIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO email_mailboxes (account_id, email_id, mailbox_id) VALUES (?, ?, ?)`,
			accountID, emailID, mid,
		); err != nil {
			return wrap(CodeQuery, "insert email mailbox", err)
		}
	}
	return nil
}

func replaceEmailKeywords(ctx context.Context, tx *sql.Tx, accountID, emailID string, keywords []string) error {
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM email_keywords WHERE account_id = ? AND email_id = ?`,
		accountID, emailID,
	); err != nil {
		return wrap(CodeQuery, "clear email keywords", err)
	}

	for _, kw := range keywords {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO email_keywords (account_id, email_id, keyword) VALUES (?, ?, ?)`,
			accountID, emailID, kw,
		); err != nil {
			return wrap(CodeQuery, "insert email keyword", err)
		}
	}
	return nil
}

// DeleteEmails 清除邮件及其全部关联行。
func DeleteEmails(ctx context.Context, tx *sql.Tx, accountID string, ids []string) error {
	for _, id := range ids {
		for _, q := range []string{
			`DELETE FROM email_keywords  WHERE account_id = ? AND email_id = ?`,
			`DELETE FROM email_mailboxes WHERE account_id = ? AND email_id = ?`,
			`DELETE FROM emails          WHERE account_id = ? AND id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, accountID, id); err != nil {
				return wrap(CodeQuery, "delete email", err)
			}
		}
	}
	return nil
}

// Attachment 是邮件附件的元数据。内容本身留在服务器上, 按 BlobID 按需下载。
type Attachment struct {
	BlobID string `json:"blobId"`
	PartID string `json:"partId"`
	Name   string `json:"name"`
	Type   string `json:"type"`
	Size   int64  `json:"size"`
	// CID 非空的是内嵌在 HTML 正文里的图片(cid: 引用), 界面上不单独列出。
	CID    string `json:"cid"`
	Inline bool   `json:"inline"`
}

// SetEmailBody 写入按需拉取到的正文。body_fetched_at 非空即表示正文已缓存。
func SetEmailBody(ctx context.Context, tx *sql.Tx, accountID, emailID, text, html, structure string) error {
	return SetEmailContent(ctx, tx, accountID, emailID, text, html, structure, nil)
}

// SetEmailContent 写入正文与附件清单。
func SetEmailContent(ctx context.Context, tx *sql.Tx, accountID, emailID, text, html, structure string, attachments []Attachment) error {
	if attachments == nil {
		attachments = []Attachment{}
	}
	raw, err := json.Marshal(attachments)
	if err != nil {
		return wrap(CodeEncode, "encode attachments", err)
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE emails
		 SET body_text = ?, body_html = ?, body_structure = ?, attachments_json = ?, body_fetched_at = ?
		 WHERE account_id = ? AND id = ?`,
		text, html, structure, string(raw), time.Now().Unix(), accountID, emailID,
	)
	return wrap(CodeQuery, "write email body", err)
}

// SetEmailUnsubscribe 写入退订信息。u 为空值时存空串, 表示已检查过、没有退订头。
func SetEmailUnsubscribe(ctx context.Context, tx *sql.Tx, accountID, emailID string, u Unsubscribe) error {
	raw := ""
	if u != (Unsubscribe{}) {
		b, err := json.Marshal(u)
		if err != nil {
			return wrap(CodeEncode, "encode unsubscribe", err)
		}
		raw = string(b)
	}
	_, err := tx.ExecContext(ctx, `UPDATE emails SET list_unsubscribe = ? WHERE account_id = ? AND id = ?`, raw, accountID, emailID)
	return wrap(CodeQuery, "write unsubscribe", err)
}

// encodeAddressLists 按 from/to/cc/bcc/replyTo 的固定顺序序列化地址列表。
func encodeAddressLists(e *Email) ([5]string, error) {
	var out [5]string
	for i, list := range [][]Address{e.From, e.To, e.CC, e.BCC, e.ReplyTo} {
		if list == nil {
			list = []Address{}
		}
		b, err := json.Marshal(list)
		if err != nil {
			return out, wrap(CodeEncode, "encode address list", err)
		}
		out[i] = string(b)
	}
	return out, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
