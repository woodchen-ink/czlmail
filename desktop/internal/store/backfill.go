package store

import (
	"context"
	"database/sql"

	"github.com/woodchen-ink/czlmail/desktop/internal/htmltext"
)

// SettingBodyTextBackfilled 标记"旧缓存的 body_text 已重写过"。
const SettingBodyTextBackfilled = "bodyTextBackfilled"

// backfillBatch 是单个事务里重写的邮件数。批太大时一次提交要重建同样多的 FTS 行,
// 会把写锁攥得太久, 挡住正在进行的同步。
const backfillBatch = 200

// BackfillBodyText 把旧缓存里存成 HTML 源码的 body_text 重写成纯文本。
//
// 邮件只有 text/html 部件时, JMAP 的 textBody 返回的就是那个 HTML 部件
// (见 syncer.joinBodyValues)。早先的版本照原样落库, 结果 body_text 里是 HTML 源码 ——
// FTS 索引的是这一列, 标签和内联样式一起进了索引; 喂给模型的正文也取这一列。
// 新同步的邮件在落库时就已转换, 这里只补历史数据, 整库跑一次后就不再跑。
//
// 只改 body_text: 正文不重新拉取, body_html 与 body_fetched_at 都不动。
func (s *Store) BackfillBodyText(ctx context.Context) (int, error) {
	if v, _ := s.StringSetting(ctx, SettingBodyTextBackfilled); v == "1" {
		return 0, nil
	}

	total := 0
	// 改过的行不再满足筛选条件, 会自动退出结果集; 转换后没有变化的行会一直留在里面,
	// 用 offset 跳过它们, 否则每一批都是同样那几行。
	skipped := 0
	for {
		changed, seen, err := s.backfillBodyTextBatch(ctx, skipped)
		if err != nil {
			return total, err
		}
		if seen == 0 {
			break
		}
		total += changed
		skipped += seen - changed
	}
	return total, s.SetStringSetting(ctx, SettingBodyTextBackfilled, "1")
}

// backfillBodyTextBatch 处理一批, 返回改写的行数与看过的行数。
func (s *Store) backfillBodyTextBatch(ctx context.Context, offset int) (changed, seen int, err error) {
	type row struct {
		accountID string
		id        string
		text      string
	}

	// body_text = body_html 正是"没有 text/plain 部件"的特征: 两列取自同一个部件。
	rows, err := s.db.QueryContext(ctx, `
		SELECT account_id, id, body_text FROM emails
		WHERE body_fetched_at IS NOT NULL
		  AND body_html IS NOT NULL AND body_html != ''
		  AND body_text = body_html
		LIMIT ? OFFSET ?`, backfillBatch, offset)
	if err != nil {
		return 0, 0, wrap(CodeQuery, "scan emails for body_text backfill", err)
	}

	var batch []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.accountID, &r.id, &r.text); err != nil {
			rows.Close()
			return 0, 0, wrap(CodeQuery, "scan email body", err)
		}
		batch = append(batch, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, wrap(CodeQuery, "scan emails for body_text backfill", err)
	}

	err = s.WithTx(ctx, func(tx *sql.Tx) error {
		for _, r := range batch {
			// 两列相同也可能是纯文本邮件(服务器把 text/plain 部件同时放进了 htmlBody),
			// 那种不能碰: 转换会顺手压掉它真实的空行与连续空格。
			if !looksLikeHTML(r.text) {
				continue
			}
			text := htmltext.Convert(r.text)
			// 转换后没有变化的不写回: 写了也一样会被下一批查到。
			if text == r.text {
				continue
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE emails SET body_text = ? WHERE account_id = ? AND id = ?`,
				text, r.accountID, r.id); err != nil {
				return wrap(CodeQuery, "rewrite body_text", err)
			}
			changed++
		}
		return nil
	})
	return changed, len(batch), err
}
