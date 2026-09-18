package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"time"
)

// EmailSummary 是列表视图的一行。刻意不含正文与收件人列表 ——
// 列表只渲染这些字段, 多取的每一列都要乘以一屏的行数。
type EmailSummary struct {
	ID            string    `json:"id"`
	ThreadID      string    `json:"threadId"`
	Subject       string    `json:"subject"`
	From          []Address `json:"from"`
	ReceivedAt    time.Time `json:"receivedAt"`
	Preview       string    `json:"preview"`
	HasAttachment bool      `json:"hasAttachment"`
	Size          int64     `json:"size"`
	IsUnread      bool      `json:"isUnread"`
	IsFlagged     bool      `json:"isFlagged"`
	IsDraft       bool      `json:"isDraft"`
	// IsPinned 是 $pinned 关键字(与 Bulwark 相同), 置顶的邮件在列表里排在最前。
	IsPinned bool `json:"isPinned"`
	// ThreadCount 是同一会话的邮件总数(跨文件夹); 只有列表查询会填, 其它查询为 0。
	ThreadCount int `json:"threadCount"`
}

// EmailDetail 是阅读视图。Body 可能为空, 表示正文尚未拉取。
type EmailDetail struct {
	EmailSummary
	To      []Address `json:"to"`
	CC      []Address `json:"cc"`
	BCC     []Address `json:"bcc"`
	ReplyTo []Address `json:"replyTo"`
	BlobID  string    `json:"blobId"`
	// MessageID 是邮件头里的 Message-ID, 回复时填进 In-Reply-To。
	// 不能用 JMAP 的 id 代替 —— 那是服务器内部标识, 对方客户端不认识。
	MessageID string `json:"messageId"`
	InReplyTo string `json:"inReplyTo"`
	// Attachments 在正文拉取后才有内容。
	Attachments []Attachment `json:"attachments"`
	// Labels 是自定义关键字, 不含 $ 开头的系统标志。
	Labels      []string `json:"labels"`
	BodyText    string   `json:"bodyText"`
	BodyHTML    string   `json:"bodyHtml"`
	BodyFetched bool     `json:"bodyFetched"`
	// Unsubscribe 是 List-Unsubscribe 解析结果; nil 表示还没检查过。
	Unsubscribe *Unsubscribe `json:"unsubscribe"`
	// MDNSent 表示已回复或已忽略已读回执请求($mdnsent)。
	MDNSent bool `json:"mdnSent"`
}

// Unsubscribe 是邮件的退订方式(RFC 2369 / RFC 8058)。三个字段都为空表示邮件没有退订头。
type Unsubscribe struct {
	HTTP     string `json:"http"`
	Mailto   string `json:"mailto"`
	OneClick bool   `json:"oneClick"`
	// ReceiptTo 是发件人请求已读回执的地址(Disposition-Notification-To), 与退订信息一起从邮件头解析。
	ReceiptTo string `json:"receiptTo"`
}

// 已读与星标是"某个 keyword 是否存在", 用 EXISTS 子查询而不是 JOIN 聚合:
// 一封邮件可以有任意多个自定义标签, JOIN 后再 GROUP BY 会让排序无法走
// idx_emails_received, 在万级邮箱上直接退化成全表扫描。
const keywordFlags = `
	NOT EXISTS (SELECT 1 FROM email_keywords k
	            WHERE k.account_id = e.account_id AND k.email_id = e.id AND k.keyword = '$seen')    AS is_unread,
	    EXISTS (SELECT 1 FROM email_keywords k
	            WHERE k.account_id = e.account_id AND k.email_id = e.id AND k.keyword = '$flagged') AS is_flagged,
	    EXISTS (SELECT 1 FROM email_keywords k
	            WHERE k.account_id = e.account_id AND k.email_id = e.id AND k.keyword = '$draft')   AS is_draft,
	    EXISTS (SELECT 1 FROM email_keywords k
	            WHERE k.account_id = e.account_id AND k.email_id = e.id AND k.keyword = '$pinned')  AS is_pinned`

