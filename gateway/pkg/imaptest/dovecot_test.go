package imaptest

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
)

func TestDovecotCapabilityTiers(t *testing.T) {
	full := Dovecot(t)
	basic := DovecotBasic(t)

	wantFull := []string{"IMAP4rev1", "CONDSTORE", "QRESYNC", "NOTIFY", "IDLE", "MOVE", "UIDPLUS", "SPECIAL-USE", "LIST-STATUS"}
	if caps := capabilities(t, full.NewUser(t)); !hasAll(caps, wantFull) {
		t.Errorf("full tier capabilities %v, want all of %v", caps, wantFull)
	}

	caps := capabilities(t, basic.NewUser(t))
	if !hasAll(caps, []string{"IMAP4rev1", "IDLE", "MOVE", "UIDPLUS", "SPECIAL-USE", "LIST-STATUS"}) {
		t.Errorf("basic tier capabilities %v miss a baseline extension", caps)
	}
	for _, c := range []string{"CONDSTORE", "QRESYNC", "NOTIFY"} {
		if slices.Contains(caps, c) {
			t.Errorf("basic tier advertises %s", c)
		}
	}
}

func capabilities(t *testing.T, u *User) []string {
	raw := DialRaw(t, u.Server.Addr)
	raw.MustOK("LOGIN %s %s", u.Name, u.Password)
	line := raw.MustOK("CAPABILITY").Find("CAPABILITY ")
	return strings.Fields(strings.TrimPrefix(line, "CAPABILITY "))
}

func hasAll(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}

func TestDovecotMailboxHelpers(t *testing.T) {
	u := Dovecot(t).NewUser(t)
	m := u.Dial(t)

	if got := m.Status("INBOX"); *got.NumMessages != 0 {
		t.Fatalf("new user INBOX has %d messages", *got.NumMessages)
	}

	uid := m.AppendSample("INBOX", "hello")
	m.AddFlags("INBOX", uid, imap.FlagSeen, imap.FlagFlagged)
	m.RemoveFlags("INBOX", uid, imap.FlagFlagged)
	if flags := m.Flags("INBOX", uid); !slices.Contains(flags, imap.FlagSeen) || slices.Contains(flags, imap.FlagFlagged) {
		t.Errorf("flags after store: %v", flags)
	}

	moved := m.Move("INBOX", uid, "Archive")
	if got := m.UIDs("Archive"); !slices.Equal(got, []imap.UID{moved}) {
		t.Errorf("Archive UIDs %v, want [%d]", got, moved)
	}
	if got := m.UIDs("INBOX"); len(got) != 0 {
		t.Errorf("INBOX UIDs after move %v", got)
	}

	m.Create("Projects/2026")
	m.AppendSample("Projects/2026", "in a folder")
	before, after := m.Recreate("Projects/2026")
	t.Logf("UIDVALIDITY %d -> %d", before, after)
	if got := m.UIDs("Projects/2026"); len(got) != 0 {
		t.Errorf("recreated folder still has %v", got)
	}

	keep := m.AppendSample("INBOX", "keep")
	gone := m.AppendSample("INBOX", "gone")
	m.Expunge("INBOX", gone)
	if got := m.UIDs("INBOX"); !slices.Equal(got, []imap.UID{keep}) {
		t.Errorf("INBOX after expunge %v, want [%d]", got, keep)
	}
}

func TestDovecotIdlePush(t *testing.T) {
	u := Dovecot(t).NewUser(t)
	m := u.Dial(t)

	watcher := DialRaw(t, u.Server.Addr)
	watcher.MustOK("LOGIN %s %s", u.Name, u.Password)
	watcher.MustOK("SELECT INBOX")
	watcher.Send("idle IDLE")
	if line, err := watcher.ReadResponse(); err != nil || !strings.HasPrefix(line, "+") {
		t.Fatalf("IDLE continuation: %q %v", line, err)
	}

	m.AppendSample("INBOX", "pushed")
	deadline := time.Now().Add(10 * time.Second)
	for {
		line, err := watcher.ReadResponseWithin(time.Until(deadline))
		if err != nil {
			t.Fatalf("no EXISTS during IDLE: %v", err)
		}
		if line == "* 1 EXISTS" {
			break
		}
	}
	watcher.Send("DONE")
}
