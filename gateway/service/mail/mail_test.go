package mail

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/model"
	"github.com/woodchen-ink/czlmail/gateway/service/mailindex"
)

const relatedMessage = "From: a@example.test\r\nSubject: =?utf-8?q?Gr=C3=BC=C3=9Fe?=\r\nX-Folded: one\r\n two\r\n" +
	"Content-Type: multipart/mixed; boundary=m\r\n\r\n" +
	"--m\r\nContent-Type: multipart/alternative; boundary=a\r\n\r\n" +
	"--a\r\nContent-Type: text/plain; charset=iso-8859-1\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\nGr=FC=DFe\r\n" +
	"--a\r\nContent-Type: multipart/related; boundary=r\r\n\r\n" +
	"--r\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<img src=\"cid:logo\">\r\n" +
	"--r\r\nContent-Type: image/png\r\nContent-ID: <logo>\r\nContent-Transfer-Encoding: base64\r\n\r\naGVsbG8=\r\n" +
	"--r--\r\n--a--\r\n" +
	"--m\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; filename*=utf-8''r%C3%A9sum%C3%A9.pdf\r\n\r\nPDF\r\n" +
	"--m--\r\n"

func ids(parts []*part) []string {
	out := []string{}
	for _, p := range parts {
		out = append(out, p.partID+"="+p.mediaType())
	}
	return out
}

func TestParseStructure(t *testing.T) {
	rm := parseRawMessage([]byte(relatedMessage))
	bl := buildBody(rm.root)
	if got := strings.Join(ids(bl.textBody), ","); got != "1.1=text/plain" {
		t.Errorf("textBody %s", got)
	}
	if got := strings.Join(ids(bl.htmlBody), ","); got != "1.2.1=text/html" {
		t.Errorf("htmlBody %s", got)
	}
	if got := strings.Join(ids(bl.attachments), ","); got != "1.2.2=image/png,2=application/pdf" {
		t.Errorf("attachments %s", got)
	}
	if n := bl.byID["2"].name(); n != "résumé.pdf" {
		t.Errorf("RFC 2231 name %q", n)
	}
	dec, problem := decodeTransfer(rm.sections["1.1"], "quoted-printable")
	text, p2 := decodeCharset(dec, "iso-8859-1")
	if problem || p2 || text != "Grüße" {
		t.Errorf("text %q %v %v", text, problem, p2)
	}
	if img, _ := decodeTransfer(rm.sections["1.2.2"], "base64"); string(img) != "hello" {
		t.Errorf("image %q", img)
	}
	r, _ := io.ReadAll(transferReader(bytes.NewReader([]byte("aGVs\r\nbG8=\r\n")), "base64"))
	if string(r) != "hello" {
		t.Errorf("streamed base64 %q", r)
	}

	fields := headerFields(rm.sections["HEADER"])
	if v := (headerProp{name: "subject", form: "Text"}).value(fields); v != "Grüße" {
		t.Errorf("subject %v", v)
	}
	if v := (headerProp{name: "x-folded", form: "Raw"}).value(fields); v != " one\r\n two" {
		t.Errorf("raw folded %q", v)
	}
	if hp, ok := parseHeaderProp("header:List-Unsubscribe:asURLs:all"); !ok || hp.form != "URLs" || !hp.all {
		t.Errorf("parse header prop %+v %v", hp, ok)
	}
	if _, ok := parseHeaderProp("header:X:all:asText"); ok {
		t.Error("form after :all accepted")
	}
}

func TestSinglePartAlternativeFallback(t *testing.T) {
	// Only HTML in an alternative: textBody falls back to it (RFC 8621 §4.1.4).
	raw := "Content-Type: multipart/alternative; boundary=x\r\n\r\n--x\r\nContent-Type: text/html\r\n\r\n<p>hi</p>\r\n--x--\r\n"
	bl := buildBody(parseRawMessage([]byte(raw)).root)
	if strings.Join(ids(bl.textBody), ",") != "1=text/html" || strings.Join(ids(bl.htmlBody), ",") != "1=text/html" {
		t.Errorf("text %v html %v", ids(bl.textBody), ids(bl.htmlBody))
	}
	single := buildBody(parseRawMessage([]byte("Subject: x\r\n\r\nbody\r\n")).root)
	if p := single.tree; p.partID != "1" || !p.root || p.headerSection() != "HEADER" || ids(single.textBody)[0] != "1=text/plain" {
		t.Errorf("single part %+v", p)
	}
}

