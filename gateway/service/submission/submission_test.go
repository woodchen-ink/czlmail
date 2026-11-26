package submission_test

import (
	"bytes"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/pkg/smtptest"
)

// The gateway strategy: Mailpit gets the message without Bcc and with the right
// envelope; the draft moves to Sent under its id; nothing is left in Drafts.
func TestSendGatewayCopy(t *testing.T) {
	h := newHarness(t, harnessOptions{sentCopy: "gateway"})
	mp := smtptest.New(t)
	to, cc, bcc := smtptest.UniqueAddress("to"), smtptest.UniqueAddress("cc"), smtptest.UniqueAddress("bcc")
	msgID := uniqueID("gw")
	draft := h.appendDraft(h.draft(msgID, to, cc, bcc), msgID)

	created, notCreated, emailSet := h.submit(draft, nil)
	if len(notCreated) > 0 || created["sub"] == nil {
		t.Fatalf("not created: %v", notCreated)
	}
	sub := created["sub"].(map[string]any)
	if sub["undoStatus"] != "final" || sub["id"] == "" {
		t.Fatalf("created %v", sub)
	}
	if _, ok := emailSet["updated"].(map[string]any)[draft]; !ok {
		t.Fatalf("implicit Email/set: %v", emailSet)
	}

	msgs := mp.WaitFor("to:"+to, 1, 10*time.Second)
	// Mailpit puts its own Bcc: and Received: lines in front of what it got.
	raw := mp.Raw(msgs[0].ID)
	if i := bytes.Index(raw, []byte("\nFrom: ")); i >= 0 {
		raw = raw[i:]
	}
	if bytes.Contains(raw, []byte("Hidden")) || bytes.Contains(bytes.ToLower(raw), []byte("\nbcc:")) || bytes.Contains(raw, []byte(bcc)) {
		t.Fatalf("Bcc reached the recipients:\n%s", raw)
	}
	// Mailpit lists envelope recipients missing from the header as Bcc.
	if b := msgs[0].Bcc; len(b) != 1 || b[0].Address != bcc {
		t.Fatalf("envelope Bcc %v, want %s", b, bcc)
	}
	if c := msgs[0].Cc; len(c) != 1 || c[0].Address != cc {
		t.Fatalf("Cc %v, want %s", c, cc)
	}
	if msgs[0].MessageID != msgID {
		t.Fatalf("Message-ID %q, want %q", msgs[0].MessageID, msgID)
	}

	if id := h.waitEmail(msgID, "sent", 1); id != draft {
		t.Fatalf("Sent copy is %s, want the draft's id %s", id, draft)
	}
	if kw := h.keywords(draft); kw["$draft"] || !kw["$seen"] {
		t.Fatalf("keywords %v", kw)
	}
	if n := len(h.box.UIDs("Drafts")); n != 0 {
		t.Fatalf("%d messages left in Drafts", n)
	}
	if n := len(h.box.UIDs("Sent")); n != 1 {
		t.Fatalf("%d messages in Sent, want 1", n)
	}

	// EmailSubmission/get, /query and /changes see it.
	subID := sub["id"].(string)
	r := h.api(
		[]any{"EmailSubmission/query", map[string]any{"accountId": h.acct, "filter": map[string]any{"emailIds": []string{draft}}}, "q"},
		[]any{"EmailSubmission/get", map[string]any{"accountId": h.acct, "#ids": map[string]any{"resultOf": "q", "name": "EmailSubmission/query", "path": "/ids"}}, "g"},
		[]any{"EmailSubmission/changes", map[string]any{"accountId": h.acct, "sinceState": "0"}, "c"},
	)
	if ids := args(t, r[0], "EmailSubmission/query")["ids"].([]any); len(ids) != 1 || ids[0] != subID {
		t.Fatalf("query ids %v", ids)
	}
	got := args(t, r[1], "EmailSubmission/get")["list"].([]any)[0].(map[string]any)
	env := got["envelope"].(map[string]any)
	var rcpts []string
	for _, x := range env["rcptTo"].([]any) {
		rcpts = append(rcpts, x.(map[string]any)["email"].(string))
	}
	if env["mailFrom"].(map[string]any)["email"] != h.email || !slices.Equal(rcpts, []string{to, cc, bcc}) {
		t.Fatalf("envelope %v", env)
	}
	if ds := got["deliveryStatus"].(map[string]any)[bcc].(map[string]any); ds["delivered"] != "queued" {
		t.Fatalf("delivery status %v", ds)
	}
	if c := args(t, r[2], "EmailSubmission/changes")["created"].([]any); len(c) != 1 || c[0] != subID {
		t.Fatalf("changes %v", c)
	}
}

