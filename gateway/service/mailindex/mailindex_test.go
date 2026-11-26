package mailindex_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
)

func open(t *testing.T) *mailindex.Store {
	t.Helper()
	s, err := mailindex.Open(t.TempDir(), "acc1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func write(t *testing.T, s *mailindex.Store, fn func(*mailindex.Tx) error) map[string]string {
	t.Helper()
	st, err := s.Write(fn)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func email(msgID string) *model.IndexEmail {
	return &model.IndexEmail{MessageID: msgID, Size: 100, ReceivedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Keywords: "{}"}
}

func TestStatesAndChanges(t *testing.T) {
	s := open(t)
	if _, err := s.State(mailindex.TypeEmail); !errors.Is(err, mailindex.ErrNotReady) {
		t.Fatalf("state before ready: %v", err)
	}
	inbox := &model.IndexMailbox{Name: "INBOX", Role: "inbox"}
	var first *model.IndexEmail
	// The first import is not logged.
	if st := write(t, s, func(tx *mailindex.Tx) error {
		if err := tx.CreateMailbox(inbox); err != nil {
			return err
		}
		first = email("a@x")
		return tx.CreateEmail(first, nil, inbox.ID, 1, 0)
	}); st != nil {
		t.Fatalf("states before ready: %v", st)
	}
	st := write(t, s, func(tx *mailindex.Tx) error { tx.SetReady(); return nil })
	gen := s.Meta().Generation
	start := st[mailindex.TypeEmail]
	if start != itoa(gen)+"-0" {
		t.Fatalf("start state %v", st)
	}

	// A reply joins the thread; a parent arriving later joins it too.
	reply, parent := email("b@x"), email("p@x")
	write(t, s, func(tx *mailindex.Tx) error {
		if err := tx.CreateEmail(reply, []string{"a@x"}, inbox.ID, 2, 0); err != nil {
			return err
		}
		orphanChild := email("c@x")
		if err := tx.CreateEmail(orphanChild, []string{"p@x"}, inbox.ID, 3, 0); err != nil {
			return err
		}
		if err := tx.CreateEmail(parent, nil, inbox.ID, 4, 0); err != nil {
			return err
		}
		if parent.ThreadID != orphanChild.ThreadID {
			t.Errorf("parent thread %s, child %s", parent.ThreadID, orphanChild.ThreadID)
		}
		return nil
	})
	if reply.ThreadID != first.ThreadID {
		t.Fatalf("reply thread %s != %s", reply.ThreadID, first.ThreadID)
	}

	ch, err := s.Changes(mailindex.TypeEmail, start, 0)
	if err != nil || len(ch.Created) != 3 || len(ch.Updated) != 0 {
		t.Fatalf("%+v %v", ch, err)
	}
	mid := ch.NewState

	// Seen flag: Email updated and Mailbox counts-only.
	write(t, s, func(tx *mailindex.Tx) error {
		return tx.SetKeywords(inbox.ID, 1, mailindex.FlagsToKeywords([]string{`\Seen`, "Work"}), 5)
	})
	ch, _ = s.Changes(mailindex.TypeEmail, mid, 0)
	if !slices.Equal(ch.Updated, []string{first.ID}) {
		t.Fatalf("updated %+v", ch)
	}
	mc, _ := s.Changes(mailindex.TypeMailbox, start, 0)
	if !slices.Equal(mc.Updated, []string{inbox.ID}) || !mc.CountsOnly {
		t.Fatalf("mailbox changes %+v", mc)
	}
	var got model.IndexEmail
	s.DB().First(&got, "id = ?", first.ID)
	if got.Keywords != `{"$seen":true,"work":true}` || !got.Seen {
		t.Fatalf("keywords %s", got.Keywords)
	}
	// Stale modseq is ignored.
	write(t, s, func(tx *mailindex.Tx) error { return tx.SetKeywords(inbox.ID, 1, mailindex.Keywords{}, 4) })
	s.DB().First(&got, "id = ?", first.ID)
	if !got.Seen {
		t.Fatal("stale flags applied")
	}

	// Paging.
	ch, _ = s.Changes(mailindex.TypeEmail, start, 2)
	if !ch.HasMoreChanges || len(ch.Created) != 2 {
		t.Fatalf("page %+v", ch)
	}
	ch2, _ := s.Changes(mailindex.TypeEmail, ch.NewState, 0)
	if len(ch2.Created)+len(ch2.Updated) != 2 || ch2.HasMoreChanges {
		t.Fatalf("page 2 %+v", ch2)
	}

	if _, err := s.Changes(mailindex.TypeEmail, "1-0", 0); !errors.Is(err, mailindex.ErrCannotCalculateChanges) {
		t.Fatalf("other generation: %v", err)
	}
	if _, err := s.Changes(mailindex.TypeEmail, "garbage", 0); !errors.Is(err, mailindex.ErrCannotCalculateChanges) {
		t.Fatalf("garbage: %v", err)
	}
}

func TestOrphansAndMoves(t *testing.T) {
	s := open(t)
	inbox := &model.IndexMailbox{Name: "INBOX"}
	archive := &model.IndexMailbox{Name: "Archive"}
	e1, e2 := email("1@x"), email("2@x")
	write(t, s, func(tx *mailindex.Tx) error {
		tx.SetReady()
		tx.CreateMailbox(inbox)
		tx.CreateMailbox(archive)
		tx.CreateEmail(e1, nil, inbox.ID, 1, 0)
		return tx.CreateEmail(e2, nil, inbox.ID, 2, 0)
	})
	before, _ := s.State(mailindex.TypeEmail)
	threadsBefore, _ := s.State(mailindex.TypeThread)
	// Both vanish from INBOX; e1 shows up in Archive, e2 is gone.
	write(t, s, func(tx *mailindex.Tx) error { return tx.Detach(inbox.ID, []uint32{1, 2}) })
	write(t, s, func(tx *mailindex.Tx) error {
		orphans, err := tx.Orphans()
		if err != nil || len(orphans) != 2 {
			t.Fatalf("orphans %v %v", orphans, err)
		}
		for _, o := range orphans {
			if o.ID == e1.ID {
				return tx.Attach(o, archive.ID, 7, 0)
			}
		}
		return nil
	})
	write(t, s, func(tx *mailindex.Tx) error {
		n, err := tx.DestroyOrphans()
		if n != 1 {
			t.Errorf("destroyed %d", n)
		}
		return err
	})
	ch, _ := s.Changes(mailindex.TypeEmail, before, 0)
	if !slices.Equal(ch.Updated, []string{e1.ID}) || !slices.Equal(ch.Destroyed, []string{e2.ID}) {
		t.Fatalf("%+v", ch)
	}
	tc, _ := s.Changes(mailindex.TypeThread, threadsBefore, 0)
	if !slices.Equal(tc.Destroyed, []string{e2.ThreadID}) {
		t.Fatalf("threads %+v", tc)
	}

	// UIDVALIDITY reset: re-attach in the same mailbox is invisible.
	mid, _ := s.State(mailindex.TypeEmail)
	write(t, s, func(tx *mailindex.Tx) error { return tx.DetachAll(archive.ID) })
	write(t, s, func(tx *mailindex.Tx) error {
		orphans, _ := tx.Orphans()
		return tx.Attach(orphans[0], archive.ID, 1, 0)
	})
	if st, _ := s.State(mailindex.TypeEmail); st != mid {
		t.Fatalf("state moved %s -> %s", mid, st)
	}
}

func TestTrim(t *testing.T) {
	s := open(t)
	inbox := &model.IndexMailbox{Name: "INBOX"}
	write(t, s, func(tx *mailindex.Tx) error { tx.SetReady(); return tx.CreateMailbox(inbox) })
	start, _ := s.State(mailindex.TypeEmail)
	for i := range 5 {
		write(t, s, func(tx *mailindex.Tx) error { return tx.CreateEmail(email(itoa(int64(i))+"@x"), nil, inbox.ID, uint32(i+1), 0) })
	}
	n, err := s.Trim(30*24*time.Hour, 3)
	if err != nil || n == 0 {
		t.Fatalf("trim %d %v", n, err)
	}
	if _, err := s.Changes(mailindex.TypeEmail, start, 0); !errors.Is(err, mailindex.ErrCannotCalculateChanges) {
		t.Fatalf("trimmed state: %v", err)
	}
	cur, _ := s.State(mailindex.TypeEmail)
	if _, err := s.Changes(mailindex.TypeEmail, cur, 0); err != nil {
		t.Fatalf("current state: %v", err)
	}
	mb, _ := s.State(mailindex.TypeMailbox)
	if _, err := s.Changes(mailindex.TypeMailbox, mb, 0); err != nil {
		t.Fatalf("current mailbox state after trim: %v", err)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