func TestApplyEmailPatch(t *testing.T) {
	cur := patchState{keywords: mailindex.Keywords{"$seen": true, "work": true}, mailboxes: []string{"inbox"}}
	known := map[string]bool{"inbox": true, "trash": true}
	resolve := func(id string) (string, bool) { return id, known[id] }
	patch := func(s string) map[string]json.RawMessage {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(s), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	p, se := applyEmailPatch(cur, patch(`{"keywords/$seen": null, "keywords/$Flagged": true, "mailboxIds": {"trash": true}}`), resolve)
	if se != nil || p.MailboxID != "trash" || p.Keywords["$seen"] || !p.Keywords["$flagged"] || !p.Keywords["work"] {
		t.Fatalf("patch %+v %+v", p, se)
	}
	if _, se := applyEmailPatch(cur, patch(`{"mailboxIds/trash": true}`), resolve); se == nil || se.Type != "tooManyMailboxes" {
		t.Errorf("two mailboxes: %+v", se)
	}
	if _, se := applyEmailPatch(cur, patch(`{"mailboxIds/inbox": null}`), resolve); se == nil || se.Type != "invalidProperties" {
		t.Errorf("no mailbox: %+v", se)
	}
	if _, se := applyEmailPatch(cur, patch(`{"keywords/bad word": true}`), resolve); se == nil {
		t.Error("invalid keyword accepted")
	}
	if _, se := applyEmailPatch(cur, patch(`{"mailboxIds/nowhere": true, "mailboxIds/inbox": null}`), resolve); se == nil {
		t.Error("unknown mailbox accepted")
	}
}

func TestBlobIDs(t *testing.T) {
	for _, c := range []struct{ id, email, section string }{
		{blobIDMessage("eabc"), "eabc", ""},
		{blobIDPart("eabc", "1.2"), "eabc", "1.2"},
	} {
		e, s, ok := parseBlobID(c.id)
		if !ok || e != c.email || s != c.section {
			t.Errorf("%s: %q %q %v", c.id, e, s, ok)
		}
	}
	for _, bad := range []string{"", "m", "pe", "pe.", "me.1", "xabc"} {
		if _, _, ok := parseBlobID(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestHighlight(t *testing.T) {
	if v := highlight("Quarterly <report>", []string{"report"}); v != "Quarterly &lt;<mark>report</mark>&gt;" {
		t.Errorf("highlight %v", v)
	}
	if v := highlight("nothing here", []string{"report"}); v != nil {
		t.Errorf("no match %v", v)
	}
}

func TestCache(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCache(dir, 100)
	if err != nil {
		t.Fatal(err)
	}
	var fetches atomic.Int32
	fill := func(data string) func(io.Writer) error {
		return func(w io.Writer) error {
			fetches.Add(1)
			_, err := io.WriteString(w, data)
			return err
		}
	}
	read := func(key, data string) string {
		f, err := c.Open(context.Background(), key, fill(data))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		return string(b)
	}
	if read("a/1", "0123456789") != "0123456789" || read("a/1", "other") != "0123456789" || fetches.Load() != 1 {
		t.Fatalf("hit: %d fetches", fetches.Load())
	}
	// Entries above a quarter of the cache are served but not kept.
	big := strings.Repeat("x", 40)
	if read("a/big", big) != big || read("a/big", big) != big || fetches.Load() != 3 {
		t.Fatalf("big entry: %d fetches", fetches.Load())
	}
	for i := range 12 {
		read("a/k"+itoa(i), "0123456789")
	}
	if c.size > 100 {
		t.Fatalf("cache holds %d bytes", c.size)
	}
	// A restart keeps what fits.
	c2, err := NewCache(dir, 100)
	if err != nil || c2.size == 0 || c2.size > 100 {
		t.Fatalf("reopen: %v %d", err, c2.size)
	}
	c2.DropAccount("a")
	if c2.size != 0 || c2.lru.Len() != 0 {
		t.Fatalf("drop account left %d", c2.size)
	}
}

func TestDeliveredReachesOnNewMail(t *testing.T) {
	idx, err := mailindex.Open(t.TempDir(), "acct1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { idx.Close() })
	inbox := &model.IndexMailbox{Name: "INBOX", Role: "inbox", Attrs: "[]"}
	sent := &model.IndexMailbox{Name: "Sent", Role: "sent", Attrs: "[]"}
	if _, err := idx.Write(func(tx *mailindex.Tx) error {
		if err := tx.CreateMailbox(inbox); err != nil {
			return err
		}
		if err := tx.CreateMailbox(sent); err != nil {
			return err
		}
		tx.SetReady()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got := make(chan []NewMail, 1)
	s, err := New(Options{Log: slog.New(slog.DiscardHandler), OnNewMail: func(user string, mails []NewMail) {
		if user == "u1" {
			got <- mails
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	idx.OnDelivered(func(ids []string) { s.Delivered("u1", "acct1", idx, ids) })
	mk := func(subject string) *model.IndexEmail {
		return &model.IndexEmail{Subject: subject, ReceivedAt: time.Now().UTC(), Size: 10, Keywords: `{"$flagged":true}`,
			From: `[{"name":"Bob","email":"bob@example.test"}]`}
	}
	var inboxMail *model.IndexEmail
	if _, err := idx.Write(func(tx *mailindex.Tx) error {
		inboxMail = mk("Hello")
		if err := tx.CreateEmail(inboxMail, nil, inbox.ID, 1, 0); err != nil {
			return err
		}
		tx.Delivered(inboxMail.ID)
		own := mk("My own copy")
		if err := tx.CreateEmail(own, nil, sent.ID, 1, 0); err != nil {
			return err
		}
		tx.Delivered(own.ID)
		// Created by a gateway write: not delivered.
		return tx.CreateEmail(mk("Appended"), nil, inbox.ID, 2, 0)
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case mails := <-got:
		if len(mails) != 1 {
			t.Fatalf("mails %+v", mails)
		}
		m := mails[0]
		st, _ := idx.State(mailindex.TypeEmail)
		if m.AccountID != "acct1" || m.Subject != "Hello" || m.From[0].Email != "bob@example.test" || m.State != st ||
			m.Email["id"] != inboxMail.ID || m.Email["preview"] == nil || len(m.Keywords) != 1 || m.MailboxIDs[0] != inbox.ID {
			t.Fatalf("mail %+v", m)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no new mail reported")
	}
}

func TestHeaderInjectionRejected(t *testing.T) {
	bad := "x>\r\nBcc: a@b"
	if se := (&createEmail{MessageID: []string{bad}}).checkHeaderValues(); se == nil || se.Properties[0] != "messageId" {
		t.Errorf("message id %+v", se)
	}
	name := "Eve\r\nBcc: a@b"
	if se := (&createEmail{To: []emailAddress{{Name: &name, Email: "e@x"}}}).checkHeaderValues(); se == nil {
		t.Error("address name with CRLF accepted")
	}
	if se := (&createEmail{MessageID: []string{"ok@example.test"}}).checkHeaderValues(); se != nil {
		t.Errorf("valid message id refused: %+v", se)
	}
	if _, ok := parseHeaderProp("header:X\r\nBcc"); ok {
		t.Error("header name with CRLF accepted")
	}
	if !validFolded(" one\r\n two") || validFolded(" one\r\nBcc: x") || validFolded("a\nb") {
		t.Error("validFolded")
	}
}

func TestLenientBase64(t *testing.T) {
	src := "aGVs bG8g\r\nd29y!bGQ\r\n" // stray blank and '!', no padding
	stream, err := io.ReadAll(transferReader(strings.NewReader(src), "base64"))
	whole, problem := decodeTransfer([]byte(src), "base64")
	if err != nil || string(stream) != "hello world" || string(whole) != "hello world" || !problem {
		t.Fatalf("stream %q %v, whole %q %v", stream, err, whole, problem)
	}
}