// EmailsByMailbox 分页取某邮箱的邮件: 置顶的在最前, 其余按收件时间倒序。
//
// 不在一条 SQL 里 ORDER BY is_pinned: 那样要对整个邮箱逐封算关键字再排序, 用不上
// idx_emails_received。置顶的邮件很少, 单独查出来拼在前面, offset 仍按合并后的顺序计。
func (s *Store) EmailsByMailbox(
	ctx context.Context, accountID, mailboxID string, limit, offset int,
) ([]EmailSummary, error) {
	pinned, err := s.pinnedInMailbox(ctx, accountID, mailboxID)
	if err != nil {
		return nil, err
	}
	var out []EmailSummary
	if offset < len(pinned) {
		out = pinned[offset:min(len(pinned), offset+limit)]
		limit -= len(out)
		offset = 0
	} else {
		offset -= len(pinned)
	}
	if limit <= 0 {
		return out, nil
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.thread_id, e.subject, e.from_json, e.received_at,
		       e.preview, e.has_attachment, e.size,`+keywordFlags+threadCountColumn+`
		FROM emails e
		JOIN email_mailboxes m
		  ON m.account_id = e.account_id AND m.email_id = e.id
		WHERE e.account_id = ? AND m.mailbox_id = ?
		  AND NOT EXISTS (SELECT 1 FROM email_keywords p
		                  WHERE p.account_id = e.account_id AND p.email_id = e.id AND p.keyword = '$pinned')
		ORDER BY e.received_at DESC
		LIMIT ? OFFSET ?`,
		accountID, mailboxID, limit, offset)
	if err != nil {
		return nil, wrap(CodeQuery, "query emails by mailbox", err)
	}
	defer rows.Close()

	rest, err := scanSummaries(rows)
	if err != nil {
		return nil, err
	}
	return append(out, rest...), nil
}

// pinnedInMailbox 列出邮箱里置顶的邮件, 按收件时间倒序。
func (s *Store) pinnedInMailbox(ctx context.Context, accountID, mailboxID string) ([]EmailSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.thread_id, e.subject, e.from_json, e.received_at,
		       e.preview, e.has_attachment, e.size,`+keywordFlags+threadCountColumn+`
		FROM email_keywords p
		JOIN email_mailboxes m
		  ON m.account_id = p.account_id AND m.email_id = p.email_id
		JOIN emails e
		  ON e.account_id = p.account_id AND e.id = p.email_id
		WHERE p.account_id = ? AND p.keyword = '$pinned' AND m.mailbox_id = ?
		ORDER BY e.received_at DESC`,
		accountID, mailboxID)
	if err != nil {
		return nil, wrap(CodeQuery, "query pinned emails", err)
	}
	defer rows.Close()
	return scanSummaries(rows)
}

// threadCountColumn 附加在列表查询末尾, scanSummaries 按列数识别。
const threadCountColumn = `,
	(SELECT COUNT(*) FROM emails t WHERE t.account_id = e.account_id AND t.thread_id = e.thread_id AND e.thread_id != '')`

func scanSummaries(rows *sql.Rows) ([]EmailSummary, error) {
	var out []EmailSummary
	cols, _ := rows.Columns()
	withThread := len(cols) > 12
	for rows.Next() {
		var e EmailSummary
		var fromJSON string
		var receivedAt int64
		var hasAttachment, unread, flagged, draft, pinned int

		dest := []any{
			&e.ID, &e.ThreadID, &e.Subject, &fromJSON, &receivedAt,
			&e.Preview, &hasAttachment, &e.Size,
			&unread, &flagged, &draft, &pinned,
		}
		if withThread {
			dest = append(dest, &e.ThreadCount)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, wrap(CodeQuery, "scan email summary", err)
		}

		if err := json.Unmarshal([]byte(fromJSON), &e.From); err != nil {
			return nil, wrap(CodeDecode, "decode from addresses", err)
		}
		e.ReceivedAt = time.Unix(receivedAt, 0)
		e.HasAttachment = hasAttachment != 0
		e.IsUnread = unread != 0
		e.IsFlagged = flagged != 0
		e.IsDraft = draft != 0
		e.IsPinned = pinned != 0

		out = append(out, e)
	}
	return out, wrap(CodeQuery, "iterate email summaries", rows.Err())
}