// The server strategy: the server's own Sent copy (simulated by appending it)
// takes over the draft's id; the draft is deleted; Sent holds one message.
func TestSendServerCopy(t *testing.T) {
	h := newHarness(t, harnessOptions{sentCopy: "server", wait: 10 * time.Second})
	smtptest.New(t)
	to := smtptest.UniqueAddress("to")
	msgID := uniqueID("srv")
	raw := h.draft(msgID, to, "", "")
	draft := h.appendDraft(raw, msgID)

	go func() {
		time.Sleep(time.Second)
		h.box.Append("Sent", append([]byte("X-Server-Copy: yes\r\n"), raw...))
	}()
	created, notCreated, _ := h.submit(draft, nil)
	if created["sub"] == nil {
		t.Fatalf("not created: %v", notCreated)
	}
	if id := h.waitEmail(msgID, "sent", 1); id != draft {
		t.Fatalf("Sent copy is %s, want %s", id, draft)
	}
	if n := len(h.box.UIDs("Drafts")); n != 0 {
		t.Fatalf("%d messages left in Drafts", n)
	}
	uids := h.box.UIDs("Sent")
	if len(uids) != 1 {
		t.Fatalf("%d messages in Sent, want 1", len(uids))
	}
	if kw := h.keywords(draft); kw["$draft"] {
		t.Fatalf("keywords %v", kw)
	}
	// A later sync round must not create a second email.
	time.Sleep(2 * time.Second)
	if got := h.emailsByMessageID(msgID); len(got) != 1 {
		t.Fatalf("emails %v", got)
	}
}

