package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// 置顶的邮件排在最前, 分页 offset 按合并后的顺序计, 不重复不遗漏。
func TestEmailsByMailboxPinnedFirst(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	var msgs []Email
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		m := Email{ID: id, MailboxIDs: []string{"inbox"}, ReceivedAt: time.Unix(int64(1700000010-i), 0)}
		if id == "c" || id == "e" {
			m.Keywords = []string{"$pinned"}
		}
		msgs = append(msgs, m)
	}
	if err := s.WithTx(ctx, func(tx *sql.Tx) error { return UpsertEmails(ctx, tx, "acc", msgs) }); err != nil {
		t.Fatal(err)
	}

	var got []string
	for offset := 0; offset < 10; offset += 2 {
		page, err := s.EmailsByMailbox(ctx, "acc", "inbox", 2, offset)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range page {
			got = append(got, e.ID)
			if e.IsPinned != (e.ID == "c" || e.ID == "e") {
				t.Fatalf("%s pinned=%v", e.ID, e.IsPinned)
			}
		}
	}
	want := "c e a b d"
	if joined := strings.Join(got, " "); joined != want {
		t.Fatalf("got %q want %q", joined, want)
	}
}
