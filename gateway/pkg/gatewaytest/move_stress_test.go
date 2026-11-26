package gatewaytest

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	imapv2 "github.com/emersion/go-imap/v2"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imaptest"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// Another client moves INBOX → Trash while flagging and delivering, over and
// over (as TestMailDovecot does once): every move must keep the Email id.
// GATEWAY_TEST_MOVE_ROUNDS sets the repetitions (default 20).
func TestExternalMoveKeepsIDStress(t *testing.T) {
	rounds := 20
	if n, err := strconv.Atoi(os.Getenv("GATEWAY_TEST_MOVE_ROUNDS")); err == nil && n > 0 {
		rounds = n
	}
	mbox := imaptest.Dovecot(t).NewUser(t)
	box := mbox.Dial(t)
	keep := box.AppendSample("INBOX", "flag me")
	gw := Start(t)
	cred := gw.NewUser(t)
	acct := string(gw.AddIMAPUpstream(t, cred, mbox))
	h, _ := gw.App.Manager.Lookup(acct)
	db := h.Upstream.Index().DB()
	idOf := func(subject string, wait time.Duration) (string, string) {
		deadline := time.Now().Add(wait)
		for {
			var row struct{ ID, Role string }
			db.Table("emails e").Select("e.id, b.role").Joins("JOIN memberships m ON m.email_id = e.id").Joins("JOIN mailboxes b ON b.id = m.mailbox_id").
				Where("e.subject = ? AND e.orphaned_from = ''", subject).Limit(1).Scan(&row)
			if row.ID != "" || time.Now().After(deadline) {
				return row.ID, row.Role
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	for i := range rounds {
		subj := fmt.Sprintf("move %d", i)
		uid := box.AppendSample("INBOX", subj)
		id, _ := idOf(subj, 20*time.Second)
		if id == "" {
			t.Fatalf("round %d: %q not indexed", i, subj)
		}
		if i%2 == 0 {
			box.AddFlags("INBOX", keep, imapv2.FlagSeen)
		} else {
			box.RemoveFlags("INBOX", keep, imapv2.FlagSeen)
		}
		box.Move("INBOX", uid, "Trash")
		box.AppendSample("INBOX", fmt.Sprintf("fresh %d", i))
		deadline := time.Now().Add(20 * time.Second)
		for {
			got, role := idOf(subj, 0)
			if role == "trash" {
				if got != id {
					var n int64
					db.Model(&model.IndexEmail{}).Where("id = ?", id).Count(&n)
					t.Fatalf("round %d: moved email has id %s, was %s (old row left: %d)", i, got, id, n)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("round %d: move not seen (now %s in %q)", i, got, role)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// A server's MOVE is a copy and an expunge; another session can see the copy
// in Trash while the message is still in INBOX. That round indexes the copy as
// a new email; when INBOX loses the message in a later round, the original id
// must take the copy's place (and the copy's id go), not be destroyed.
func TestMoveSeenAsCopyThenExpunge(t *testing.T) {
	mbox := imaptest.Dovecot(t).NewUser(t)
	box := mbox.Dial(t)
	uid := box.AppendSample("INBOX", "split move")
	gw := Start(t)
	cred := gw.NewUser(t)
	acct := string(gw.AddIMAPUpstream(t, cred, mbox))
	h, _ := gw.App.Manager.Lookup(acct)
	up, db := h.Upstream, h.Upstream.Index().DB()
	ctx := context.Background()
	var orig model.IndexEmail
	if err := db.Where("subject = ?", "split move").First(&orig).Error; err != nil {
		t.Fatal(err)
	}
	s0, _ := up.Index().State(mailindex.TypeEmail)

	box.Copy("INBOX", uid, "Trash")
	if _, err := up.Sync(ctx, upstream.SyncRequest{Mailboxes: []string{"Trash"}}); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&model.IndexEmail{}).Where("subject = ? AND orphaned_from = ''", "split move").Count(&n)
	if n != 2 {
		t.Fatalf("after the copy: %d emails, want 2 (a copy is a new email)", n)
	}
	box.Expunge("INBOX", uid)
	if _, err := up.Sync(ctx, upstream.SyncRequest{Mailboxes: []string{"INBOX"}}); err != nil {
		t.Fatal(err)
	}
	var rows []model.IndexEmail
	db.Where("subject = ? AND orphaned_from = ''", "split move").Find(&rows)
	if len(rows) != 1 || rows[0].ID != orig.ID {
		t.Fatalf("after the expunge: %d emails (%v), want only the original %s", len(rows), rows, orig.ID)
	}
	var trash model.IndexMailbox
	db.Where("role = ?", "trash").First(&trash)
	var ms []model.IndexMembership
	db.Where("email_id = ?", orig.ID).Find(&ms)
	if len(ms) != 1 || ms[0].MailboxID != trash.ID {
		t.Fatalf("memberships %+v, want Trash", ms)
	}
	ch, err := up.Index().Changes(mailindex.TypeEmail, s0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(ch.Created) != 0 || len(ch.Destroyed) != 0 || len(ch.Updated) != 1 || ch.Updated[0] != orig.ID {
		t.Fatalf("changes %+v, want only %s updated", ch, orig.ID)
	}
}