// Auto strategy, no server copy within the wait: the gateway files the draft and
// learns "gateway"; a server copy turning up later replaces the gateway's and
// switches the upstream to "server".
func TestSendLateServerCopy(t *testing.T) {
	h := newHarness(t, harnessOptions{wait: 1500 * time.Millisecond})
	smtptest.New(t)
	msgID := uniqueID("late")
	raw := h.draft(msgID, smtptest.UniqueAddress("to"), "", "")
	draft := h.appendDraft(raw, msgID)
	if created, notCreated, _ := h.submit(draft, nil); created["sub"] == nil {
		t.Fatalf("not created: %v", notCreated)
	}
	if id := h.waitEmail(msgID, "sent", 1); id != draft {
		t.Fatalf("Sent copy is %s, want %s", id, draft)
	}
	if s := h.upstreamSentCopy(); s != "gateway" {
		t.Fatalf("sentCopy %q after a send without server copy", s)
	}

	h.box.Append("Sent", append([]byte("X-Server-Copy: late\r\n"), raw...))
	deadline := time.Now().Add(20 * time.Second)
	for {
		uids := h.box.UIDs("Sent")
		got := h.emailsByMessageID(msgID)
		if len(uids) == 1 && len(got) == 1 && got[draft] != nil && h.upstreamSentCopy() == "server" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Sent uids %v, emails %v, sentCopy %q", uids, got, h.upstreamSentCopy())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// The same draft content submitted twice within the window is sent once.
func TestDuplicateSubmit(t *testing.T) {
	h := newHarness(t, harnessOptions{sentCopy: "gateway"})
	mp := smtptest.New(t)
	to := smtptest.UniqueAddress("dup")
	msgID := uniqueID("dup")
	draft := h.appendDraft(h.draft(msgID, to, "", ""), msgID)
	ident := h.identityID()
	send := func() string {
		r := h.api([]any{"EmailSubmission/set", map[string]any{"accountId": h.acct,
			"create": map[string]any{"k": map[string]any{"identityId": ident, "emailId": draft}}}, "0"})
		a := args(t, r[0], "EmailSubmission/set")
		c, _ := a["created"].(map[string]any)
		if c["k"] == nil {
			t.Fatalf("not created: %v", a["notCreated"])
		}
		return c["k"].(map[string]any)["id"].(string)
	}
	first, second := send(), send()
	if first != second {
		t.Fatalf("duplicate submit created %s and %s", first, second)
	}
	mp.WaitFor("to:"+to, 1, 10*time.Second)
	time.Sleep(500 * time.Millisecond)
	if n := len(mp.Search("to:" + to)); n != 1 {
		t.Fatalf("%d deliveries, want 1", n)
	}
}

// From and envelope sender must be identities; "*@domain" aliases cover a domain.
func TestSenderChecks(t *testing.T) {
	fake := smtptest.NewFake(t)
	h := newHarness(t, harnessOptions{smtp: fake, sentCopy: "gateway"})

	foreign := uniqueID("foreign")
	raw := bytes.Replace(h.draft(foreign, "rcpt@example.net", "", ""), []byte(h.email), []byte("boss@example.org"), 1)
	draft := h.appendDraft(raw, foreign)
	if _, nc, _ := h.submit(draft, nil); typeOf(nc) != "forbiddenFrom" {
		t.Fatalf("foreign From: %v", nc)
	}

	own := uniqueID("own")
	draft2 := h.appendDraft(h.draft(own, "rcpt@example.net", "", ""), own)
	env := map[string]any{"mailFrom": map[string]any{"email": "bounce@example.org"}, "rcptTo": []any{map[string]any{"email": "rcpt@example.net"}}}
	if _, nc, _ := h.submit(draft2, env); typeOf(nc) != "forbiddenMailFrom" {
		t.Fatalf("foreign envelope sender: %v", nc)
	}
	if n := len(fake.Messages()); n != 0 {
		t.Fatalf("%d messages sent", n)
	}

	// Register *@example.org in the admin: both now pass.
	h.gw.UserAdmin(t, h.cred).Do(t, "POST", "/api/admin/upstreams/"+h.acct+"/identities", map[string]any{"email": "*@example.org", "name": "Org"}, nil)
	if c, nc, _ := h.submit(draft, nil); c["sub"] == nil {
		t.Fatalf("aliased From: %v", nc)
	}
	if c, nc, _ := h.submit(draft2, env); c["sub"] == nil {
		t.Fatalf("aliased envelope sender: %v", nc)
	}
	msgs := fake.Messages()
	if len(msgs) != 2 || msgs[1].From != "bounce@example.org" {
		t.Fatalf("sent %v", msgs)
	}
}

func typeOf(notCreated map[string]any) string {
	e, _ := notCreated["sub"].(map[string]any)
	s, _ := e["type"].(string)
	return s
}

// SMTP replies map to JMAP errors without the server's words.
func TestSMTPErrorMapping(t *testing.T) {
	var mode atomic.Value
	mode.Store("")
	var mails atomic.Int32
	fake := smtptest.NewFake(t)
	fake.Reply = func(verb, arg string) string {
		m := mode.Load().(string)
		switch {
		case verb == "MAIL":
			mails.Add(1)
			if m == "421" {
				return "421 4.7.0 upstream-secret try later"
			}
		case verb == "RCPT" && m == "rcpt" && strings.HasPrefix(arg, "nobody"):
			return "550 5.1.1 upstream-secret no such user"
		case verb == "DATA" && m == "552":
			return "552 5.3.4 upstream-secret too big"
		case verb == "AUTH" && m == "535":
			return "535 5.7.8 upstream-secret bad credentials"
		}
		return ""
	}
	h := newHarness(t, harnessOptions{smtp: fake, sentCopy: "gateway"})

	check := func(m, want string) map[string]any {
		t.Helper()
		mode.Store(m)
		id := uniqueID("err" + m)
		draft := h.appendDraft(h.draft(id, "ok@example.net", "nobody@example.net", ""), id)
		_, nc, _ := h.submit(draft, nil)
		e, _ := nc["sub"].(map[string]any)
		if e["type"] != want {
			t.Fatalf("%s: %v, want %s", m, nc, want)
		}
		if d, _ := e["description"].(string); strings.Contains(d, "upstream-secret") {
			t.Fatalf("%s: description leaks the server reply: %q", m, d)
		}
		return e
	}
	e := check("rcpt", "invalidRecipients")
	if got := e["invalidRecipients"].([]any); len(got) != 1 || got[0] != "nobody@example.net" {
		t.Fatalf("invalidRecipients %v", got)
	}
	check("552", "tooLarge")
	mails.Store(0)
	check("421", "rateLimit")
	if n := mails.Load(); n != 3 {
		t.Fatalf("421: %d attempts, want 3", n)
	}
	if n := len(fake.Messages()); n != 0 {
		t.Fatalf("%d messages accepted", n)
	}
	e = check("535", "serverUnavailable")
	up := e["czlUpstream"].(map[string]any)
	if up["status"] != "authRequired" || up["code"] != float64(40110) {
		t.Fatalf("czlUpstream %v", up)
	}
	h.gw.WaitUpstream(t, h.cred, h.acct, "authRequired", 10*time.Second)
}

// The admin test send finds no server copy behind Mailpit and stores "gateway".
func TestAdminSendTest(t *testing.T) {
	h := newHarness(t, harnessOptions{wait: time.Second})
	var res struct {
		OK          bool   `json:"ok"`
		SentCopy    string `json:"sentCopy"`
		ServerSaves bool   `json:"serverSaves"`
	}
	h.gw.UserAdmin(t, h.cred).Do(t, "POST", "/api/admin/upstreams/"+h.acct+"/send-test", map[string]any{}, &res)
	if !res.OK || res.ServerSaves || res.SentCopy != "gateway" || h.upstreamSentCopy() != "gateway" {
		t.Fatalf("send test %+v, stored %q", res, h.upstreamSentCopy())
	}
	var view struct {
		SentCopy string `json:"sentCopy"`
	}
	h.gw.UserAdmin(t, h.cred).Do(t, "PATCH", "/api/admin/upstreams/"+h.acct, map[string]any{"sentCopy": "server"}, &view)
	if view.SentCopy != "server" {
		t.Fatalf("PATCH sentCopy: %+v", view)
	}
}

// A draft without Message-ID: the gateway adds one when sending, and the Sent
// copy it files is the delivered message (same Message-ID, Bcc kept) under the
// draft's id.
func TestSendDraftWithoutMessageID(t *testing.T) {
	h := newHarness(t, harnessOptions{sentCopy: "gateway"})
	mp := smtptest.New(t)
	to, bcc := smtptest.UniqueAddress("noid"), smtptest.UniqueAddress("noidbcc")
	tag := uniqueID("noid")
	raw := h.draft(tag, to, "", bcc)
	raw = bytes.Replace(raw, []byte("Message-ID: <"+tag+">\r\n"), nil, 1)
	h.box.Append("Drafts", raw)
	var draft string
	deadline := time.Now().Add(20 * time.Second)
	for draft == "" {
		handle, _ := h.gw.App.Manager.Lookup(h.acct)
		handle.Upstream.Index().DB().Table("emails").Where("subject = ?", "test "+tag).Limit(1).Pluck("id", &draft)
		if time.Now().After(deadline) {
			t.Fatal("draft not indexed")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if c, nc, _ := h.submit(draft, nil); c["sub"] == nil {
		t.Fatalf("not created: %v", nc)
	}
	msgs := mp.WaitFor("to:"+to, 1, 10*time.Second)
	sentID := msgs[0].MessageID
	if sentID == "" {
		t.Fatal("delivered without Message-ID")
	}
	if id := h.waitEmail(sentID, "sent", 1); id != draft {
		t.Fatalf("Sent copy %s, want %s", id, draft)
	}
	uids := h.box.UIDs("Sent")
	if len(uids) != 1 || len(h.box.UIDs("Drafts")) != 0 {
		t.Fatalf("Sent %v, Drafts %v", uids, h.box.UIDs("Drafts"))
	}
}
