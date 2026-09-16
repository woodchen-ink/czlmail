package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// SettingTrustAddressBook 控制是否自动信任通讯录中的发件人。
//
// 默认开启: 在你自己通讯录里的人, 你本来就知道他是谁, 对这些人再屏蔽图片
// 只会让界面上到处是"已屏蔽"的横幅而没有实际收益。陌生发件人仍然一律屏蔽。
const SettingTrustAddressBook = "trustAddressBookSenders"

// SettingSenderAvatars 控制是否显示发件人头像(Gravatar 与域名图标)。
//
// 默认开启, 但提供开关: 头像要把发件地址的哈希与发件域名发给头像服务,
// 在意这类元数据外泄的用户应当能关掉它, 此时一律显示首字母。
const SettingSenderAvatars = "senderAvatars"

// NormalizeAddress 统一邮件地址的比较形态。
//
// 只做小写化, 不做本地部分的任何规整(比如去掉点或加号后缀) ——
// 那些规则是各家服务商自己的, 对 a.b@example.com 和 ab@example.com
// 做等价处理会在不支持该规则的服务器上把信任错误地扩散到另一个人身上。
func NormalizeAddress(addr string) string {
	return strings.ToLower(strings.TrimSpace(addr))
}

// IsSenderTrusted 判断某个发件人的远程内容是否可以放行。
//
// 两条来源: 显式信任名单, 以及(开关开启时)通讯录。两者任一命中即放行。
func (s *Store) IsSenderTrusted(ctx context.Context, accountID, address string) (bool, error) {
	addr := NormalizeAddress(address)
	if addr == "" {
		return false, nil
	}

	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM trusted_senders WHERE account_id = ? AND address = ?`,
		accountID, addr,
	).Scan(&n)
	if err != nil {
		return false, wrap(CodeQuery, "check trusted sender", err)
	}
	if n > 0 {
		return true, nil
	}
	// 服务端信任通讯录与本地名单同等对待, 不受"信任通讯录联系人"开关影响:
	// 它就是信任名单本身, 只是存在服务端。
	if ok, err := s.inTrustedBook(ctx, addr); err != nil || ok {
		return ok, err
	}

	enabled, err := s.BoolSetting(ctx, SettingTrustAddressBook, true)
	if err != nil {
		return false, err
	}
	if !enabled {
		return false, nil
	}
	return s.isInAddressBook(ctx, accountID, addr)
}

// isInAddressBook 检查地址是否出现在已同步的联系人里。
//
// emails_json 是 JSON 数组, 用 json_each 展开后比对而不是对整串做 LIKE:
// LIKE '%addr%' 会让 bob@x.com 命中 rob@x.com 之类的子串。
func (s *Store) isInAddressBook(ctx context.Context, accountID, addr string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM contacts c, json_each(c.emails_json) e
		WHERE c.account_id = ?
		  AND LOWER(COALESCE(json_extract(e.value, '$.email'), e.value)) = ?`,
		accountID, addr,
	).Scan(&n)
	if err != nil {
		return false, wrap(CodeQuery, "check address book", err)
	}
	return n > 0, nil
}

// TrustSender 把发件人加入信任名单。
func TrustSender(ctx context.Context, tx *sql.Tx, accountID, address string) error {
	addr := NormalizeAddress(address)
	if addr == "" {
		return nil
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO trusted_senders (account_id, address, created_at) VALUES (?, ?, ?)
		 ON CONFLICT (account_id, address) DO NOTHING`,
		accountID, addr, time.Now().Unix())
	return wrap(CodeQuery, "trust sender", err)
}

// UntrustSender 把发件人移出信任名单。
func UntrustSender(ctx context.Context, tx *sql.Tx, accountID, address string) error {
	_, err := tx.ExecContext(ctx,
		`DELETE FROM trusted_senders WHERE account_id = ? AND address = ?`,
		accountID, NormalizeAddress(address))
	return wrap(CodeQuery, "untrust sender", err)
}

// TrustedSenders 列出信任名单, 供设置页管理。
func (s *Store) TrustedSenders(ctx context.Context, accountID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT address FROM trusted_senders WHERE account_id = ? ORDER BY address`,
		accountID)
	if err != nil {
		return nil, wrap(CodeQuery, "list trusted senders", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var addr string
		if err := rows.Scan(&addr); err != nil {
			return nil, wrap(CodeQuery, "scan trusted sender", err)
		}
		out = append(out, addr)
	}
	return out, wrap(CodeQuery, "iterate trusted senders", rows.Err())
}

// BoolSetting 读布尔设置, 未设置时返回给定默认值。
func (s *Store) BoolSetting(ctx context.Context, key string, fallback bool) (bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return fallback, nil
	}
	if err != nil {
		return fallback, wrap(CodeQuery, "read setting", err)
	}
	return v == "true", nil
}

// SetBoolSetting 写布尔设置。
func SetBoolSetting(ctx context.Context, tx *sql.Tx, key string, value bool) error {
	v := "false"
	if value {
		v = "true"
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO app_settings (key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, v, time.Now().Unix())
	return wrap(CodeQuery, "write setting", err)
}
