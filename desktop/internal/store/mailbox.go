package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// Mailbox 是邮箱(文件夹)行。Role 为 JMAP 语义角色, 可能为空(用户自建文件夹)。
//
// JSON tag 是给 Wails 的绑定生成器看的: 前端拿到的字段名由此确定,
// 缺了 tag 会退化成 Go 的导出名, 与其他类型的 camelCase 不一致。
type Mailbox struct {
	ID       string `json:"id"`
	ParentID string `json:"parentId"`
	Name     string `json:"name"`
	Role     string `json:"role"`
	// Kind 是界面据以分类的类别: 服务器给了 role 就用 role, 否则按名称识别。
	// 不落库, 读取时计算 —— 识别规则会演进, 存下来的旧结果会过时。
	Kind          string          `json:"kind"`
	SortOrder     int64           `json:"sortOrder"`
	TotalEmails   int64           `json:"totalEmails"`
	UnreadEmails  int64           `json:"unreadEmails"`
	TotalThreads  int64           `json:"totalThreads"`
	UnreadThreads int64           `json:"unreadThreads"`
	MyRights      map[string]bool `json:"myRights"`
	IsSubscribed  bool            `json:"isSubscribed"`
}

// UpsertMailboxes 批量写入邮箱。
func UpsertMailboxes(ctx context.Context, tx *sql.Tx, accountID string, boxes []Mailbox) error {
	if len(boxes) == 0 {
		return nil
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO mailboxes (
			account_id, id, parent_id, name, role, sort_order,
			total_emails, unread_emails, total_threads, unread_threads,
			my_rights, is_subscribed
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (account_id, id) DO UPDATE SET
			parent_id      = excluded.parent_id,
			name           = excluded.name,
			role           = excluded.role,
			sort_order     = excluded.sort_order,
			total_emails   = excluded.total_emails,
			unread_emails  = excluded.unread_emails,
			total_threads  = excluded.total_threads,
			unread_threads = excluded.unread_threads,
			my_rights      = excluded.my_rights,
			is_subscribed  = excluded.is_subscribed`)
	if err != nil {
		return wrap(CodeQuery, "prepare mailbox upsert", err)
	}
	defer stmt.Close()

	for i := range boxes {
		b := &boxes[i]

		rights, err := json.Marshal(b.MyRights)
		if err != nil {
			return wrap(CodeEncode, "encode mailbox rights", err)
		}

		// 顶层邮箱的 parentId 在 JMAP 里是 null, 存 NULL 而非空串,
		// 否则 parent_id = '' 会被当成一个真实存在的父节点参与建树。
		var parent any
		if b.ParentID != "" {
			parent = b.ParentID
		}

		if _, err := stmt.ExecContext(ctx,
			accountID, b.ID, parent, b.Name, b.Role, b.SortOrder,
			b.TotalEmails, b.UnreadEmails, b.TotalThreads, b.UnreadThreads,
			string(rights), boolToInt(b.IsSubscribed),
		); err != nil {
			return wrap(CodeQuery, "upsert mailbox", err)
		}
	}
	return nil
}

// DeleteMailboxes 删除邮箱本身。
//
// 不级联删除 email_mailboxes 中的关联行: 服务端删除一个邮箱时, 其中的邮件要么
// 被移入其他邮箱(会以 Email 的更新推送过来), 要么被销毁(会以 Email/changes 的
// destroyed 推送过来)。两种情况都由邮件侧的同步负责, 在此提前清理反而会让
// 尚未收到邮件侧变更的窗口期内出现无归属的孤儿邮件。
func DeleteMailboxes(ctx context.Context, tx *sql.Tx, accountID string, ids []string) error {
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM mailboxes WHERE account_id = ? AND id = ?`,
			accountID, id,
		); err != nil {
			return wrap(CodeQuery, "delete mailbox", err)
		}
	}
	return nil
}

// Mailboxes 读出某账号的全部邮箱, 按 sort_order 再按名称排序。
// 建树由上层完成, 本层只负责按稳定顺序吐出平表。
func (s *Store) Mailboxes(ctx context.Context, accountID string) ([]Mailbox, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(parent_id, ''), name, COALESCE(role, ''), sort_order,
		       total_emails, unread_emails, total_threads, unread_threads,
		       my_rights, is_subscribed
		FROM mailboxes
		WHERE account_id = ?
		ORDER BY sort_order, name`, accountID)
	if err != nil {
		return nil, wrap(CodeQuery, "query mailboxes", err)
	}
	defer rows.Close()

	var out []Mailbox
	for rows.Next() {
		var b Mailbox
		var rights string
		var subscribed int

		if err := rows.Scan(
			&b.ID, &b.ParentID, &b.Name, &b.Role, &b.SortOrder,
			&b.TotalEmails, &b.UnreadEmails, &b.TotalThreads, &b.UnreadThreads,
			&rights, &subscribed,
		); err != nil {
			return nil, wrap(CodeQuery, "scan mailbox", err)
		}

		if err := json.Unmarshal([]byte(rights), &b.MyRights); err != nil {
			return nil, wrap(CodeDecode, "decode mailbox rights", err)
		}
		b.IsSubscribed = subscribed != 0
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, wrap(CodeQuery, "iterate mailboxes", err)
	}

	assignKinds(out)
	return out, nil
}

// assignKinds 为每个邮箱确定类别。
//
// 按名称推断出的类别只在该类别尚无服务器声明的邮箱时生效: 服务器已经把
// "Deleted Items" 标为 trash 时, 另一个叫 "Trash" 的自建文件夹不应再被当成
// 回收站, 否则删除操作可能把邮件移进错误的那个。
func assignKinds(boxes []Mailbox) {
	declared := map[string]bool{}
	for _, b := range boxes {
		if b.Role != "" {
			declared[strings.ToLower(b.Role)] = true
		}
	}

	claimed := map[string]bool{}
	for i := range boxes {
		b := &boxes[i]
		if b.Role != "" {
			b.Kind = strings.ToLower(b.Role)
			continue
		}
		kind := InferMailboxKind("", b.Name)
		// 同一类别只认第一个按名称推断出的邮箱, 理由同上。
		if kind != KindCustom && !declared[kind] && !claimed[kind] {
			b.Kind = kind
			claimed[kind] = true
		}
	}
}