// ThreadEmails 按时间正序列出同一会话的邮件(跨文件夹), 最多 limit 封。
func (s *Store) ThreadEmails(ctx context.Context, accountID, threadID string, limit int) ([]EmailSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.thread_id, e.subject, e.from_json, e.received_at,
		       e.preview, e.has_attachment, e.size,`+keywordFlags+`
		FROM emails e
		WHERE e.account_id = ? AND e.thread_id = ?
		ORDER BY e.received_at ASC
		LIMIT ?`,
		accountID, threadID, limit)
	if err != nil {
		return nil, wrap(CodeQuery, "query thread", err)
	}
	defer rows.Close()
	return scanSummaries(rows)
}

// Email 读单封邮件的完整信息。正文未缓存时 BodyFetched 为 false,
// 调用方据此决定是否发起一次按需拉取。
func (s *Store) Email(ctx context.Context, accountID, emailID string) (*EmailDetail, error) {
	var d EmailDetail
	var fromJSON, toJSON, ccJSON, bccJSON, replyToJSON string
	var receivedAt int64
	var hasAttachment, unread, flagged, draft, pinned int
	var bodyText, bodyHTML sql.NullString
	var bodyFetchedAt sql.NullInt64
	var attachmentsJSON string
	var unsubscribe sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT e.id, e.thread_id, e.subject, e.from_json, e.received_at,
		       e.preview, e.has_attachment, e.size,`+keywordFlags+`,
		       e.to_json, e.cc_json, e.bcc_json, e.reply_to_json,
		       e.blob_id, e.message_id, e.in_reply_to, e.attachments_json,
		       e.body_text, e.body_html, e.body_fetched_at, e.list_unsubscribe,
		       EXISTS (SELECT 1 FROM email_keywords k
		               WHERE k.account_id = e.account_id AND k.email_id = e.id AND k.keyword = '$mdnsent')
		FROM emails e
		WHERE e.account_id = ? AND e.id = ?`,
		accountID, emailID,
	).Scan(
		&d.ID, &d.ThreadID, &d.Subject, &fromJSON, &receivedAt,
		&d.Preview, &hasAttachment, &d.Size,
		&unread, &flagged, &draft, &pinned,
		&toJSON, &ccJSON, &bccJSON, &replyToJSON,
		&d.BlobID, &d.MessageID, &d.InReplyTo, &attachmentsJSON,
		&bodyText, &bodyHTML, &bodyFetchedAt, &unsubscribe, &d.MDNSent,
	)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, wrap(CodeQuery, "query email", err)
	}

	for _, pair := range []struct {
		raw string
		dst *[]Address
	}{
		{fromJSON, &d.From}, {toJSON, &d.To}, {ccJSON, &d.CC},
		{bccJSON, &d.BCC}, {replyToJSON, &d.ReplyTo},
	} {
		if err := json.Unmarshal([]byte(pair.raw), pair.dst); err != nil {
			return nil, wrap(CodeDecode, "decode address list", err)
		}
	}

	d.ReceivedAt = time.Unix(receivedAt, 0)
	d.HasAttachment = hasAttachment != 0
	d.IsUnread = unread != 0
	d.IsFlagged = flagged != 0
	d.IsDraft = draft != 0
	d.IsPinned = pinned != 0
	d.BodyText = bodyText.String
	d.BodyHTML = bodyHTML.String
	// 旧缓存里纯文本邮件的 body_html 是同一份纯文本(见 syncer.joinBodyValues 的说明),
	// 当 HTML 渲染会丢掉全部换行。正文不重新拉取, 读的时候顺手丢掉这份假 HTML。
	if d.BodyHTML == d.BodyText && !looksLikeHTML(d.BodyHTML) {
		d.BodyHTML = ""
	}
	d.BodyFetched = bodyFetchedAt.Valid
	if unsubscribe.Valid {
		d.Unsubscribe = &Unsubscribe{}
		if unsubscribe.String != "" {
			_ = json.Unmarshal([]byte(unsubscribe.String), d.Unsubscribe)
		}
	}
	if err := json.Unmarshal([]byte(attachmentsJSON), &d.Attachments); err != nil {
		return nil, wrap(CodeDecode, "decode attachments", err)
	}

	labels, err := s.emailLabels(ctx, accountID, emailID)
	if err != nil {
		return nil, err
	}
	d.Labels = labels

	return &d, nil
}

