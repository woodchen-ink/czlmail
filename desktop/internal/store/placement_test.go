package store

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

func TestEmailPlacements(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	boxes := []Mailbox{
		{ID: "inbox", Name: "Inbox", Role: "inbox"},
		{ID: "junk", Name: "Junk", Role: "junk"},
		{ID: "work", Name: "工作"},
	}
	msgs := []Email{
		{ID: "a", MailboxIDs: []string{"inbox"}, Keywords: []string{"$seen", "$notjunk"}, ReceivedAt: time.Unix(1700000000, 0)},
		{ID: "b", MailboxIDs: []string{"work", "inbox"}, ReceivedAt: time.Unix(1700000000, 0)},
		{ID: "c", MailboxIDs: []string{"junk"}, Keywords: []string{"$junk"}, ReceivedAt: time.Unix(1700000000, 0)},
	}
	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := UpsertMailboxes(ctx, tx, "acc", boxes); err != nil {
			return err
		}
		return UpsertEmails(ctx, tx, "acc", msgs)
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := s.EmailPlacements(ctx, "acc", []string{"a", "b", "c", "missing"})
	if err != nil {
		t.Fatalf("placements: %v", err)
	}
	want := []EmailPlacement{
		{ID: "a", MailboxIDs: []string{"inbox"}, NotJunk: true},
		{ID: "b", MailboxIDs: []string{"inbox", "work"}},
		{ID: "c", MailboxIDs: []string{"junk"}, Junk: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}

	// 别的账号里同 id 的邮件不算。
	other, err := s.EmailPlacements(ctx, "other", []string{"a"})
	if err != nil {
		t.Fatalf("other account: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("other account: got %+v", other)
	}
}
