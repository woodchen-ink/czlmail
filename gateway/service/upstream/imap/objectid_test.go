package imap_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/gmailtest"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imaptest"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream/imap"
)

// The fake Gmail server with OBJECTID is the only test server offering OBJECTID,
// and the only CONDSTORE-without-QRESYNC tier. The plain IMAP engine sees Gmail's
// labels as independent folders here; the Gmail dialect (#42) maps them properly.
func TestObjectIDAndCondStore(t *testing.T) {
	g := gmailtest.New(t, gmailtest.Options{Account: "me@gmail.test", Password: "app-pass", ObjectID: true})
	g.CreateLabel("Work")
	m1, _ := g.MustAdd(gmailtest.Inbox, imaptest.Sample("one"))
	m2, _ := g.MustAdd(gmailtest.Inbox, imaptest.Sample("two"))

	cfg := config(t, g.Addr(), "me@gmail.test", "app-pass", nil)
	idx, err := mailindex.Open(t.TempDir(), cfg.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	defer idx.Close()
	up := imap.NewWithOptions(cfg, idx, imap.Options{})
	defer up.Close()
	h := &harness{t: t, up: up, idx: idx}

	caps, err := up.Probe(context.Background())
	if err != nil || !caps.ObjectID || !caps.CondStore || caps.QResync || caps.XGUID {
		t.Fatalf("caps %+v %v", caps, err)
	}
	h.sync(upstream.SyncRequest{Full: true})
	inboxUID := g.UID(gmailtest.Inbox, m1)
	id1 := h.emailIn(gmailtest.Inbox, goUID(inboxUID))
	e1 := h.email(id1)
	if e1.UpstreamID == "" || e1.UpstreamID[:4] != "oid:" || e1.UpstreamThreadID == "" {
		t.Fatalf("object ids %q %q", e1.UpstreamID, e1.UpstreamThreadID)
	}
	var work model.IndexMailbox
	idx.DB().First(&work, "name = ?", "Work")
	if work.UpstreamID == "" {
		t.Fatal("no MAILBOXID")
	}

	// CONDSTORE: a flag change arrives through CHANGEDSINCE.
	st := h.state(mailindex.TypeEmail)
	g.SetFlags(m1, []string{`\Seen`}, nil)
	h.sync(upstream.SyncRequest{Mailboxes: []string{gmailtest.Inbox}})
	if !h.email(id1).Seen {
		t.Fatal("seen not synced")
	}
	if ch := h.changes(mailindex.TypeEmail, st); !slices.Contains(ch.Updated, id1) {
		t.Fatalf("flag changes %+v", ch)
	}

	// UIDVALIDITY reset with the same messages: EMAILID re-pairs them, clients see
	// nothing but the one message deleted at the same time (whose orphan makes the
	// round sweep the other folders before destroying it).
	st = h.state(mailindex.TypeEmail)
	id2 := h.emailIn(gmailtest.Inbox, goUID(g.UID(gmailtest.Inbox, m2)))
	g.Delete(m2)
	if err := g.ResetUIDValidity(gmailtest.Inbox); err != nil {
		t.Fatal(err)
	}
	h.sync(upstream.SyncRequest{Mailboxes: []string{gmailtest.Inbox}})
	if got := h.emailIn(gmailtest.Inbox, goUID(g.UID(gmailtest.Inbox, m1))); got != id1 {
		t.Fatalf("id after UIDVALIDITY reset %s, want %s", got, id1)
	}
	// (m2's All Mail copy is destroyed too, and m1's All Mail copy may update: the sweep reads it.)
	if ch := h.changes(mailindex.TypeEmail, st); !slices.Contains(ch.Destroyed, id2) || len(ch.Created) != 0 || slices.Contains(ch.Updated, id1) || slices.Contains(ch.Destroyed, id1) {
		t.Fatalf("reset changes %+v", ch)
	}

	// Moved to another folder elsewhere: same id by EMAILID.
	st = h.state(mailindex.TypeEmail)
	g.SetLabels(m1, []string{"Work"}, []string{gmailtest.LabelInbox})
	h.sync(upstream.SyncRequest{Mailboxes: []string{gmailtest.Inbox}})
	if got := h.emailIn("Work", goUID(g.UID("Work", m1))); got != id1 {
		t.Fatalf("moved id %s, want %s", got, id1)
	}
	// (The All Mail copy may show up as updated too: the sweep also brought its \Seen in.)
	if ch := h.changes(mailindex.TypeEmail, st); !slices.Contains(ch.Updated, id1) || len(ch.Created)+len(ch.Destroyed) != 0 {
		t.Fatalf("move changes %+v", ch)
	}

	// IDLE push from the fake server.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hints := make(chan upstream.Hint, 16)
	go up.Watch(ctx, func(hn upstream.Hint) { hints <- hn })
	time.Sleep(200 * time.Millisecond)
	g.MustAdd(gmailtest.Inbox, imaptest.Sample("three"))
	select {
	case hn := <-hints:
		if hn.Mailbox != gmailtest.Inbox {
			t.Fatalf("hint %+v", hn)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no IDLE push")
	}
}
