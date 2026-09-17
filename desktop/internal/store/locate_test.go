package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func seedForLocate(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()

	boxes := []Mailbox{
		{ID: "inbox", Name: "Inbox", Role: "inbox"},
		{ID: "archive", Name: "Archive", Role: "archive"},
		{ID: "trash", Name: "Trash", Role: "trash"},
		{ID: "work", Name: "工作"},
	}
	// 与 TestEmailsByMailboxPinnedFirst 相同的一批: 顺序是 c e a b d。
	var msgs []Email
	for i, id := range []string{"a", "b", "c", "d", "e"} {
		m := Email{ID: id, MailboxIDs: []string{"inbox"}, ReceivedAt: time.Unix(int64(1700000010-i), 0)}
		if id == "c" || id == "e" {
			m.Keywords = []string{"$pinned"}
		}
		msgs = append(msgs, m)
	}
	msgs = append(msgs,
		Email{ID: "both", MailboxIDs: []string{"archive", "inbox"}, ReceivedAt: time.Unix(1700000000, 0)},
		Email{ID: "gone", MailboxIDs: []string{"trash", "work"}, ReceivedAt: time.Unix(1700000000, 0)},
	)

	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := UpsertMailboxes(ctx, tx, "acc", boxes); err != nil {
			return err
		}
		return UpsertEmails(ctx, tx, "acc", msgs)
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
}

// 下标必须与 EmailsByMailbox 的顺序一致, 否则界面加载到的那一页里没有这封邮件。
func TestLocateEmailIndexMatchesList(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	seedForLocate(t, s)

	list, err := s.EmailsByMailbox(ctx, "acc", "inbox", 100, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for want, e := range list {
		loc, err := s.LocateEmail(ctx, "acc", e.ID, "inbox")
		if err != nil {
			t.Fatalf("locate %s: %v", e.ID, err)
		}
		if loc.MailboxID != "inbox" || loc.Index != want {
			t.Fatalf("%s at %v want inbox/%d", e.ID, loc, want)
		}
	}
}

// 邮件在多个文件夹里时: 优先界面当前打开的那个, 其次收件箱, 垃圾邮件与回收站排最后。
func TestLocateEmailPicksMailbox(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)
	seedForLocate(t, s)

	cases := []struct{ id, prefer, want string }{
		{"both", "", "inbox"},
		{"both", "archive", "archive"},
		{"both", "work", "inbox"}, // prefer 不含这封邮件时忽略
		{"gone", "", "work"},
		{"gone", "trash", "trash"},
	}
	for _, c := range cases {
		loc, err := s.LocateEmail(ctx, "acc", c.id, c.prefer)
		if err != nil {
			t.Fatalf("locate %s: %v", c.id, err)
		}
		if loc.MailboxID != c.want {
			t.Fatalf("locate %s prefer %q: got %q want %q", c.id, c.prefer, loc.MailboxID, c.want)
		}
	}

	if _, err := s.LocateEmail(ctx, "acc", "missing", ""); err != ErrNotFound {
		t.Fatalf("missing email: got %v want ErrNotFound", err)
	}
}
