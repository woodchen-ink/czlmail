package store

import (
	"context"
	"strings"
)

// EmailPlacement 是一封邮件在移动类操作(删除、移动、归档、垃圾邮件)之前所在的位置,
// 界面「撤销」时据此把它放回去。
type EmailPlacement struct {
	ID         string   `json:"id"`
	MailboxIDs []string `json:"mailboxIds"`
	// Junk / NotJunk 是 $junk / $notjunk 关键字。标记垃圾邮件会改动它们, 撤销时一并还原。
	Junk    bool `json:"junk"`
	NotJunk bool `json:"notJunk"`
}

// EmailPlacements 读出邮件当前所在的文件夹与垃圾邮件标记, 按 emailIDs 的顺序返回。
// 本地缓存里没有的邮件不出现在结果里: 不知道原来在哪, 也就无从撤销。
func (s *Store) EmailPlacements(ctx context.Context, accountID string, emailIDs []string) ([]EmailPlacement, error) {
	if len(emailIDs) == 0 {
		return []EmailPlacement{}, nil
	}
	in := `(?` + strings.Repeat(",?", len(emailIDs)-1) + `)`
	args := make([]any, 0, len(emailIDs)+1)
	args = append(args, accountID)
	for _, id := range emailIDs {
		args = append(args, id)
	}

	byID := make(map[string]*EmailPlacement, len(emailIDs))
	rows, err := s.db.QueryContext(ctx, `
		SELECT email_id, mailbox_id FROM email_mailboxes
		WHERE account_id = ? AND email_id IN `+in+`
		ORDER BY email_id, mailbox_id`, args...)
	if err != nil {
		return nil, wrap(CodeQuery, "query email mailboxes", err)
	}
	for rows.Next() {
		var id, box string
		if err := rows.Scan(&id, &box); err != nil {
			rows.Close()
			return nil, wrap(CodeQuery, "scan email mailbox", err)
		}
		p := byID[id]
		if p == nil {
			p = &EmailPlacement{ID: id}
			byID[id] = p
		}
		p.MailboxIDs = append(p.MailboxIDs, box)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, wrap(CodeQuery, "query email mailboxes", err)
	}

	rows, err = s.db.QueryContext(ctx, `
		SELECT email_id, keyword FROM email_keywords
		WHERE account_id = ? AND keyword IN ('$junk', '$notjunk') AND email_id IN `+in, args...)
	if err != nil {
		return nil, wrap(CodeQuery, "query junk keywords", err)
	}
	for rows.Next() {
		var id, keyword string
		if err := rows.Scan(&id, &keyword); err != nil {
			rows.Close()
			return nil, wrap(CodeQuery, "scan junk keyword", err)
		}
		if p := byID[id]; p != nil {
			p.Junk = p.Junk || keyword == "$junk"
			p.NotJunk = p.NotJunk || keyword == "$notjunk"
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, wrap(CodeQuery, "query junk keywords", err)
	}

	out := make([]EmailPlacement, 0, len(byID))
	for _, id := range emailIDs {
		if p := byID[id]; p != nil {
			out = append(out, *p)
			delete(byID, id) // 同一个 id 传了两次时只出现一次
		}
	}
	return out, nil
}
