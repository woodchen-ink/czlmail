package imap_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"slices"
	"strconv"
	"testing"
	"time"

	goimap "github.com/emersion/go-imap/v2"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/pkg/imaptest"
	"github.com/woodchen-ink/czlmail/gateway/pkg/netx"
	"github.com/woodchen-ink/czlmail/gateway/pkg/netx/netxtest"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream/imap"
)

func config(t testing.TB, addr, user, pass string, proxy *netx.Proxy) upstream.Config {
	t.Helper()
	host, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	return upstream.Config{
		AccountID: "a" + strconv.FormatInt(time.Now().UnixNano(), 36),
		Kind:      upstream.KindIMAP,
		IMAP:      upstream.Endpoint{Host: host, Port: p, TLS: imapc.TLSNone, Username: user, Password: pass},
		Dialer:    netx.New(netx.Options{AllowPrivate: true, Proxy: proxy, Timeout: 5 * time.Second}),
	}
}

type harness struct {
	t   *testing.T
	up  *imap.Upstream
	idx *mailindex.Store
	box *imaptest.Mailbox
}

func newHarness(t *testing.T, srv *imaptest.Server) *harness {
	t.Helper()
	user := srv.NewUser(t)
	box := user.Dial(t) // also creates the mailbox
	cfg := config(t, srv.Addr, user.Name, user.Password, nil)
	idx, err := mailindex.Open(t.TempDir(), cfg.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	up := imap.NewWithOptions(cfg, idx, imap.Options{})
	t.Cleanup(func() { up.Close() })
	return &harness{t: t, up: up, idx: idx, box: box}
}

func (h *harness) sync(req upstream.SyncRequest) map[string]string {
	h.t.Helper()
	st, err := h.up.Sync(context.Background(), req)
	if err != nil {
		h.t.Fatalf("sync: %v", err)
	}
	return st
}

func (h *harness) mailboxID(name string) string {
	h.t.Helper()
	var m model.IndexMailbox
	if err := h.idx.DB().First(&m, "name = ?", name).Error; err != nil {
		h.t.Fatalf("mailbox %s: %v", name, err)
	}
	return m.ID
}

// emailIn returns the email id at (folder, uid).
func (h *harness) emailIn(folder string, uid goimap.UID) string {
	h.t.Helper()
	var ms model.IndexMembership
	if err := h.idx.DB().First(&ms, "mailbox_id = ? AND uid = ?", h.mailboxID(folder), uint32(uid)).Error; err != nil {
		h.t.Fatalf("no email at %s/%d: %v", folder, uid, err)
	}
	return ms.EmailID
}

func (h *harness) count(folder string) int {
	var n int64
	h.idx.DB().Model(&model.IndexMembership{}).Where("mailbox_id = ?", h.mailboxID(folder)).Count(&n)
	return int(n)
}

func (h *harness) email(id string) model.IndexEmail {
	h.t.Helper()
	var e model.IndexEmail
	if err := h.idx.DB().First(&e, "id = ?", id).Error; err != nil {
		h.t.Fatalf("email %s: %v", id, err)
	}
	return e
}

func (h *harness) changes(typ, since string) *mailindex.Changes {
	h.t.Helper()
	ch, err := h.idx.Changes(typ, since, 0)
	if err != nil {
		h.t.Fatalf("changes %s since %s: %v", typ, since, err)
	}
	return ch
}

func (h *harness) state(typ string) string {
	st, err := h.idx.State(typ)
	if err != nil {
		h.t.Fatal(err)
	}
	return st
}

func TestDovecotSync(t *testing.T) {
	for _, tc := range []struct {
		name string
		srv  func(testing.TB) *imaptest.Server
	}{{"full", imaptest.Dovecot}, {"basic", imaptest.DovecotBasic}} {
		t.Run(tc.name, func(t *testing.T) { testSync(t, tc.srv(t)) })
	}
}

func testSync(t *testing.T, srv *imaptest.Server) {
	h := newHarness(t, srv)
	first := h.box.Append("INBOX", imaptest.Sample("hello"))
	reply := imaptest.Sample("re: hello")
	// Thread the reply onto the first message.
	firstRaw := imaptest.Sample("x") // placeholder; real Message-ID read below
	_ = firstRaw
	h.box.AppendSample("INBOX", "second", goimap.FlagSeen)
	h.box.AppendSample("Archive", "archived")

	var progress [][2]int
	h.sync(upstream.SyncRequest{Full: true, Progress: func(d, n int) { progress = append(progress, [2]int{d, n}) }})
	if !h.idx.Ready() {
		t.Fatal("not ready after the first full round")
	}
	if len(progress) == 0 || progress[len(progress)-1] != [2]int{3, 3} {
		t.Fatalf("progress %v", progress)
	}
	if h.count("INBOX") != 2 || h.count("Archive") != 1 {
		t.Fatalf("counts inbox %d archive %d", h.count("INBOX"), h.count("Archive"))
	}
	var roles []string
	h.idx.DB().Model(&model.IndexMailbox{}).Where("role <> ''").Order("role").Pluck("role", &roles)
	if !slices.Equal(roles, []string{"archive", "drafts", "inbox", "junk", "sent", "trash"}) {
		t.Fatalf("roles %v", roles)
	}
	e1 := h.email(h.emailIn("INBOX", first))
	if e1.Subject != "hello" || e1.Preview != "Body of hello." || e1.Seen || mailindex.DecodeAddresses(e1.From)[0].Email != "sender@example.test" {
		t.Fatalf("email %+v", e1)
	}

	emailState, threadState := h.state(mailindex.TypeEmail), h.state(mailindex.TypeThread)

	// New message replying to the first: created, same thread.
	reply = []byte("In-Reply-To: <" + e1.MessageID + ">\r\nReferences: <" + e1.MessageID + ">\r\n" + string(reply))
	replyUID := h.box.Append("INBOX", reply)
	if st := h.sync(upstream.SyncRequest{Mailboxes: []string{"INBOX"}}); st[mailindex.TypeEmail] == "" {
		t.Fatalf("no Email state change: %v", st)
	}
	replyID := h.emailIn("INBOX", replyUID)
	if h.email(replyID).ThreadID != e1.ThreadID {
		t.Fatal("reply not threaded")
	}
	ch := h.changes(mailindex.TypeEmail, emailState)
	if !slices.Equal(ch.Created, []string{replyID}) {
		t.Fatalf("created %+v", ch)
	}
	if tc := h.changes(mailindex.TypeThread, threadState); !slices.Equal(tc.Updated, []string{e1.ThreadID}) {
		t.Fatalf("threads %+v", tc)
	}

	// Flag change elsewhere.
	emailState = h.state(mailindex.TypeEmail)
	h.box.AddFlags("INBOX", first, goimap.FlagFlagged, goimap.FlagSeen)
	h.sync(upstream.SyncRequest{Mailboxes: []string{"INBOX"}})
	if e := h.email(e1.ID); !e.Flagged || !e.Seen {
		t.Fatalf("flags not synced: %s", e.Keywords)
	}
	if ch := h.changes(mailindex.TypeEmail, emailState); !slices.Equal(ch.Updated, []string{e1.ID}) {
		t.Fatalf("flag changes %+v", ch)
	}

	// External move: same id, now in Archive; the client sees an update only.
	emailState = h.state(mailindex.TypeEmail)
	newUID := h.box.Move("INBOX", first, "Archive")
	h.sync(upstream.SyncRequest{Mailboxes: []string{"INBOX"}}) // only INBOX hinted: the sweep finds Archive
	if got := h.emailIn("Archive", newUID); got != e1.ID {
		t.Fatalf("moved email got id %s, want %s", got, e1.ID)
	}
	if ch := h.changes(mailindex.TypeEmail, emailState); !slices.Equal(ch.Updated, []string{e1.ID}) || len(ch.Created)+len(ch.Destroyed) != 0 {
		t.Fatalf("move changes %+v", ch)
	}

	// And back into INBOX, again with only INBOX hinted (an IDLE push): Archive is
	// checked before importing, so the message keeps its id instead of appearing twice.
	emailState = h.state(mailindex.TypeEmail)
	backUID := h.box.Move("Archive", newUID, "INBOX")
	h.sync(upstream.SyncRequest{Mailboxes: []string{"INBOX"}})
	if got := h.emailIn("INBOX", backUID); got != e1.ID {
		t.Fatalf("moved back: id %s, want %s", got, e1.ID)
	}
	if ch := h.changes(mailindex.TypeEmail, emailState); !slices.Equal(ch.Updated, []string{e1.ID}) || len(ch.Created)+len(ch.Destroyed) != 0 {
		t.Fatalf("move back changes %+v", ch)
	}

	// Expunge: destroyed.
	emailState = h.state(mailindex.TypeEmail)
	h.box.Expunge("INBOX", replyUID)
	h.sync(upstream.SyncRequest{Mailboxes: []string{"INBOX"}})
	if ch := h.changes(mailindex.TypeEmail, emailState); !slices.Equal(ch.Destroyed, []string{replyID}) {
		t.Fatalf("expunge changes %+v", ch)
	}

	// Folder created and deleted elsewhere.
	mboxState := h.state(mailindex.TypeMailbox)
	h.box.Create("Projects")
	h.box.AppendSample("Projects", "p1")
	h.sync(upstream.SyncRequest{Full: true})
	if h.count("Projects") != 1 {
		t.Fatal("new folder not imported")
	}
	if mc := h.changes(mailindex.TypeMailbox, mboxState); len(mc.Created) != 1 {
		t.Fatalf("mailbox changes %+v", mc)
	}

	// UIDVALIDITY change: the folder is rebuilt; its old message is gone.
	emailState = h.state(mailindex.TypeEmail)
	projects := h.emailIn("Projects", 1)
	if before, after := h.box.Recreate("Projects"); before == after {
		t.Logf("Dovecot reused UIDVALIDITY %d", before)
	}
	pUID := h.box.AppendSample("Projects", "p2")
	h.sync(upstream.SyncRequest{Full: true})
	ch = h.changes(mailindex.TypeEmail, emailState)
	if !slices.Equal(ch.Destroyed, []string{projects}) || !slices.Equal(ch.Created, []string{h.emailIn("Projects", pUID)}) {
		t.Fatalf("uidvalidity changes %+v", ch)
	}

	// A round with nothing to do changes nothing.
	before := h.idx.States()
	h.sync(upstream.SyncRequest{Full: true})
	if after := h.idx.States(); after[mailindex.TypeEmail] != before[mailindex.TypeEmail] || after[mailindex.TypeMailbox] != before[mailindex.TypeMailbox] {
		t.Fatalf("idle round changed states %v -> %v", before, after)
	}
}

func TestDovecotWrites(t *testing.T) {
	h := newHarness(t, imaptest.Dovecot(t))
	uid := h.box.AppendSample("INBOX", "w1")
	h.box.AppendSample("INBOX", "w2")
	h.sync(upstream.SyncRequest{Full: true})
	ctx := context.Background()
	id := h.emailIn("INBOX", uid)

	if _, err := h.up.SetKeywords(ctx, []string{id}, []string{"$seen", "$flagged", "Work"}, nil); err != nil {
		t.Fatal(err)
	}
	if e := h.email(id); e.Keywords != `{"$flagged":true,"$seen":true,"work":true}` { // Dovecot's $HasNoAttachment is dropped
		t.Fatalf("keywords %s", e.Keywords)
	}
	if f := h.box.Flags("INBOX", uid); !slices.Contains(f, goimap.FlagFlagged) || !slices.Contains(f, "Work") {
		t.Fatalf("server flags %v", f)
	}
	// The next sync sees nothing new.
	st := h.state(mailindex.TypeEmail)
	h.sync(upstream.SyncRequest{Mailboxes: []string{"INBOX"}})
	if h.state(mailindex.TypeEmail) != st {
		t.Fatal("own flag change echoed back as a change")
	}

	if _, err := h.up.Move(ctx, []string{id}, h.mailboxID("Archive")); err != nil {
		t.Fatal(err)
	}
	if h.count("Archive") != 1 || h.count("INBOX") != 1 {
		t.Fatalf("after move inbox %d archive %d", h.count("INBOX"), h.count("Archive"))
	}
	st = h.state(mailindex.TypeEmail)
	h.sync(upstream.SyncRequest{Full: true})
	if h.state(mailindex.TypeEmail) != st {
		t.Fatalf("own move echoed back: %+v", h.changes(mailindex.TypeEmail, st))
	}

	copies, _, err := h.up.Copy(ctx, []string{id}, h.mailboxID("INBOX"))
	if err != nil || copies[0] == "" || copies[0] == id {
		t.Fatalf("copy %v %v", copies, err)
	}

	raw := imaptest.Sample("appended")
	newID, _, err := h.up.Append(ctx, h.mailboxID("Drafts"), raw, []string{"$draft", "$seen"}, time.Time{})
	if err != nil || newID == "" {
		t.Fatalf("append %q %v", newID, err)
	}
	if e := h.email(newID); !e.Draft || e.Subject != "appended" {
		t.Fatalf("appended %+v", e)
	}
	var buf bytes.Buffer
	if err := h.up.FetchRaw(ctx, newID, &buf); err != nil || !bytes.Equal(buf.Bytes(), raw) {
		t.Fatalf("raw %v %q", err, buf.String())
	}
	buf.Reset()
	if err := h.up.FetchSection(ctx, newID, "1", &buf); err != nil || buf.String() != "Body of appended.\r\n" {
		t.Fatalf("section %v %q", err, buf.String())
	}

	if _, err := h.up.Destroy(ctx, []string{newID}); err != nil {
		t.Fatal(err)
	}
	if h.count("Drafts") != 0 {
		t.Fatal("destroyed email still indexed")
	}

	fid, _, err := h.up.CreateMailbox(ctx, "", "Receipts")
	if err != nil {
		t.Fatal(err)
	}
	childID, _, err := h.up.CreateMailbox(ctx, fid, "2026")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.up.RenameMailbox(ctx, fid, "", "Bills"); err != nil {
		t.Fatal(err)
	}
	var child model.IndexMailbox
	h.idx.DB().First(&child, "id = ?", childID)
	if child.Name != "Bills/2026" && child.Name != "Bills.2026" {
		t.Fatalf("child after rename %q", child.Name)
	}
	st = h.state(mailindex.TypeMailbox)
	h.sync(upstream.SyncRequest{Full: true})
	if h.state(mailindex.TypeMailbox) != st {
		t.Fatalf("own rename echoed back: %+v", h.changes(mailindex.TypeMailbox, st))
	}
	if _, err := h.up.DeleteMailbox(ctx, childID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.up.CreateMailbox(ctx, "", "Bills"); !upstream.IsKind(err, upstream.KindRejected) {
		t.Fatalf("duplicate folder: %v", err)
	}
}

func TestDovecotWatch(t *testing.T) {
	for _, tc := range []struct {
		name string
		srv  func(testing.TB) *imaptest.Server
	}{{"notify", imaptest.Dovecot}, {"idle", imaptest.DovecotBasic}} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.srv(t))
			h.sync(upstream.SyncRequest{Full: true})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			hints := make(chan upstream.Hint, 100)
			done := make(chan error, 1)
			go func() { done <- h.up.Watch(ctx, func(hn upstream.Hint) { hints <- hn }) }()
			time.Sleep(500 * time.Millisecond) // let NOTIFY / IDLE start
			for len(hints) > 0 {
				<-hints // initial STATUS burst from NOTIFY
			}
			start := time.Now()
			h.box.AppendSample("INBOX", "pushed")
			select {
			case hn := <-hints:
				if hn.Mailbox != "INBOX" {
					t.Fatalf("hint %+v", hn)
				}
				if d := time.Since(start); d > 3*time.Second {
					t.Fatalf("push took %v", d)
				}
				t.Logf("push latency %v", time.Since(start))
			case err := <-done:
				t.Fatalf("watch ended: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("no push within 5 s")
			}
			cancel()
			if err := <-done; err != nil {
				t.Fatalf("watch after cancel: %v", err)
			}
		})
	}
}

