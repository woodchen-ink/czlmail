package gmailtest

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imaptest"
	"github.com/woodchen-ink/czlmail/gateway/pkg/oauthtest"
)

const appPassword = "app-password"

func newServer(t *testing.T, opts Options) (*Server, *imaptest.Raw) {
	t.Helper()
	if opts.Password == "" {
		opts.Password = appPassword
	}
	s := New(t, opts)
	raw := imaptest.DialRaw(t, s.Addr())
	raw.MustOK("LOGIN %s %s", s.Account(), appPassword)
	return s, raw
}

var appendUID = regexp.MustCompile(`APPENDUID (\d+) (\d+)`)

// Trap 1: a UID only means something in its own folder.
func TestTrapUIDsArePerFolder(t *testing.T) {
	s, c := newServer(t, Options{})
	inboxMsg, inboxUID := s.MustAdd(Inbox, imaptest.Sample("in the inbox"))
	if inboxUID != 1 || s.UID(AllMail, inboxMsg) != 1 {
		t.Fatalf("fixture: INBOX uid %d, All Mail uid %d", inboxUID, s.UID(AllMail, inboxMsg))
	}

	draft := imaptest.Sample("my draft")
	rep := c.CmdLiteral(`APPEND "[Gmail]/Drafts" (\Draft \Seen)`, draft, "")
	m := appendUID.FindStringSubmatch(rep.Status)
	if !rep.OK() || m == nil {
		t.Fatalf("APPEND: %s", rep.Status)
	}
	draftUID, _ := strconv.Atoi(m[2])
	if draftUID != 1 {
		t.Fatalf("APPENDUID uid %d, want 1 (Drafts numbers its own messages)", draftUID)
	}
	// The trap: UID 1 in All Mail is the INBOX message, not the draft.
	if got := s.At(AllMail, 1); got != inboxMsg {
		t.Fatalf("All Mail UID 1 should be the inbox message")
	}

	// The right way: X-GM-MSGID from the folder APPEND went to, then search All Mail.
	c.MustOK(`SELECT "[Gmail]/Drafts"`)
	line := c.MustOK("UID FETCH %d (X-GM-MSGID)", draftUID).Find("X-GM-MSGID")
	gmID := regexp.MustCompile(`X-GM-MSGID (\d+)`).FindStringSubmatch(line)[1]
	c.MustOK(`SELECT "[Gmail]/All Mail"`)
	hits := c.MustOK("UID SEARCH X-GM-MSGID %s", gmID).Find("SEARCH")
	if hits != "SEARCH 2" {
		t.Fatalf("All Mail search: %q, want SEARCH 2", hits)
	}
}

// Trap 2: labels with spaces must be quoted.
func TestTrapLabelsWithSpaces(t *testing.T) {
	s, c := newServer(t, Options{})
	msg, uid := s.MustAdd(Inbox, imaptest.Sample("labels"))
	c.MustOK("SELECT INBOX")

	c.MustOK(`UID STORE %d +X-GM-LABELS.SILENT ("Work Projects")`, uid)
	if got := s.LabelsOf(msg); !slices.Contains(got, "Work Projects") {
		t.Fatalf("quoted label: %v", got)
	}
	line := c.MustOK("UID FETCH %d (X-GM-LABELS)", uid).Find("X-GM-LABELS")
	if !strings.Contains(line, `"Work Projects"`) || !strings.Contains(line, `"\\Inbox"`) {
		t.Errorf("FETCH X-GM-LABELS: %s", line)
	}
	if list := c.MustOK(`LIST "" "*"`).Find(`"Work Projects"`); list == "" {
		t.Errorf("label folder not listed")
	}

	c.MustOK(`UID STORE %d +X-GM-LABELS.SILENT (Side Job)`, uid)
	got := s.LabelsOf(msg)
	if !slices.Contains(got, "Side") || !slices.Contains(got, "Job") || slices.Contains(got, "Side Job") {
		t.Errorf("unquoted label should split into two: %v", got)
	}
}

