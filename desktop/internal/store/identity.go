package store

import (
	"context"
	"database/sql"
)

// SettingCacheOwner 记录缓存属于哪个登录身份 (会话地址 + 用户名)。
// 退出登录时缓存保留 (重新登录不必重新引导), 换成别人登录时据此清空, 否则上一个人的邮件会混进新账号的界面,
// 模板、AI 设置还会经同步写进新账号的网盘。
const SettingCacheOwner = "cacheOwner"

// identityTables 是随登录身份走的数据。头像缓存与界面偏好 (已读方式、提醒开关等) 不在其中。
var identityTables = []string{
	"email_keywords", "email_mailboxes", "threads", "translations", "emails",
	"mailboxes", "calendar_events", "calendars", "contacts", "address_books",
	"file_nodes", "email_templates", "trusted_senders", "sync_state", "accounts",
}

// ResetIdentityData 清空上一个登录身份的缓存与随身份同步的设置 (settingKeys 以及 reconciledAt:* 等按账号记的键)。
func (s *Store) ResetIdentityData(ctx context.Context, settingKeys []string) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		for _, t := range identityTables {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+t); err != nil {
				return wrap(CodeQuery, "reset "+t, err)
			}
		}
		// 删 emails 时触发器已逐行清索引; 重建一次保证外部内容表与源表一致。
		if _, err := tx.ExecContext(ctx, `INSERT INTO emails_fts(emails_fts) VALUES('rebuild')`); err != nil {
			return wrap(CodeQuery, "rebuild fts", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM app_settings WHERE key LIKE 'reconciledAt:%'`); err != nil {
			return wrap(CodeQuery, "reset reconcile marks", err)
		}
		for _, k := range settingKeys {
			if _, err := tx.ExecContext(ctx, `DELETE FROM app_settings WHERE key = ?`, k); err != nil {
				return wrap(CodeQuery, "reset setting", err)
			}
		}
		return nil
	})
}

// accountTables 是按 account_id 存的数据, 失去某个账号的访问权时整账号删除。
// 与 identityTables 的差别: 模板不分账号, accounts 本身按 id 删。
var accountTables = []string{
	"email_keywords", "email_mailboxes", "threads", "translations", "emails",
	"mailboxes", "calendar_events", "calendars", "contacts", "address_books",
	"file_nodes", "trusted_senders", "sync_state",
}

// RemoveAccount 删除一个账号的全部缓存 (会话里已没有它, 如共享被撤销)。
// emails 的删除触发器会逐行清掉全文索引。
func (s *Store) RemoveAccount(ctx context.Context, accountID string) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		for _, t := range accountTables {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+t+` WHERE account_id = ?`, accountID); err != nil {
				return wrap(CodeQuery, "remove account data from "+t, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM accounts WHERE id = ?`, accountID); err != nil {
			return wrap(CodeQuery, "remove account", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM app_settings WHERE key = ?`, "reconciledAt:"+accountID); err != nil {
			return wrap(CodeQuery, "remove reconcile mark", err)
		}
		return nil
	})
}