func TestDovecotFailures(t *testing.T) {
	srv := imaptest.Dovecot(t)
	user := srv.NewUser(t)
	user.Dial(t)

	// Wrong password: authRequired, not retried.
	cfg := config(t, srv.Addr, user.Name, "wrong", nil)
	rep, err := upstream.Probe(context.Background(), cfg)
	if err != nil || rep.IMAP.OK || rep.IMAP.Code != upstream.CodeProbeAuth {
		t.Fatalf("probe wrong password %+v %v", rep, err)
	}
	idx, _ := mailindex.Open(t.TempDir(), cfg.AccountID)
	defer idx.Close()
	up := imap.NewWithOptions(cfg, idx, imap.Options{})
	defer up.Close()
	_, err = up.Sync(context.Background(), upstream.SyncRequest{Full: true})
	if !upstream.IsKind(err, upstream.KindAuth) {
		t.Fatalf("sync wrong password: %v", err)
	}

	// Through a SOCKS5 proxy, then with the proxy gone: unreachable.
	px := netxtest.Start(t, "u", "p")
	cfg = config(t, srv.Addr, user.Name, user.Password, &netx.Proxy{Host: px.Host(), Port: px.Port(), Username: "u", Password: "p"})
	rep, err = upstream.Probe(context.Background(), cfg)
	if err != nil || !rep.IMAP.OK || !rep.IMAP.Capabilities.QResync || !rep.IMAP.Capabilities.XGUID || rep.IMAP.Capabilities.ObjectID {
		t.Fatalf("probe via proxy %+v %v", rep, err)
	}
	if px.Connects.Load() == 0 {
		t.Fatal("proxy not used")
	}
	idx2, _ := mailindex.Open(t.TempDir(), cfg.AccountID)
	defer idx2.Close()
	up2 := imap.NewWithOptions(cfg, idx2, imap.Options{})
	defer up2.Close()
	if _, err := up2.Sync(context.Background(), upstream.SyncRequest{Full: true}); err != nil {
		t.Fatal(err)
	}
	px.Stop()
	up2.Close() // drop pooled connections, as a proxy outage would
	_, err = up2.Sync(context.Background(), upstream.SyncRequest{Full: true})
	var pe *netx.ProxyError
	if !upstream.IsKind(err, upstream.KindUnreachable) || !errors.As(err, &pe) {
		t.Fatalf("sync without proxy: %v", err)
	}
	rep, _ = upstream.Probe(context.Background(), cfg)
	if rep.IMAP.Code != upstream.CodeProbeProxy {
		t.Fatalf("probe without proxy %+v", rep.IMAP)
	}
}

func goUID(u uint32) goimap.UID { return goimap.UID(u) }
