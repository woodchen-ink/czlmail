package store

import (
	"context"
	"strings"
)

// MailboxCoverage 返回某邮箱本地已缓存的邮件数与服务端公布的总数。
//
// 首次引导只取整个账号最近的一个窗口, 归档这类以旧邮件为主的邮箱本地几乎是空的;
// 调用方据此判断翻页时要不要向服务端补取。
func (s *Store) MailboxCoverage(ctx context.Context, accountID, mailboxID string) (local, total int64, err error) {
	err = s.db.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM email_mailboxes WHERE account_id = ? AND mailbox_id = ?),
			COALESCE((SELECT total_emails FROM mailboxes WHERE account_id = ? AND id = ?), 0)`,
		accountID, mailboxID, accountID, mailboxID,
	).Scan(&local, &total)
	if err != nil {
		return 0, 0, wrap(CodeQuery, "mailbox coverage", err)
	}
	return local, total, nil
}

// MissingEmailIDs 返回 ids 中本地尚未缓存的部分, 保持原顺序。
func (s *Store) MissingEmailIDs(ctx context.Context, accountID string, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	args := make([]any, 0, len(ids)+1)
	args = append(args, accountID)
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM emails WHERE account_id = ? AND id IN (?`+strings.Repeat(",?", len(ids)-1)+`)`,
		args...)
	if err != nil {
		return nil, wrap(CodeQuery, "query cached email ids", err)
	}
	defer rows.Close()

	have := make(map[string]bool, len(ids))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrap(CodeQuery, "scan cached email id", err)
		}
		have[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, wrap(CodeQuery, "iterate cached email ids", err)
	}

	var missing []string
	for _, id := range ids {
		if !have[id] {
			missing = append(missing, id)
		}
	}
	return missing, nil
}

// EmailsWithoutBody 返回某账号最近收到、尚未缓存正文的邮件 id, 供后台预取。
// 只看收件箱类文件夹之外也无妨: 按收件时间取最新的, 用户最可能打开的就是这些。
func (s *Store) EmailsWithoutBody(ctx context.Context, accountID string, since int64, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM emails
		WHERE account_id = ? AND body_fetched_at IS NULL AND received_at >= ?
		ORDER BY received_at DESC LIMIT ?`, accountID, since, limit)
	if err != nil {
		return nil, wrap(CodeQuery, "emails without body", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrap(CodeQuery, "scan email id", err)
		}
		out = append(out, id)
	}
	return out, wrap(CodeQuery, "iterate emails without body", rows.Err())
}

// BodyCached 返回 ids 中已缓存正文的集合。
func (s *Store) BodyCached(ctx context.Context, accountID string, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	args := []any{accountID}
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM emails WHERE account_id = ? AND body_fetched_at IS NOT NULL AND id IN (?`+strings.Repeat(",?", len(ids)-1)+`)`,
		args...)
	if err != nil {
		return nil, wrap(CodeQuery, "body cached", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrap(CodeQuery, "scan id", err)
		}
		out[id] = true
	}
	return out, wrap(CodeQuery, "iterate body cached", rows.Err())
}

// LocalEmailIDs 列出某账号本地缓存的全部邮件 id。
func (s *Store) LocalEmailIDs(ctx context.Context, accountID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM emails WHERE account_id = ?`, accountID)
	if err != nil {
		return nil, wrap(CodeQuery, "list local email ids", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, wrap(CodeQuery, "scan local email id", err)
		}
		out = append(out, id)
	}
	return out, wrap(CodeQuery, "iterate local email ids", rows.Err())
}
