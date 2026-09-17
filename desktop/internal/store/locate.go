package store

import (
	"context"
	"database/sql"
)

// EmailLocation 是某封邮件在列表里的位置。
type EmailLocation struct {
	MailboxID string `json:"mailboxId"`
	// Index 是邮件在 EmailsByMailbox 的顺序里的下标(置顶在前, 其余按收件时间倒序)。
	// 界面据此知道要加载到第几页才能看见它, 再滚动过去。
	Index int `json:"index"`
}

// LocateEmail 找出一封邮件该在哪个文件夹的列表里、排第几。
//
// 从通讯录或链接打开邮件时, 光设置选中 id 不够: 邮件可能在别的文件夹, 或在当前
// 文件夹里靠后、还没翻到那一页, 列表就会停在原处不动。prefer 非空且邮件确实在其中
// 时优先选它, 免得从当前文件夹里打开邮件反而被切走。
//
// 同一秒收到的邮件之间没有稳定次序, 下标可能差一两位; 界面只用它决定加载深度,
// 找到具体那一行仍按 id。
func (s *Store) LocateEmail(ctx context.Context, accountID, emailID, prefer string) (EmailLocation, error) {
	var receivedAt int64
	var pinned bool
	err := s.db.QueryRowContext(ctx, `
		SELECT e.received_at,
		       EXISTS (SELECT 1 FROM email_keywords k
		               WHERE k.account_id = e.account_id AND k.email_id = e.id AND k.keyword = '$pinned')
		FROM emails e
		WHERE e.account_id = ? AND e.id = ?`,
		accountID, emailID).Scan(&receivedAt, &pinned)
	if err == sql.ErrNoRows {
		return EmailLocation{}, ErrNotFound
	}
	if err != nil {
		return EmailLocation{}, wrap(CodeQuery, "locate email", err)
	}

	boxID, err := s.mailboxForEmail(ctx, accountID, emailID, prefer)
	if err != nil {
		return EmailLocation{}, err
	}
	if boxID == "" {
		return EmailLocation{}, ErrNotFound
	}

	index, err := s.rankInMailbox(ctx, accountID, boxID, receivedAt, pinned)
	if err != nil {
		return EmailLocation{}, err
	}
	return EmailLocation{MailboxID: boxID, Index: index}, nil
}

// mailboxForEmail 在邮件所属的文件夹里挑一个给界面显示。
// 优先 prefer, 其次收件箱, 再次任何不是垃圾邮件/回收站的文件夹。
func (s *Store) mailboxForEmail(ctx context.Context, accountID, emailID, prefer string) (string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT mailbox_id FROM email_mailboxes
		WHERE account_id = ? AND email_id = ?`,
		accountID, emailID)
	if err != nil {
		return "", wrap(CodeQuery, "query email mailboxes", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", wrap(CodeQuery, "scan email mailbox", err)
		}
		if id == prefer {
			return id, nil
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", wrap(CodeQuery, "query email mailboxes", err)
	}
	if len(ids) == 0 {
		return "", nil
	}

	boxes, err := s.Mailboxes(ctx, accountID)
	if err != nil {
		return "", err
	}
	kinds := make(map[string]string, len(boxes))
	for _, b := range boxes {
		kinds[b.ID] = b.Kind
	}
	fallback := ""
	for _, id := range ids {
		switch kinds[id] {
		case KindInbox:
			return id, nil
		case KindJunk, KindTrash:
		default:
			if fallback == "" {
				fallback = id
			}
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return ids[0], nil
}

// rankInMailbox 数出文件夹列表里排在这封邮件前面的邮件数。
func (s *Store) rankInMailbox(ctx context.Context, accountID, mailboxID string, receivedAt int64, pinned bool) (int, error) {
	count := func(query string, args ...any) (int, error) {
		var n int
		if err := s.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
			return 0, wrap(CodeQuery, "count emails before", err)
		}
		return n, nil
	}

	// 置顶的邮件只排在其它置顶邮件之后。
	if pinned {
		return count(`
			SELECT COUNT(*) FROM email_keywords p
			JOIN email_mailboxes m ON m.account_id = p.account_id AND m.email_id = p.email_id
			JOIN emails e ON e.account_id = p.account_id AND e.id = p.email_id
			WHERE p.account_id = ? AND p.keyword = '$pinned' AND m.mailbox_id = ? AND e.received_at > ?`,
			accountID, mailboxID, receivedAt)
	}

	// 未置顶的排在全部置顶邮件之后, 再按时间算。
	ahead, err := count(`
		SELECT COUNT(*) FROM email_keywords p
		JOIN email_mailboxes m ON m.account_id = p.account_id AND m.email_id = p.email_id
		WHERE p.account_id = ? AND p.keyword = '$pinned' AND m.mailbox_id = ?`,
		accountID, mailboxID)
	if err != nil {
		return 0, err
	}
	newer, err := count(`
		SELECT COUNT(*) FROM emails e
		JOIN email_mailboxes m ON m.account_id = e.account_id AND m.email_id = e.id
		WHERE e.account_id = ? AND m.mailbox_id = ? AND e.received_at > ?
		  AND NOT EXISTS (SELECT 1 FROM email_keywords p
		                  WHERE p.account_id = e.account_id AND p.email_id = e.id AND p.keyword = '$pinned')`,
		accountID, mailboxID, receivedAt)
	if err != nil {
		return 0, err
	}
	return ahead + newer, nil
}