// Trap 3: the \Draft flag and the \Drafts folder are the same thing.
func TestTrapDraftFlagIsDraftsFolder(t *testing.T) {
	s, c := newServer(t, Options{})
	msg, uid := s.MustAdd(Inbox, imaptest.Sample("becomes a draft"))
	c.MustOK("SELECT INBOX")

	c.MustOK(`UID STORE %d +FLAGS.SILENT (\Draft)`, uid)
	if s.UID(Drafts, msg) == 0 {
		t.Fatal("\\Draft flag did not put the message in [Gmail]/Drafts")
	}
	if !slices.Contains(s.LabelsOf(msg), LabelDraft) {
		t.Errorf("X-GM-LABELS %v lacks \\Draft", s.LabelsOf(msg))
	}
	if l := c.MustOK(`LIST "" "*"`).Find(`[Gmail]/Drafts`); !strings.Contains(l, `\Drafts`) {
		t.Errorf("Drafts LIST line %q", l)
	}

	// Removing the label removes the flag.
	c.MustOK(`UID STORE %d -X-GM-LABELS.SILENT ("\\Draft")`, uid)
	if slices.Contains(s.FlagsOf(msg), `\Draft`) || s.UID(Drafts, msg) != 0 {
		t.Errorf("after -X-GM-LABELS \\Draft: flags %v, Drafts uid %d", s.FlagsOf(msg), s.UID(Drafts, msg))
	}
}

func TestXOAuth2(t *testing.T) {
	provider := oauthtest.New(t)
	s := New(t, Options{Account: "me@gmail.test", Tokens: provider})
	good := provider.IssueAccessToken("me@gmail.test")
	sasl := func(tok string) string {
		return base64.StdEncoding.EncodeToString([]byte("user=me@gmail.test\x01auth=Bearer " + tok + "\x01\x01"))
	}

	c := imaptest.DialRaw(t, s.Addr())
	if !strings.Contains(c.Greeting, "OK") {
		t.Fatalf("greeting %q", c.Greeting)
	}
	if rep := c.Cmd("AUTHENTICATE XOAUTH2 %s", sasl(good)); !rep.OK() {
		t.Fatalf("valid token: %s", rep.Status)
	}

	bad := imaptest.DialRaw(t, s.Addr())
	bad.Send("a1 AUTHENTICATE XOAUTH2 " + sasl("expired"))
	line, err := bad.ReadResponse()
	if err != nil || !strings.HasPrefix(line, "+ ") {
		t.Fatalf("expected error challenge, got %q %v", line, err)
	}
	detail, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, "+ "))
	if !strings.Contains(string(detail), `"status":"400"`) {
		t.Errorf("challenge %s", detail)
	}
	bad.Send("")
	if line, _ := bad.ReadResponse(); !strings.HasPrefix(line, "a1 NO [AUTHENTICATIONFAILED]") {
		t.Errorf("final reply %q", line)
	}
}

