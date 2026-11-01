package store

import (
	"context"
	"strings"
)

// MailboxRef 指定某个账号的一个文件夹。
type MailboxRef struct {
	AccountID string
	MailboxID string
}

// EmailsInMailboxes 跨账号列出若干文件夹里的邮件, 按收件时间倒序, 供统一视图使用。
// 同一封邮件在多个文件夹里只出现一次; unreadOnly 只列未读。
//
// 不像单个文件夹那样把置顶的排在前面: 统一视图是各账号的邮件按时间合流,
// 某个共享邮箱的置顶邮件压在所有账号前面反而打乱阅读顺序。
func (s *Store) EmailsInMailboxes(
	ctx context.Context, boxes []MailboxRef, unreadOnly bool, limit, offset int,
) ([]EmailSummary, error) {
	if len(boxes) == 0 {
		return nil, nil
	}
	conds := make([]string, len(boxes))
	args := make([]any, 0, len(boxes)*2+2)
	for i, b := range boxes {
		conds[i] = "(m.account_id = ? AND m.mailbox_id = ?)"
		args = append(args, b.AccountID, b.MailboxID)
	}
	unread := ""
	if unreadOnly {
		unread = `AND NOT EXISTS (SELECT 1 FROM email_keywords k
		                  WHERE k.account_id = e.account_id AND k.email_id = e.id AND k.keyword = '$seen')`
	}
	args = append(args, limit, offset)

	rows, err := s.db.QueryContext(ctx, `
		SELECT `+summaryColumns+threadCountColumn+`
		FROM emails e
		WHERE EXISTS (SELECT 1 FROM email_mailboxes m
		              WHERE m.account_id = e.account_id AND m.email_id = e.id
		                AND (`+strings.Join(conds, " OR ")+`))
		  `+unread+`
		ORDER BY e.received_at DESC, e.account_id, e.id
		LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, wrap(CodeQuery, "query unified emails", err)
	}
	defer rows.Close()
	return scanSummaries(rows)
}
