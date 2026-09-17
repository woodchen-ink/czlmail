package store

import (
	"context"
	"database/sql"
	"time"
)

// Translation 读取缓存的译文, 没有时返回空串。content 由界面决定格式(按文字片段的 JSON 数组)。
func (s *Store) Translation(ctx context.Context, accountID, emailID, language string) (string, error) {
	var content string
	err := s.db.QueryRowContext(ctx,
		`SELECT content FROM translations WHERE account_id = ? AND email_id = ? AND language = ?`,
		accountID, emailID, language).Scan(&content)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return content, wrap(CodeQuery, "read translation", err)
}

// SaveTranslation 写入或覆盖译文缓存。
func (s *Store) SaveTranslation(ctx context.Context, accountID, emailID, language, content string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO translations (account_id, email_id, language, content, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (account_id, email_id, language) DO UPDATE SET content = excluded.content, created_at = excluded.created_at`,
		accountID, emailID, language, content, time.Now().Unix())
	return wrap(CodeQuery, "save translation", err)
}
