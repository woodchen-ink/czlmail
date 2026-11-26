package gatewaytest

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/gmailtest"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imaptest"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

// Another client moves a message INBOX → Trash after the round's LIST-STATUS
// but before it selects INBOX: Trash still looks unchanged in the snapshot.
// The move must keep the email id (Trash is checked again with a fresh STATUS),
// not destroy it and create a new one.
func TestExternalMoveAfterListStatus(t *testing.T) {
	srv := gmailtest.New(t, gmailtest.Options{Account: "plain@imap.test", Password: "pw"})
	msg, _ := srv.MustAdd(gmailtest.Inbox, imaptest.Sample("moving"))
	other, _ := srv.MustAdd(gmailtest.Inbox, imaptest.Sample("staying"))
	gw := Start(t)
	cred := gw.NewUser(t)
	gw.AllowPrivateNetworks(t)
	host, port := splitAddr(srv.Addr())
	acct := gw.CreateUpstream(t, cred, map[string]any{
		"name": "plain", "email": "plain@imap.test",
		"imap":  map[string]any{"host": host, "port": port, "tls": "plain", "username": "plain@imap.test", "password": "pw"},
		"proxy": map[string]any{"mode": "none"},
	})
	gw.WaitUpstream(t, cred, acct, "ok", 30*time.Second)
	h, _ := gw.App.Manager.Lookup(acct)
	idx := h.Upstream.Index()
	// On this fake, All Mail is a folder too: a plain IMAP view sees two copies.
	var before []string
	idx.DB().Model(&model.IndexEmail{}).Where("subject = ?", "moving").Pluck("id", &before)
	if len(before) == 0 {
		t.Fatal("not indexed")
	}

	// A flag change makes INBOX worth selecting in the next round.
	srv.SetFlags(other, []string{`\Seen`}, nil)
	var armed atomic.Bool
	armed.Store(true)
	srv.SetIntercept(func(cmd string) string {
		up := strings.ToUpper(cmd)
		if (strings.HasPrefix(up, "SELECT ") || strings.HasPrefix(up, "EXAMINE ")) && strings.Contains(up, "INBOX") && armed.CompareAndSwap(true, false) {
			srv.SetLabels(msg, []string{gmailtest.LabelTrash}, nil)
		}
		return ""
	})
	if _, err := h.Upstream.Sync(context.Background(), upstream.SyncRequest{Full: true}); err != nil {
		t.Fatal(err)
	}
	if armed.Load() {
		t.Fatal("the round never selected INBOX")
	}
	var trash model.IndexMailbox
	idx.DB().Where("role = ?", "trash").First(&trash)
	var after []string
	idx.DB().Table("emails e").Joins("JOIN memberships m ON m.email_id = e.id").
		Where("e.subject = ? AND m.mailbox_id = ?", "moving", trash.ID).Pluck("e.id", &after)
	if len(after) != 1 || !slices.Contains(before, after[0]) {
		t.Fatalf("in Trash after the move: %v, want one of the old ids %v", after, before)
	}
}
