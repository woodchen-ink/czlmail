package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"strings"
	"time"

	"git.sr.ht/~rockorager/go-jmap"

	"github.com/woodchen-ink/czlmail/desktop/internal/capability"
	"github.com/woodchen-ink/czlmail/desktop/internal/otp"
	"github.com/woodchen-ink/czlmail/desktop/internal/upstream"
)

// EmailSummary 是列表视图的一行。刻意不含正文与收件人列表 ——
// 列表只渲染这些字段, 多取的每一列都要乘以一屏的行数。
type EmailSummary struct {
	// AccountID 是邮件所属账号。统一视图里各账号的邮件混在一个列表, id 只在账号内唯一。
	AccountID string    `json:"accountId"`
	ID        string    `json:"id"`
	ThreadID  string    `json:"threadId"`
	Subject   string    `json:"subject"`
	From      []Address `json:"from"`
	// To 供已发送、草稿这类文件夹在列表里显示收件人(发件人都是自己)。
	To            []Address `json:"to"`
	ReceivedAt    time.Time `json:"receivedAt"`
	Preview       string    `json:"preview"`
	HasAttachment bool      `json:"hasAttachment"`
	// Signed 表示邮件带 S/MIME 签名, 列表里显示盾牌。
	Signed bool `json:"signed"`
	// AttachmentNames 是附件文件名, 列表里显示成文件标签; 升级前缓存、还没补齐的邮件为空, 只显示回形针。
	AttachmentNames []string `json:"attachmentNames,omitempty"`
	// Code 是从主题与摘要 (阅读视图里是正文) 认出的验证码, 认不出为空。
	Code      string `json:"code,omitempty"`
	Size      int64  `json:"size"`
	IsUnread  bool   `json:"isUnread"`
	IsFlagged bool   `json:"isFlagged"`
	IsDraft   bool   `json:"isDraft"`
	// IsPinned 是 $pinned 关键字(与 Bulwark 相同), 置顶的邮件在列表里排在最前。
	IsPinned bool `json:"isPinned"`
	// ThreadCount 是同一会话的邮件总数(跨文件夹); 只有列表查询会填, 其它查询为 0。
	ThreadCount int `json:"threadCount"`
	// DraftCount 是同一会话里的草稿数(跨文件夹, 筛选同 ThreadCount, 含本行自己); 只有列表查询会填。
	// 界面据此在收件箱、已发送等文件夹的会话行上标出还有没写完的回复。
	DraftCount int `json:"draftCount"`
	// MailboxIDs 是邮件所在的文件夹; 只有搜索结果与会话成员会填, 界面据此标出邮件在哪、「移动到」排除所在文件夹。
	MailboxIDs []string `json:"mailboxIds,omitempty"`
}

