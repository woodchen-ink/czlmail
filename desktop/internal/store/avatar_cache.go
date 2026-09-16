package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CachedAvatar 是一条头像缓存。Missing 为真表示上游确认没有该头像。
type CachedAvatar struct {
	ContentType string
	Data        []byte
	Missing     bool
	FetchedAt   time.Time
}

// Avatar 读缓存, 不存在时返回 nil 且 err 为 nil。
func (s *Store) Avatar(ctx context.Context, key string) (*CachedAvatar, error) {
	var a CachedAvatar
	var missing int
	var fetched int64
	err := s.db.QueryRowContext(ctx,
		`SELECT content_type, data, missing, fetched_at FROM avatar_cache WHERE key = ?`, key,
	).Scan(&a.ContentType, &a.Data, &missing, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, wrap(CodeQuery, "read avatar cache", err)
	}
	a.Missing = missing != 0
	a.FetchedAt = time.Unix(fetched, 0)
	return &a, nil
}

// PutAvatar 写入或覆盖一条头像缓存。
func (s *Store) PutAvatar(ctx context.Context, key string, a CachedAvatar) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO avatar_cache (key, content_type, data, missing, fetched_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET
			content_type = excluded.content_type,
			data         = excluded.data,
			missing      = excluded.missing,
			fetched_at   = excluded.fetched_at`,
		key, a.ContentType, a.Data, boolToInt(a.Missing), time.Now().Unix())
	return wrap(CodeQuery, "write avatar cache", err)
}
