package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Template 是一封可复用的邮件草稿。
//
// Body 里可以出现 {{name}} 形式的占位符, 由 ApplyPlaceholders 在套用时替换。
type Template struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Category   string   `json:"category"`
	Subject    string   `json:"subject"`
	Body       string   `json:"body"`
	IsHTML     bool     `json:"isHtml"`
	DefaultTo  []string `json:"defaultTo"`
	DefaultCC  []string `json:"defaultCc"`
	DefaultBCC []string `json:"defaultBcc"`
	IdentityID string   `json:"identityId"`
	IsFavorite bool     `json:"isFavorite"`
	CreatedAt  int64    `json:"createdAt"`
	UpdatedAt  int64    `json:"updatedAt"`
	RemoteRev  string   `json:"-"`
}

// Templates 列出全部未删除的模板。
func (s *Store) Templates(ctx context.Context) ([]Template, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, category, subject, body, is_html,
		       default_to, default_cc, default_bcc, identity_id,
		       is_favorite, created_at, updated_at, remote_rev
		FROM email_templates
		WHERE deleted_at IS NULL
		ORDER BY is_favorite DESC, category, name`)
	if err != nil {
		return nil, wrap(CodeQuery, "list templates", err)
	}
	defer rows.Close()

	var out []Template
	for rows.Next() {
		t, err := scanTemplate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, wrap(CodeQuery, "iterate templates", rows.Err())
}

// Template 读单个模板。
func (s *Store) Template(ctx context.Context, id string) (*Template, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, category, subject, body, is_html,
		       default_to, default_cc, default_bcc, identity_id,
		       is_favorite, created_at, updated_at, remote_rev
		FROM email_templates
		WHERE id = ? AND deleted_at IS NULL`, id)

	t, err := scanTemplate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// SaveTemplate 新建或更新模板。
func SaveTemplate(ctx context.Context, tx *sql.Tx, t Template) error {
	now := time.Now().Unix()
	if t.CreatedAt == 0 {
		t.CreatedAt = now
	}
	t.UpdatedAt = now

	to, cc, bcc, err := encodeRecipients(t)
	if err != nil {
		return err
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO email_templates (
			id, name, category, subject, body, is_html,
			default_to, default_cc, default_bcc, identity_id,
			is_favorite, created_at, updated_at, deleted_at, remote_rev
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?)
		ON CONFLICT (id) DO UPDATE SET
			name        = excluded.name,
			category    = excluded.category,
			subject     = excluded.subject,
			body        = excluded.body,
			is_html     = excluded.is_html,
			default_to  = excluded.default_to,
			default_cc  = excluded.default_cc,
			default_bcc = excluded.default_bcc,
			identity_id = excluded.identity_id,
			is_favorite = excluded.is_favorite,
			updated_at  = excluded.updated_at,
			-- 重新保存一个已删除的模板等于恢复它。
			deleted_at  = NULL`,
		t.ID, t.Name, t.Category, t.Subject, t.Body, boolToInt(t.IsHTML),
		to, cc, bcc, t.IdentityID, boolToInt(t.IsFavorite),
		t.CreatedAt, t.UpdatedAt, t.RemoteRev)
	return wrap(CodeQuery, "save template", err)
}

// DeleteTemplate 软删除模板, 留下墓碑供同步传播。
//
// 不做物理删除: 同步时若只是让行消失, 另一台设备会把它当成"本地缺失"
// 而重新推回来, 用户会看到删掉的模板自己长回来。
func DeleteTemplate(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE email_templates
		SET deleted_at = ?, updated_at = ?, body = '', subject = ''
		WHERE id = ?`,
		time.Now().Unix(), time.Now().Unix(), id)
	return wrap(CodeQuery, "delete template", err)
}

// PurgeDeletedTemplates 清掉早于 before 的墓碑。
// 同步周期内的墓碑必须保留, 过了足够长时间再清理。
func PurgeDeletedTemplates(ctx context.Context, tx *sql.Tx, before time.Time) error {
	_, err := tx.ExecContext(ctx,
		`DELETE FROM email_templates WHERE deleted_at IS NOT NULL AND deleted_at < ?`,
		before.Unix())
	return wrap(CodeQuery, "purge deleted templates", err)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTemplate(row rowScanner) (Template, error) {
	var t Template
	var to, cc, bcc string
	var isHTML, isFavorite int

	if err := row.Scan(
		&t.ID, &t.Name, &t.Category, &t.Subject, &t.Body, &isHTML,
		&to, &cc, &bcc, &t.IdentityID,
		&isFavorite, &t.CreatedAt, &t.UpdatedAt, &t.RemoteRev,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return t, err
		}
		return t, wrap(CodeQuery, "scan template", err)
	}

	t.IsHTML = isHTML != 0
	t.IsFavorite = isFavorite != 0

	for _, pair := range []struct {
		raw string
		dst *[]string
	}{{to, &t.DefaultTo}, {cc, &t.DefaultCC}, {bcc, &t.DefaultBCC}} {
		if err := json.Unmarshal([]byte(pair.raw), pair.dst); err != nil {
			return t, wrap(CodeDecode, "decode template recipients", err)
		}
	}
	return t, nil
}

func encodeRecipients(t Template) (string, string, string, error) {
	enc := func(list []string) (string, error) {
		if list == nil {
			list = []string{}
		}
		b, err := json.Marshal(list)
		return string(b), err
	}

	to, err := enc(t.DefaultTo)
	if err != nil {
		return "", "", "", wrap(CodeEncode, "encode template recipients", err)
	}
	cc, err := enc(t.DefaultCC)
	if err != nil {
		return "", "", "", wrap(CodeEncode, "encode template recipients", err)
	}
	bcc, err := enc(t.DefaultBCC)
	if err != nil {
		return "", "", "", wrap(CodeEncode, "encode template recipients", err)
	}
	return to, cc, bcc, nil
}
