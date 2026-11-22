package store

import "context"

// EmailPlacement 是一封邮件在移动类操作(删除、移动、归档、垃圾邮件)之前所在的位置,
// 界面「撤销」时据此把它放回去。
type EmailPlacement struct {
	ID         string   `json:"id"`
	MailboxIDs []string `json:"mailboxIds"`
	// Junk / NotJunk 是 $junk / $notjunk 关键字。标记垃圾邮件会改动它们, 撤销时一并还原。
	Junk    bool `json:"junk"`
	NotJunk bool `json:"notJunk"`
}

// EmailPlacements 读出邮件当前所在的文件夹与垃圾邮件标记。
// 本地缓存里没有的邮件不出现在结果里: 不知道原来在哪, 也就无从撤销。
func (s *Store) EmailPlacements(ctx context.Context, accountID string, emailIDs []string) ([]EmailPlacement, error) {
	out := make([]EmailPlacement, 0, len(emailIDs))
	for _, id := range emailIDs {
		p := EmailPlacement{ID: id}

		rows, err := s.db.QueryContext(ctx, `
			SELECT mailbox_id FROM email_mailboxes
			WHERE account_id = ? AND email_id = ?
			ORDER BY mailbox_id`,
			accountID, id)
		if err != nil {
			return nil, wrap(CodeQuery, "query email mailboxes", err)
		}
		for rows.Next() {
			var box string
			if err := rows.Scan(&box); err != nil {
				rows.Close()
				return nil, wrap(CodeQuery, "scan email mailbox", err)
			}
			p.MailboxIDs = append(p.MailboxIDs, box)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, wrap(CodeQuery, "query email mailboxes", err)
		}
		if len(p.MailboxIDs) == 0 {
			continue
		}

		err = s.db.QueryRowContext(ctx, `
			SELECT
			  EXISTS (SELECT 1 FROM email_keywords WHERE account_id = ? AND email_id = ? AND keyword = '$junk'),
			  EXISTS (SELECT 1 FROM email_keywords WHERE account_id = ? AND email_id = ? AND keyword = '$notjunk')`,
			accountID, id, accountID, id).Scan(&p.Junk, &p.NotJunk)
		if err != nil {
			return nil, wrap(CodeQuery, "query junk keywords", err)
		}
		out = append(out, p)
	}
	return out, nil
}