// EmailDetail 是阅读视图。Body 可能为空, 表示正文尚未拉取。
type EmailDetail struct {
	EmailSummary
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

// summaryColumns 是邮件摘要的列, 顺序与 scanSummaries 一致。
const summaryColumns = `e.account_id, e.id, e.thread_id, e.subject, e.from_json, e.to_json, e.received_at,
	       e.preview, e.has_attachment, e.signed, e.size, e.attachment_names,` + keywordFlags

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
		SELECT `+summaryColumns+threadCountColumn+`
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
		SELECT `+summaryColumns+threadCountColumn+`
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

// discardedSQL 是「邮件只在回收站/垃圾邮件里」的条件, alias 为 emails 表的别名。
// 按服务器 role 判断, 不含按名称推断的类别(那要在 Go 里算, 没法放进列表查询)。
func discardedSQL(alias string) string {
	return strings.ReplaceAll(`NOT EXISTS (
		SELECT 1 FROM email_mailboxes dm
		LEFT JOIN mailboxes db ON db.account_id = dm.account_id AND db.id = dm.mailbox_id
		WHERE dm.account_id = @.account_id AND dm.email_id = @.id
		  AND COALESCE(LOWER(db.role), '') NOT IN ('trash', 'junk'))`, "@", alias)
}

// threadCountColumn 附加在列表查询末尾, scanSummaries 按列数识别。
// 删掉的往来不计: 收件箱里的会话行不该因为回收站里的几封而显示成多封; 行本身在回收站/垃圾邮件里时全部计入。
// 与 ThreadEmails 的筛选一致, 封数才对得上展开后的行数。
// 第二列是会话里的草稿数, 筛选相同 (删到回收站的草稿不算), 展开后标着「[草稿]」的行数与它一致。
var threadCountColumn = `,
	(SELECT COUNT(*) FROM emails t WHERE t.account_id = e.account_id AND t.thread_id = e.thread_id AND e.thread_id != ''
	   AND (` + discardedSQL("e") + ` OR NOT ` + discardedSQL("t") + `)),
	(SELECT COUNT(*) FROM emails t
	   JOIN email_keywords dk ON dk.account_id = t.account_id AND dk.email_id = t.id AND dk.keyword = '$draft'
	   WHERE t.account_id = e.account_id AND t.thread_id = e.thread_id AND e.thread_id != ''
	   AND (` + discardedSQL("e") + ` OR NOT ` + discardedSQL("t") + `))`

func scanSummaries(rows *sql.Rows) ([]EmailSummary, error) {
	var out []EmailSummary
	cols, _ := rows.Columns()
	withThread := len(cols) > 16
	for rows.Next() {
		var e EmailSummary
		var fromJSON, toJSON string
		var receivedAt int64
		var hasAttachment, signed, unread, flagged, draft, pinned int
		var names sql.NullString

		dest := []any{
			&e.AccountID, &e.ID, &e.ThreadID, &e.Subject, &fromJSON, &toJSON, &receivedAt,
			&e.Preview, &hasAttachment, &signed, &e.Size, &names,
			&unread, &flagged, &draft, &pinned,
		}
		if withThread {
			dest = append(dest, &e.ThreadCount, &e.DraftCount)
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, wrap(CodeQuery, "scan email summary", err)
		}

		if err := json.Unmarshal([]byte(fromJSON), &e.From); err != nil {
			return nil, wrap(CodeDecode, "decode from addresses", err)
		}
		if err := json.Unmarshal([]byte(toJSON), &e.To); err != nil {
			return nil, wrap(CodeDecode, "decode to addresses", err)
		}
		e.ReceivedAt = time.Unix(receivedAt, 0)
		e.HasAttachment = hasAttachment != 0
		e.Signed = signed != 0
		e.IsUnread = unread != 0
		e.IsFlagged = flagged != 0
		e.IsDraft = draft != 0
		e.IsPinned = pinned != 0
		if e.HasAttachment && names.Valid {
			_ = json.Unmarshal([]byte(names.String), &e.AttachmentNames)
		}
		e.Code = otp.Extract(e.Subject, e.Preview)

		out = append(out, e)
	}
	return out, wrap(CodeQuery, "iterate email summaries", rows.Err())
}

// LatestInThread 返回会话里最新一封邮件的 id, 不在回收站/垃圾邮件里的优先; 本地没有时为空。
func (s *Store) LatestInThread(ctx context.Context, accountID, threadID string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `
		SELECT e.id FROM emails e
		WHERE e.account_id = ? AND e.thread_id = ?
		ORDER BY `+discardedSQL("e")+` ASC, e.received_at DESC
		LIMIT 1`,
		accountID, threadID).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, wrap(CodeQuery, "query latest in thread", err)
}

