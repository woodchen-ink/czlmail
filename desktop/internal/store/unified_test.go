package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestEmailsInMailboxesAcrossAccounts(t *testing.T) {
	s := openTemp(t)
	ctx := context.Background()
	at := func(i int) time.Time { return time.Unix(int64(1700000000+i), 0) }
	// 两个账号的收件箱 id 相同(Stalwart 就是这样), 邮件 id 也有重复。
	personal := []Email{
		{ID: "x", MailboxIDs: []string{"a"}, ReceivedAt: at(5), Keywords: []string{"$seen"}},
		{ID: "y", MailboxIDs: []string{"a", "h"}, ReceivedAt: at(3)},
		{ID: "z", MailboxIDs: []string{"b"}, ReceivedAt: at(9)},
	}
	shared := []Email{
		{ID: "x", MailboxIDs: []string{"a"}, ReceivedAt: at(4)},
		{ID: "w", MailboxIDs: []string{"a"}, ReceivedAt: at(6), Keywords: []string{"$seen"}},
	}
	err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := UpsertEmails(ctx, tx, "c", personal); err != nil {
			return err
		}
		return UpsertEmails(ctx, tx, "f", shared)
	})
	if err != nil {
		t.Fatal(err)
	}

	keys := func(list []EmailSummary) string {
		var out []string
		for _, e := range list {
			out = append(out, e.AccountID+"/"+e.ID)
		}
		return strings.Join(out, " ")
	}

	inboxes := []MailboxRef{{"c", "a"}, {"f", "a"}}
	var all []EmailSummary
	for offset := 0; offset < 6; offset += 2 {
		page, err := s.EmailsInMailboxes(ctx, inboxes, false, 2, offset)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, page...)
	}
	if got, want := keys(all), "f/w c/x f/x c/y"; got != want {
		t.Fatalf("inbox: got %q want %q", got, want)
	}

	// 同一封邮件在两个被选中的文件夹里只出现一次。
	both, err := s.EmailsInMailboxes(ctx, []MailboxRef{{"c", "a"}, {"c", "h"}}, false, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := keys(both), "c/x c/y"; got != want {
		t.Fatalf("dedupe: got %q want %q", got, want)
	}

	unread, err := s.EmailsInMailboxes(ctx, inboxes, true, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := keys(unread), "f/x c/y"; got != want {
		t.Fatalf("unread: got %q want %q", got, want)
	}
}
