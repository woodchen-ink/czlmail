package jmaptest_test

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"git.sr.ht/~rockorager/go-jmap"
	"git.sr.ht/~rockorager/go-jmap/core/push"
	"git.sr.ht/~rockorager/go-jmap/mail/mailbox"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmaptest"
	"github.com/woodchen-ink/czlmail/desktop/internal/store"
	"github.com/woodchen-ink/czlmail/desktop/internal/syncer"
)

func dial(t *testing.T, srv *jmaptest.Server, user, pass string) *jmap.Client {
	t.Helper()
	c := &jmap.Client{SessionEndpoint: srv.SessionURL()}
	c.WithBasicAuth(user, pass)
	if err := c.Authenticate(); err != nil {
		t.Fatal(err)
	}
	return c
}

// 真实的同步器能对着 mock 引导、增量同步、写回, 推送能到。
func TestSyncerAgainstMock(t *testing.T) {
	srv := jmaptest.New(t)
	u := srv.AddUser("alice@one.test", "pw")
	acc := srv.AddAccount("a", "alice@one.test", jmaptest.AllCaps...)
	team := srv.AddAccount("t", "team@one.test")
	u.Grant(acc, true).Grant(team, false)
	first := acc.AddEmail(jmaptest.Email{Subject: "one", From: "x@ext.test", Attachments: []string{"a.pdf"}})

	client := dial(t, srv, "alice@one.test", "pw")
	if len(client.Session.Accounts) != 2 {
		t.Fatalf("accounts = %v", client.Session.Accounts)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := syncer.New(client, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := s.SyncAccount(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	list, err := st.EmailsByMailbox(ctx, "a", "inbox", 50, 0)
	if err != nil || len(list) != 1 || list[0].Subject != "one" || len(list[0].AttachmentNames) != 1 {
		t.Fatalf("bootstrap: %+v %v", list, err)
	}

	second := acc.AddEmail(jmaptest.Email{Subject: "two", From: "y@ext.test"})
	acc.SetKeyword(first, "$seen", true)
	if err := s.SyncMail(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if e, err := st.Email(ctx, "a", second); err != nil || e.Subject != "two" {
		t.Fatalf("incremental create: %+v %v", e, err)
	}
	if err := s.MarkRead(ctx, "a", []string{second}, true); err != nil {
		t.Fatal(err)
	}
	if info, _ := acc.EmailByID(second); len(info.Keywords) != 1 || info.Keywords[0] != "$seen" {
		t.Fatalf("server keywords after MarkRead = %v", info.Keywords)
	}
	if err := s.Delete(ctx, "a", []string{first}); err != nil {
		t.Fatal(err)
	}
	if _, ok := acc.EmailByID(first); ok {
		t.Fatal("delete did not reach the server")
	}

	ids, err := s.Identities(ctx, "a")
	if err != nil || len(ids) != 1 {
		t.Fatalf("identities: %v %v", ids, err)
	}
	if err := s.Send(ctx, syncer.Draft{
		AccountID: "a", IdentityID: string(ids[0].ID), FromEmail: ids[0].Email,
		To: []store.Address{{Email: "z@ext.test"}}, Subject: "sent", TextBody: "hi",
	}, "drafts", "sent"); err != nil {
		t.Fatal(err)
	}
	subs := srv.Submissions()
	if len(subs) != 1 || subs[0].Subject != "sent" || subs[0].AccountID != "a" {
		t.Fatalf("submissions = %+v", subs)
	}
	if info, ok := acc.EmailByID(subs[0].EmailID); !ok || len(info.Mailboxes) != 1 || info.Mailboxes[0] != "sent" {
		t.Fatalf("sent email not moved to sent: %+v", info)
	}

	// 推送
	changes := make(chan *jmap.StateChange, 4)
	es := &push.EventSource{Client: client, Handler: func(c *jmap.StateChange) { changes <- c }}
	go func() { _ = es.Listen() }()
	defer es.Close()
	deadline := time.Now().Add(5 * time.Second)
	for srv.StreamCount(u) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	acc.AddEmail(jmaptest.Email{Subject: "pushed"})
	select {
	case c := <-changes:
		if _, ok := c.Changed["a"]["Email"]; !ok {
			t.Fatalf("state change = %+v", c.Changed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no state change pushed")
	}

	// 凭据被拒与离线
	u.Reject(true)
	if err := (&jmap.Client{SessionEndpoint: srv.SessionURL()}).WithBasicAuth("alice@one.test", "pw").Authenticate(); err == nil {
		t.Fatal("rejected user authenticated")
	}
	u.Reject(false)
	srv.SetMode(jmaptest.Unavailable)
	if err := (&jmap.Client{SessionEndpoint: srv.SessionURL()}).WithBasicAuth("alice@one.test", "pw").Authenticate(); err == nil {
		t.Fatal("authenticated while unavailable")
	}
	srv.SetMode(jmaptest.Online)

	// 请求里用了不属于这个用户的账号 id (比如带前缀的本机 id) 时报 accountNotFound。
	req := &jmap.Request{}
	req.Invoke(&mailbox.Get{Account: "s2~a"})
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if me, ok := resp.Responses[0].Args.(*jmap.MethodError); !ok || me.Type != "accountNotFound" {
		t.Fatalf("foreign account id: %+v", resp.Responses[0].Args)
	}
}