func (s *Store) emailLabels(ctx context.Context, accountID, emailID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT keyword FROM email_keywords
		WHERE account_id = ? AND email_id = ? AND keyword NOT LIKE '$%'
		ORDER BY keyword`, accountID, emailID)
	if err != nil {
		return nil, wrap(CodeQuery, "query email labels", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var kw string
		if err := rows.Scan(&kw); err != nil {
			return nil, wrap(CodeQuery, "scan email label", err)
		}
		out = append(out, kw)
	}
	return out, wrap(CodeQuery, "iterate email labels", rows.Err())
}

// Labels 列出账号内出现过的全部自定义标签。
func (s *Store) Labels(ctx context.Context, accountID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT keyword FROM email_keywords
		WHERE account_id = ? AND keyword NOT LIKE '$%'
		ORDER BY keyword`, accountID)
	if err != nil {
		return nil, wrap(CodeQuery, "query labels", err)
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var kw string
		if err := rows.Scan(&kw); err != nil {
			return nil, wrap(CodeQuery, "scan label", err)
		}
		out = append(out, kw)
	}
	return out, wrap(CodeQuery, "iterate labels", rows.Err())
}

// Account 是账号在界面上的呈现形态。
type Account struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	IsPersonal bool   `json:"isPersonal"`
}

// Accounts 读出已缓存的账号, 个人账号排在共享账号前面。
func (s *Store) Accounts(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, is_personal FROM accounts
		ORDER BY is_personal DESC, name`)
	if err != nil {
		return nil, wrap(CodeQuery, "query accounts", err)
	}
	defer rows.Close()

	var out []Account
	for rows.Next() {
		var a Account
		var personal int
		if err := rows.Scan(&a.ID, &a.Name, &personal); err != nil {
			return nil, wrap(CodeQuery, "scan account", err)
		}
		a.IsPersonal = personal != 0
		out = append(out, a)
	}
	return out, wrap(CodeQuery, "iterate accounts", rows.Err())
}

// UpsertAccounts 写入会话里发现的账号。
func UpsertAccounts(ctx context.Context, tx *sql.Tx, accounts []Account, capsByAccount map[string][]string) error {
	now := time.Now().Unix()
	for _, a := range accounts {
		caps, err := json.Marshal(capsByAccount[a.ID])
		if err != nil {
			return wrap(CodeEncode, "encode account capabilities", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO accounts (id, name, is_personal, account_capabilities, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET
				name                 = excluded.name,
				is_personal          = excluded.is_personal,
				account_capabilities = excluded.account_capabilities,
				updated_at           = excluded.updated_at`,
			a.ID, a.Name, boolToInt(a.IsPersonal), string(caps), now,
		); err != nil {
			return wrap(CodeQuery, "upsert account", err)
		}
	}
	return nil
}

// htmlTag 匹配一个成形的标签。写得严是为了不把纯文本误判成 HTML:
// "<zn@czl.net>"、"<https://…>" 与 "a < b" 都不算标签。
var htmlTag = regexp.MustCompile(`(?s)<(?:/[a-zA-Z]|!--|[a-zA-Z][a-zA-Z0-9]*(?:\s[^<>]*)?/?>)`)

func looksLikeHTML(s string) bool {
	return htmlTag.MatchString(s)
}
