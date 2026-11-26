package mailindex

import (
	"testing"

	"github.com/woodchen-ink/czlmail/gateway/model"
)

func TestOnCommitReportsChangedStates(t *testing.T) {
	s, err := Open(t.TempDir(), "acct")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var got []map[string]string
	s.OnCommit(func(st map[string]string) { got = append(got, st) })
	// Before the first import finished there is no state to report.
	if _, err := s.Write(func(tx *Tx) error { return tx.CreateMailbox(&model.IndexMailbox{Name: "INBOX", Attrs: "[]"}) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("reported before ready: %v", got)
	}
	if _, err := s.Write(func(tx *Tx) error { tx.SetReady(); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0]) != 3 {
		t.Fatalf("ready: %v", got)
	}
	// A commit that changes nothing reports nothing; one that does reports its types.
	if _, err := s.Write(func(*Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	states, err := s.Write(func(tx *Tx) error { return tx.CreateMailbox(&model.IndexMailbox{Name: "Work", Attrs: "[]"}) })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1][TypeMailbox] != states[TypeMailbox] || got[1][TypeEmail] != "" {
		t.Fatalf("after create: %v, returned %v", got, states)
	}
}