// ThreadEmails 按时间正序列出 anchor 所在会话的邮件(跨文件夹), 最多 limit 封。
// anchor 是界面上代表会话的那封: 它不在回收站/垃圾邮件里时, 只在那里的往来不列出
// (否则刚删掉的邮件仍留在展开区, 看起来像没删掉); 它本身在回收站里时全部列出。
func (s *Store) ThreadEmails(ctx context.Context, accountID, anchorID string, limit int) ([]EmailSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+summaryColumns+`
		FROM emails a
		JOIN emails e ON e.account_id = a.account_id AND e.thread_id = a.thread_id
		WHERE a.account_id = ? AND a.id = ? AND a.thread_id != ''
		  AND (`+discardedSQL("a")+` OR NOT `+discardedSQL("e")+`)
		ORDER BY e.received_at ASC
		LIMIT ?`,
		accountID, anchorID, limit)
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
	var hasAttachment, signed, unread, flagged, draft, pinned int
	var bodyText, bodyHTML sql.NullString
	var bodyFetchedAt sql.NullInt64
	var attachmentsJSON string
	var unsubscribe sql.NullString

	err := s.db.QueryRowContext(ctx, `
		SELECT e.id, e.thread_id, e.subject, e.from_json, e.received_at,
		       e.preview, e.has_attachment, e.signed, e.size,`+keywordFlags+`,
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
		&d.Preview, &hasAttachment, &signed, &d.Size,
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

	d.AccountID = accountID
	d.ReceivedAt = time.Unix(receivedAt, 0)
	d.HasAttachment = hasAttachment != 0
	d.Signed = signed != 0
	d.IsUnread = unread != 0
	d.IsFlagged = flagged != 0
	d.IsDraft = draft != 0
	d.IsPinned = pinned != 0
	d.BodyText = bodyText.String
	// 正文拉过时按正文认, 摘要只有开头一两百字, 验证码可能在更后面。
	if bodyText.Valid && bodyText.String != "" {
		d.Code = otp.Extract(d.Subject, bodyText.String)
	} else {
		d.Code = otp.Extract(d.Subject, d.Preview)
	}
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
	// SignIn 是账号所属登录的前缀 (主登录为空), 由上层按账号 id 填写, 不存库。
	SignIn string `json:"signIn"`
	// Primary 表示会话 primaryAccounts 的邮件账号指向它 (登录的默认邮箱)。
	Primary bool `json:"primary"`
	// Features 是账号所在服务器公布的功能, 取自上次连上时存下的能力 (离线时照用); 从没连上过时全部为真。
	Features capability.Features `json:"features"`
	// Upstream 是 czlmail 网关账号背后上游邮箱的状态, 取自同一份能力; 不是网关账号时为空。
	Upstream *upstream.Info `json:"upstream"`
}

// Accounts 读出已缓存的账号, 个人账号排在共享账号前面。
func (s *Store) Accounts(ctx context.Context) ([]Account, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, is_personal, is_primary, account_capabilities FROM accounts
		ORDER BY is_personal DESC, is_primary DESC, name`)
	if err != nil {
		return nil, wrap(CodeQuery, "query accounts", err)
	}
	defer rows.Close()

	var out []Account
	for rows.Next() {
		var a Account
		var personal, primary int
		var caps string
		if err := rows.Scan(&a.ID, &a.Name, &personal, &primary, &caps); err != nil {
			return nil, wrap(CodeQuery, "scan account", err)
		}
		a.IsPersonal = personal != 0
		a.Primary = primary != 0
		a.Features = capability.Decode(caps)
		a.Upstream = upstream.Decode(caps)
		out = append(out, a)
	}
	return out, wrap(CodeQuery, "iterate accounts", rows.Err())
}

// UpsertAccounts 写入会话里发现的账号。capsByAccount 是各账号的原始能力表 (account_capabilities 列存
// URI → 能力对象; 早期版本只存 URI 数组, 读取两种都认, 见 capability.Decode)。
func UpsertAccounts(ctx context.Context, tx *sql.Tx, accounts []Account, capsByAccount map[string]map[jmap.URI]json.RawMessage) error {
	now := time.Now().Unix()
	for _, a := range accounts {
		caps, err := capability.Encode(capsByAccount[a.ID])
		if err != nil {
			return wrap(CodeEncode, "encode account capabilities", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO accounts (id, name, is_personal, is_primary, account_capabilities, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET
				name                 = excluded.name,
				is_personal          = excluded.is_personal,
				is_primary           = excluded.is_primary,
				account_capabilities = excluded.account_capabilities,
				updated_at           = excluded.updated_at`,
			a.ID, a.Name, boolToInt(a.IsPersonal), boolToInt(a.Primary), caps, now,
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
