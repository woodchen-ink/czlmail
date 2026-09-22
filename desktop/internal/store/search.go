package store

import (
	"context"
	"strings"
)

// minSearchLength 是 trigram 分词器能处理的最短查询。
// 短于三个字符时 FTS5 直接返回空集, 此处改走前缀匹配, 免得用户以为搜索坏了。
const minSearchLength = 3

// SearchEmails 在本地缓存里做全文检索, 结果按收件时间倒序。
//
// 命中范围是主题、摘要与已缓存的正文。未拉取过正文的邮件只能靠主题与摘要命中 ——
// 这是按需拉正文的必然代价, 界面上不必解释, 但要知道搜不到不等于没有。
//
// 不按 bm25 相关度排: 邮件检索里相关度几乎没有意义(同一个单号在通知邮件里出现几次
// 纯属偶然), 而时间有 —— 用户搜一个单号是想看最新那封。超出 limit 时留最新的一批。
func (s *Store) SearchEmails(ctx context.Context, accountID, query string, limit int) ([]EmailSummary, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}

	if len([]rune(query)) < minSearchLength {
		return s.searchByPrefix(ctx, accountID, query, limit)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.thread_id, e.subject, e.from_json, e.received_at,
		       e.preview, e.has_attachment, e.size,`+keywordFlags+threadCountColumn+`
		FROM emails_fts f
		JOIN emails e ON e.rowid = f.rowid
		WHERE emails_fts MATCH ? AND e.account_id = ?
		ORDER BY e.received_at DESC
		LIMIT ?`,
		ftsQuote(query), accountID, limit)
	if err != nil {
		return nil, wrap(CodeQuery, "search emails", err)
	}
	defer rows.Close()

	return scanSummaries(rows)
}

// searchByPrefix 处理过短的查询, 只在主题与发件人上做子串匹配。
func (s *Store) searchByPrefix(ctx context.Context, accountID, query string, limit int) ([]EmailSummary, error) {
	pattern := "%" + escapeLike(query) + "%"

	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.thread_id, e.subject, e.from_json, e.received_at,
		       e.preview, e.has_attachment, e.size,`+keywordFlags+threadCountColumn+`
		FROM emails e
		WHERE e.account_id = ?
		  AND (e.subject LIKE ? ESCAPE '\' OR e.from_json LIKE ? ESCAPE '\')
		ORDER BY e.received_at DESC
		LIMIT ?`,
		accountID, pattern, pattern, limit)
	if err != nil {
		return nil, wrap(CodeQuery, "search emails by prefix", err)
	}
	defer rows.Close()

	return scanSummaries(rows)
}

// ftsQuote 把用户输入当作一个短语字面量。
//
// FTS5 的 MATCH 有自己的查询语法(AND / OR / NEAR / 前缀星号等), 用户随手输入的
// 引号或星号会被当成语法而不是内容, 轻则搜不到, 重则直接语法错误。整体加引号
// 变成短语查询, 内部的引号按 FTS5 规则双写转义。
func ftsQuote(q string) string {
	return `"` + strings.ReplaceAll(q, `"`, `""`) + `"`
}

// escapeLike 转义 LIKE 的通配符, 使其按字面匹配。
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// EmailsByIDs 取这些 id 的列表行, 本地没有的跳过, 结果按收件时间倒序。
func (s *Store) EmailsByIDs(ctx context.Context, accountID string, ids []string) ([]EmailSummary, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(ids)+1)
	args = append(args, accountID)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.thread_id, e.subject, e.from_json, e.received_at,
		       e.preview, e.has_attachment, e.size,`+keywordFlags+threadCountColumn+`
		FROM emails e
		WHERE e.account_id = ? AND e.id IN (?`+strings.Repeat(",?", len(ids)-1)+`)
		ORDER BY e.received_at DESC`,
		args...)
	if err != nil {
		return nil, wrap(CodeQuery, "query emails by ids", err)
	}
	defer rows.Close()
	return scanSummaries(rows)
}

// FillMailboxIDs 给搜索结果填上所在文件夹。
func (s *Store) FillMailboxIDs(ctx context.Context, accountID string, list []EmailSummary) error {
	if len(list) == 0 {
		return nil
	}
	args := make([]any, 0, len(list)+1)
	args = append(args, accountID)
	index := make(map[string]int, len(list))
	for i, e := range list {
		args = append(args, e.ID)
		index[e.ID] = i
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT email_id, mailbox_id FROM email_mailboxes
		 WHERE account_id = ? AND email_id IN (?`+strings.Repeat(",?", len(list)-1)+`)`,
		args...)
	if err != nil {
		return wrap(CodeQuery, "query email mailboxes", err)
	}
	defer rows.Close()
	for rows.Next() {
		var emailID, mailboxID string
		if err := rows.Scan(&emailID, &mailboxID); err != nil {
			return wrap(CodeQuery, "scan email mailbox", err)
		}
		if i, ok := index[emailID]; ok {
			list[i].MailboxIDs = append(list[i].MailboxIDs, mailboxID)
		}
	}
	return wrap(CodeQuery, "iterate email mailboxes", rows.Err())
}
