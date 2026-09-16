package store

import (
	"context"
	"database/sql"
)

// SyncedTemplate 是参与跨设备同步的模板, 含墓碑。
type SyncedTemplate struct {
	Template
	// DeletedAt 非零表示已删除(Unix 秒)。
	DeletedAt int64
}

// TemplatesForSync 返回全部模板, 包括墓碑。
func (s *Store) TemplatesForSync(ctx context.Context) ([]SyncedTemplate, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, category, subject, body, is_html,
		       default_to, default_cc, default_bcc, identity_id,
		       is_favorite, created_at, updated_at, remote_rev, COALESCE(deleted_at, 0)
		FROM email_templates`)
	if err != nil {
		return nil, wrap(CodeQuery, "list templates for sync", err)
	}
	defer rows.Close()

	var out []SyncedTemplate
	for rows.Next() {
		var st SyncedTemplate
		var deleted int64
		t, err := scanTemplate(scanWithTail{rows, &deleted})
		if err != nil {
			return nil, err
		}
		st.Template, st.DeletedAt = t, deleted
		out = append(out, st)
	}
	return out, wrap(CodeQuery, "iterate templates for sync", rows.Err())
}

// scanWithTail 让 scanTemplate 复用于多了一列的查询。
type scanWithTail struct {
	rows *sql.Rows
	tail *int64
}

func (s scanWithTail) Scan(dest ...any) error {
	return s.rows.Scan(append(dest, s.tail)...)
}

// PutSyncedTemplate 写入从远端合并来的模板, 保留其原有的时间戳与删除状态。
// 与 SaveTemplate 不同: 后者代表本地编辑, 会把 updated_at 改成当前时间。
func PutSyncedTemplate(ctx context.Context, tx *sql.Tx, t SyncedTemplate) error {
	to, cc, bcc, err := encodeRecipients(t.Template)
	if err != nil {
		return err
	}
	var deleted any
	if t.DeletedAt > 0 {
		deleted = t.DeletedAt
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO email_templates (
			id, name, category, subject, body, is_html,
			default_to, default_cc, default_bcc, identity_id,
			is_favorite, created_at, updated_at, deleted_at, remote_rev
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			name = excluded.name, category = excluded.category, subject = excluded.subject,
			body = excluded.body, is_html = excluded.is_html, default_to = excluded.default_to,
			default_cc = excluded.default_cc, default_bcc = excluded.default_bcc,
			identity_id = excluded.identity_id, is_favorite = excluded.is_favorite,
			created_at = excluded.created_at, updated_at = excluded.updated_at,
			deleted_at = excluded.deleted_at, remote_rev = excluded.remote_rev`,
		t.ID, t.Name, t.Category, t.Subject, t.Body, boolToInt(t.IsHTML),
		to, cc, bcc, t.IdentityID, boolToInt(t.IsFavorite),
		t.CreatedAt, t.UpdatedAt, deleted, t.RemoteRev)
	return wrap(CodeQuery, "put synced template", err)
}
