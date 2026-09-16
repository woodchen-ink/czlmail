package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// MCP 相关设置。
const (
	// SettingMCPEnabled 是否开启本地 MCP 端点。默认关闭: 开启后本机任何拿到令牌的程序都能读邮件。
	SettingMCPEnabled = "mcpEnabled"
	// SettingMCPAllowSend 是否允许经 MCP 发信。默认关闭, 见 mcp_server.go。
	SettingMCPAllowSend = "mcpAllowSend"
	// SettingMCPToken 是 MCP 端点的访问令牌。
	SettingMCPToken = "mcpToken"
	// SettingFileFavorites 是收藏的文件, JSON 数组, 元素为 "账号/节点id"。
	SettingFileFavorites = "fileFavorites"
	// SettingUpdateCheckedAt 是上次检查更新的时间(Unix 秒)。
	SettingUpdateCheckedAt = "updateCheckedAt"
)

// StringSetting 读字符串设置, 不存在时返回 ErrNotFound。
func (s *Store) StringSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, wrap(CodeQuery, "read setting", err)
}

// SetStringSetting 写字符串设置。
func (s *Store) SetStringSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO app_settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().Unix())
	return wrap(CodeQuery, "write setting", err)
}

// SetBoolSettingNow 在独立事务里写布尔设置。
func (s *Store) SetBoolSettingNow(ctx context.Context, key string, value bool) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error { return SetBoolSetting(ctx, tx, key, value) })
}
