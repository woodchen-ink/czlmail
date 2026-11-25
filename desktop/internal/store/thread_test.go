package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// 收件箱里的会话不列出、不计入已删到回收站的往来; 从回收站里看时全部列出。
func TestThreadExcludesDiscarded(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	boxes := []Mailbox{
		{ID: "inbox", Name: "Inbox", Role: "inbox"},
		{ID: "sent", Name: "Sent", Role: "sent"},
		{ID: "trash", Name: "Trash", Role: "trash"},
	}
	at := func(i int) time.Time { return time.Unix(int64(1700000000+i), 0) }
	msgs := []Email{
		{ID: "q", ThreadID: "t", MailboxIDs: []string{"inbox"}, ReceivedAt: at(0)},
		{ID: "r1", ThreadID: "t", MailboxIDs: []string{"sent"}, ReceivedAt: at(1)},
		{ID: "r2", ThreadID: "t", MailboxIDs: []string{"trash"}, ReceivedAt: at(2)},
		{ID: "r3", ThreadID: "t", MailboxIDs: []string{"trash"}, ReceivedAt: at(3)},
	}
	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := UpsertMailboxes(ctx, tx, "acc", boxes); err != nil {
			return err
		}
		return UpsertEmails(ctx, tx, "acc", msgs)
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	ids := func(anchor string) []string {
		list, err := s.ThreadEmails(ctx, "acc", anchor, 100)
		if err != nil {
			t.Fatalf("thread %s: %v", anchor, err)
		}
		var out []string
		for _, e := range list {
			out = append(out, e.ID)
		}
		return out
	}
	if got := ids("q"); len(got) != 2 || got[0] != "q" || got[1] != "r1" {
		t.Fatalf("from inbox: got %v want [q r1]", got)
	}
	if got := ids("r2"); len(got) != 4 {
		t.Fatalf("from trash: got %v want all 4", got)
	}

	count := func(box string) int {
		list, err := s.EmailsByMailbox(ctx, "acc", box, 10, 0)
		if err != nil || len(list) == 0 {
			t.Fatalf("list %s: %v %v", box, list, err)
		}
		return list[0].ThreadCount
	}
	if n := count("inbox"); n != 2 {
		t.Fatalf("inbox thread count = %d want 2", n)
	}
	if n := count("trash"); n != 4 {
		t.Fatalf("trash thread count = %d want 4", n)
	}

	latest, err := s.LatestInThread(ctx, "acc", "t")
	if err != nil || latest != "r1" {
		t.Fatalf("latest = %q, %v want r1", latest, err)
	}
}

// 会话行带上会话里的草稿数 (跨文件夹); 删到回收站的草稿与 ThreadCount 一样不算, 从回收站里看时都算。
func TestThreadDraftCount(t *testing.T) {
	ctx := context.Background()
	s := openTemp(t)

	boxes := []Mailbox{
		{ID: "inbox", Name: "Inbox", Role: "inbox"},
		{ID: "drafts", Name: "Drafts", Role: "drafts"},
		{ID: "trash", Name: "Trash", Role: "trash"},
	}
	at := func(i int) time.Time { return time.Unix(int64(1700000000+i), 0) }
	draft := []string{"$draft", "$seen"}
	msgs := []Email{
		{ID: "q", ThreadID: "t", MailboxIDs: []string{"inbox"}, ReceivedAt: at(0)},
		{ID: "d1", ThreadID: "t", MailboxIDs: []string{"drafts"}, ReceivedAt: at(1), Keywords: draft},
		{ID: "d2", ThreadID: "t", MailboxIDs: []string{"drafts"}, ReceivedAt: at(2), Keywords: draft},
		{ID: "d3", ThreadID: "t", MailboxIDs: []string{"trash"}, ReceivedAt: at(3), Keywords: draft},
		{ID: "other", ThreadID: "u", MailboxIDs: []string{"inbox"}, ReceivedAt: at(4)},
	}
	if err := s.WithTx(ctx, func(tx *sql.Tx) error {
		if err := UpsertMailboxes(ctx, tx, "acc", boxes); err != nil {
			return err
		}
		return UpsertEmails(ctx, tx, "acc", msgs)
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	drafts := func(box string) map[string]int {
		list, err := s.EmailsByMailbox(ctx, "acc", box, 10, 0)
		if err != nil {
			t.Fatalf("list %s: %v", box, err)
		}
		out := map[string]int{}
		for _, e := range list {
			out[e.ID] = e.DraftCount
		}
		return out
	}
	if got := drafts("inbox"); got["q"] != 2 || got["other"] != 0 {
		t.Fatalf("inbox draft counts = %v want q:2 other:0", got)
	}
	if got := drafts("drafts"); got["d1"] != 2 || got["d2"] != 2 {
		t.Fatalf("drafts draft counts = %v want d1:2 d2:2", got)
	}
	if got := drafts("trash"); got["d3"] != 3 {
		t.Fatalf("trash draft counts = %v want d3:3", got)
	}

	byID, err := s.EmailsByIDs(ctx, "acc", []string{"q"})
	if err != nil || len(byID) != 1 || byID[0].DraftCount != 2 {
		t.Fatalf("by ids: %+v %v want draftCount 2", byID, err)
	}
}