// The fake works with a stock IMAP client, CONDSTORE and IDLE included.
func TestImapclientCompatibility(t *testing.T) {
	s := New(t, Options{Password: appPassword})
	c, err := imapclient.DialInsecure(s.Addr(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Login(s.Account(), appPassword).Wait(); err != nil {
		t.Fatal(err)
	}
	for _, cap := range []imap.Cap{"X-GM-EXT-1", imap.CapCondStore, imap.CapIdle, imap.CapUIDPlus, imap.CapMove, imap.CapSpecialUse} {
		if !c.Caps().Has(cap) {
			t.Errorf("missing capability %s", cap)
		}
	}

	boxes, err := c.List("", "*", &imap.ListOptions{ReturnSpecialUse: true}).Collect()
	if err != nil {
		t.Fatal(err)
	}
	special := map[imap.MailboxAttr]string{}
	for _, b := range boxes {
		for _, a := range b.Attrs {
			special[a] = b.Mailbox
		}
	}
	if special[imap.MailboxAttrAll] != AllMail || special[imap.MailboxAttrDrafts] != Drafts || special[imap.MailboxAttrNoSelect] != "[Gmail]" {
		t.Errorf("special-use folders %v", special)
	}

	sel, err := c.Select(Inbox, &imap.SelectOptions{CondStore: true}).Wait()
	if err != nil {
		t.Fatal(err)
	}
	start := sel.HighestModSeq

	raw := imaptest.Sample("via imapclient")
	ap := c.Append(Inbox, int64(len(raw)), nil)
	ap.Write(raw)
	ap.Close()
	added, err := ap.Wait()
	if err != nil || added.UID != 1 {
		t.Fatalf("append: %+v %v", added, err)
	}

	if err := c.Store(imap.UIDSetNum(added.UID), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagFlagged}, Silent: true}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	changed, err := c.Fetch(imap.UIDSetNum(added.UID), &imap.FetchOptions{Flags: true, ModSeq: true, ChangedSince: start}).Collect()
	if err != nil || len(changed) != 1 || !slices.Contains(changed[0].Flags, imap.FlagFlagged) {
		t.Fatalf("CHANGEDSINCE fetch: %+v %v", changed, err)
	}
	if got := s.UID(Starred, s.At(Inbox, uint32(added.UID))); got == 0 {
		t.Error("\\Flagged message not in [Gmail]/Starred")
	}

	body, err := c.Fetch(imap.UIDSetNum(added.UID), &imap.FetchOptions{
		Envelope:    true,
		BodySection: []*imap.FetchItemBodySection{{Peek: true}},
	}).Collect()
	if err != nil || len(body) != 1 || body[0].Envelope.Subject != "via imapclient" {
		t.Fatalf("envelope fetch: %+v %v", body, err)
	}
	if got := body[0].FindBodySection(&imap.FetchItemBodySection{}); string(got) != string(raw) {
		t.Errorf("BODY[] %q", got)
	}

	moved, err := c.Move(imap.UIDSetNum(added.UID), "[Gmail]/Trash").Wait()
	if err != nil {
		t.Fatal(err)
	}
	if moved.UIDValidity != s.UIDValidity(Trash) {
		t.Errorf("COPYUID validity %d", moved.UIDValidity)
	}

	// IDLE sees a message the "web UI" adds.
	gotExists := make(chan uint32, 4)
	c2, err := imapclient.DialInsecure(s.Addr(), &imapclient.Options{
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Mailbox: func(d *imapclient.UnilateralDataMailbox) {
				if d.NumMessages != nil {
					gotExists <- *d.NumMessages
				}
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if err := c2.Login(s.Account(), appPassword).Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Select(Inbox, nil).Wait(); err != nil {
		t.Fatal(err)
	}
	idle, err := c2.Idle()
	if err != nil {
		t.Fatal(err)
	}
	s.MustAdd(Inbox, imaptest.Sample("pushed"))
	select {
	case n := <-gotExists:
		if n != 1 {
			t.Errorf("EXISTS %d", n)
		}
	case <-time.After(5 * time.Second):
		t.Error("no EXISTS during IDLE")
	}
	if err := idle.Close(); err != nil {
		t.Fatal(err)
	}
	if err := idle.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestSearchAndThreads(t *testing.T) {
	s, c := newServer(t, Options{})
	first := imaptest.SampleFrom("alice@example.test", s.Account(), "Quarterly report")
	m1, _ := s.MustAdd(Inbox, first)
	msgID := regexp.MustCompile(`Message-ID: (<[^>]+>)`).FindSubmatch(first)[1]
	reply := []byte(fmt.Sprintf("From: %s\r\nTo: alice@example.test\r\nSubject: Re: Quarterly report\r\nIn-Reply-To: %s\r\nReferences: %s\r\n\r\nThanks\r\n", s.Account(), msgID, msgID))
	m2, _ := s.MustAdd(SentMail, reply)
	if m1.GmThrID != m2.GmThrID {
		t.Errorf("reply not threaded: %d vs %d", m1.GmThrID, m2.GmThrID)
	}
	s.MustAdd(Inbox, imaptest.SampleFrom("bob@example.test", s.Account(), "Lunch?"))
	s.SetLabels(m1, []string{"Work Projects"}, nil)

	c.MustOK(`SELECT "[Gmail]/All Mail"`)
	cases := map[string]string{
		`UID SEARCH X-GM-RAW "from:alice"`:                   "SEARCH 1",
		`UID SEARCH X-GM-RAW "label:work-projects"`:          "SEARCH 1",
		`UID SEARCH X-GM-RAW "in:sent"`:                      "SEARCH 2",
		`UID SEARCH X-GM-RAW "lunch -from:alice"`:            "SEARCH 3",
		fmt.Sprintf("UID SEARCH X-GM-THRID %d", m1.GmThrID):  "SEARCH 1 2",
		`UID SEARCH X-GM-LABELS "\\Inbox"`:                   "SEARCH 1 3",
		`UID SEARCH OR SUBJECT lunch FROM alice`:             "SEARCH 1 3",
		`UID SEARCH UNSEEN NOT X-GM-RAW "subject:quarterly"`: "SEARCH 3",
	}
	for cmd, want := range cases {
		if got := c.MustOK("%s", cmd).Find("SEARCH"); got != want {
			t.Errorf("%s: %q, want %q", cmd, got, want)
		}
	}
}

func TestUIDValidityAndLabelFolders(t *testing.T) {
	s, c := newServer(t, Options{})
	c.MustOK(`CREATE "Old Label"`)
	before := s.UIDValidity("Old Label")
	c.MustOK(`DELETE "Old Label"`)
	c.MustOK(`CREATE "Old Label"`)
	if after := s.UIDValidity("Old Label"); after == before {
		t.Errorf("recreated label kept UIDVALIDITY %d", before)
	}
	if err := s.ResetUIDValidity(Inbox); err != nil {
		t.Fatal(err)
	}
	if rep := c.Cmd(`DELETE "[Gmail]/All Mail"`); rep.OK() {
		t.Error("system folder deleted")
	}
	if rep := c.Cmd("SELECT [Gmail]"); rep.OK() {
		t.Error("selected \\Noselect [Gmail]")
	}
}

func TestExpungeSemantics(t *testing.T) {
	s, c := newServer(t, Options{})
	m, uid := s.MustAdd(Inbox, imaptest.Sample("archive me"))
	c.MustOK("SELECT INBOX")
	c.MustOK(`UID STORE %d +FLAGS.SILENT (\Deleted)`, uid)
	c.MustOK("EXPUNGE")
	if s.UID(Inbox, m) != 0 || s.UID(AllMail, m) == 0 {
		t.Fatalf("expunge from INBOX should archive: inbox %d, all mail %d", s.UID(Inbox, m), s.UID(AllMail, m))
	}
	c.MustOK(`SELECT "[Gmail]/All Mail"`)
	c.MustOK(`UID STORE %d +FLAGS.SILENT (\Deleted)`, s.UID(AllMail, m))
	c.MustOK("EXPUNGE")
	if s.UID(Trash, m) == 0 {
		t.Fatal("expunge from All Mail should move to Trash")
	}
	c.MustOK(`SELECT "[Gmail]/Trash"`)
	c.MustOK(`UID STORE %d +FLAGS.SILENT (\Deleted)`, s.UID(Trash, m))
	c.MustOK("UID EXPUNGE %d", s.UID(Trash, m))
	if s.ByGmMsgID(m.GmMsgID) != nil {
		t.Fatal("expunge from Trash should delete")
	}
}

func TestObjectIDAndIntercept(t *testing.T) {
	s, c := newServer(t, Options{ObjectID: true})
	m, uid := s.MustAdd(Inbox, imaptest.Sample("ids"))
	if !strings.Contains(c.MustOK("CAPABILITY").Find("CAPABILITY"), "OBJECTID") {
		t.Fatal("OBJECTID not advertised")
	}
	if l := c.MustOK("SELECT INBOX").Find("MAILBOXID"); l == "" {
		t.Error("SELECT without MAILBOXID")
	}
	want := fmt.Sprintf("EMAILID (M%x) THREADID (T%x)", m.GmMsgID, m.GmThrID)
	if l := c.MustOK("UID FETCH %d (EMAILID THREADID)", uid).Find("EMAILID"); !strings.Contains(l, want) {
		t.Errorf("FETCH %q, want %q", l, want)
	}

	s.SetIntercept(func(cmd string) string {
		if strings.HasPrefix(cmd, "UID FETCH") {
			return "NO [UNAVAILABLE] Temporary System Error"
		}
		return ""
	})
	if rep := c.Cmd("UID FETCH 1 (FLAGS)"); rep.Status != "NO [UNAVAILABLE] Temporary System Error" {
		t.Errorf("intercepted reply %q", rep.Status)
	}
}
