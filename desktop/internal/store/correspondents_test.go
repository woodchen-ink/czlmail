package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// 往来邮件与收件人补全都用 LIKE ... ESCAPE '\'; 转义符丢了 (写成 ”) 时 SQLite 直接报错,
// 通讯录点开联系人就是 "SQL logic error: ESCAPE expression must be a single character"。
func TestCorrespondentQueries(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	msgs := []Email{
		{ID: "m1", Subject: "hello", From: []Address{{Name: "Zhang_Wei", Email: "zhang_wei@example.com"}}, ReceivedAt: time.Unix(1700000002, 0)},
		{ID: "m2", Subject: "other", From: []Address{{Name: "Li", Email: "zhangxwei@example.com"}}, ReceivedAt: time.Unix(1700000001, 0)},
	}
	if err := s.WithTx(ctx, func(tx *sql.Tx) error { return UpsertEmails(ctx, tx, "c", msgs) }); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := s.RecentEmailsWith(ctx, "zhang_wei@example.com", 10)
	if err != nil {
		t.Fatalf("recent emails with: %v", err)
	}
	// 下划线按字面匹配, 不能把 zhangxwei 也算进来。
	if len(got) != 1 || got[0].ID != "m1" {
		t.Fatalf("recent emails with = %+v, want only m1", got)
	}

	if _, err := s.SearchCorrespondents(ctx, "zhang_", 10); err != nil {
		t.Fatalf("search correspondents: %v", err)
	}
}
