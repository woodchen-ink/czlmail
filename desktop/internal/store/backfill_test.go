package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// 旧缓存把只有 text/html 部件的邮件的 HTML 源码存进了 body_text。
// 回填要把它转成纯文本, 并且不碰另外两种邮件。
func TestBackfillBodyText(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	const htmlBody = `<div style="color:#333">订单已发出</div><p>单号 SF123</p>`
	bodies := []struct{ id, text, html string }{
		{"html-only", htmlBody, htmlBody}, // 没有 text/plain 部件: 两列取自同一个部件
		{"plain-only", "第一行\n第二行", ""},    // 纯文本邮件
		{"both", "订单已发出", htmlBody},       // 两种部件都有
	}

	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		msgs := make([]Email, 0, len(bodies))
		for _, b := range bodies {
			msgs = append(msgs, Email{ID: b.id, Subject: "订单", ReceivedAt: time.Unix(1700000000, 0)})
		}
		if err := UpsertEmails(ctx, tx, "c", msgs); err != nil {
			return err
		}
		for _, b := range bodies {
			if err := SetEmailBody(ctx, tx, "c", b.id, b.text, b.html, "{}"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	n, err := s.BackfillBodyText(ctx)
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if n != 1 {
		t.Errorf("改写了 %d 封, 想要 1 封", n)
	}

	want := map[string]string{
		"html-only":  "订单已发出\n单号 SF123",
		"plain-only": "第一行\n第二行",
		"both":       "订单已发出",
	}
	for id, w := range want {
		var got string
		if err := s.DB().QueryRowContext(ctx,
			`SELECT body_text FROM emails WHERE account_id = 'c' AND id = ?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != w {
			t.Errorf("%s: body_text = %q, 想要 %q", id, got, w)
		}
	}

	// FTS 是 external content 表, 改完正文必须靠触发器重建索引行。
	got, err := s.SearchEmails(ctx, "c", "单号 SF123", 20)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) != 1 || got[0].ID != "html-only" {
		t.Errorf("搜正文应命中 html-only, 实际 %d 条", len(got))
	}

	// 跑过一次之后不再重复扫描。
	if n, err := s.BackfillBodyText(ctx); err != nil || n != 0 {
		t.Errorf("第二次 = (%d, %v), 想要 (0, nil)", n, err)
	}
}
