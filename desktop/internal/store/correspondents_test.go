package store

import (
	"context"
	"database/sql"
	"strings"
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

// 往来邮件按方向筛选: 对方发来的只看发件人, 发给对方的看收件人/抄送/密送; 联系人的多个邮箱一起算。
func TestEmailsWithDirection(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	me := Address{Email: "me@example.com"}
	msgs := []Email{
		{ID: "in1", From: []Address{{Email: "Bob@Example.com"}}, To: []Address{me}, ReceivedAt: time.Unix(1700000005, 0)},
		{ID: "out1", From: []Address{me}, To: []Address{{Email: "bob@example.com"}}, ReceivedAt: time.Unix(1700000004, 0)},
		{ID: "out2", From: []Address{me}, BCC: []Address{{Email: "bob@work.example"}}, ReceivedAt: time.Unix(1700000003, 0)},
		{ID: "in2", From: []Address{{Email: "bob@work.example"}}, To: []Address{me}, ReceivedAt: time.Unix(1700000002, 0)},
		{ID: "other", From: []Address{{Email: "alice@example.com"}}, To: []Address{me}, ReceivedAt: time.Unix(1700000001, 0)},
	}
	if err := s.WithTx(ctx, func(tx *sql.Tx) error { return UpsertEmails(ctx, tx, "c", msgs) }); err != nil {
		t.Fatalf("seed: %v", err)
	}
	addrs := []string{"bob@example.com", "bob@work.example"}
	ids := func(list []EmailSummary) string {
		var out []string
		for _, e := range list {
			out = append(out, e.ID)
		}
		return strings.Join(out, ",")
	}
	for _, tc := range []struct {
		dir, want string
		total     int
	}{
		{DirectionAll, "in1,out1,out2,in2", 4},
		{DirectionFrom, "in1,in2", 2},
		{DirectionTo, "out1,out2", 2},
	} {
		got, total, err := s.EmailsWith(ctx, addrs, tc.dir, 10, 0)
		if err != nil {
			t.Fatalf("%q: %v", tc.dir, err)
		}
		if ids(got) != tc.want || total != tc.total {
			t.Errorf("%q = %s (%d), want %s (%d)", tc.dir, ids(got), total, tc.want, tc.total)
		}
	}
	page, total, _ := s.EmailsWith(ctx, addrs, DirectionAll, 2, 2)
	if ids(page) != "out2,in2" || total != 4 {
		t.Errorf("page 2 = %s (%d)", ids(page), total)
	}
}
