package imapc

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// script is a fake server: for each command line it receives (tag stripped and
// matched by prefix), it writes the reply lines with TAG replaced.
type script struct {
	t     *testing.T
	steps []step
}

type step struct {
	cmd   string
	reply string
}

func serve(t *testing.T, greeting string, steps ...step) *Client {
	t.Helper()
	cli, srv := net.Pipe()
	go func() {
		defer srv.Close()
		br := bufio.NewReader(srv)
		srv.Write([]byte(greeting + "\r\n"))
		for _, s := range steps {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			tag, rest, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
			if !strings.HasPrefix(rest, s.cmd) {
				t.Errorf("got command %q, want prefix %q", rest, s.cmd)
				return
			}
			srv.Write([]byte(strings.ReplaceAll(s.reply, "TAG", tag)))
		}
		io := make([]byte, 1)
		br.Read(io)
	}()
	c, err := Dial(context.Background(), func(context.Context, string, string) (net.Conn, error) { return cli, nil },
		"example.test:143", Options{TLS: TLSNone, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestFetchParsing(t *testing.T) {
	refs := "References: <a@x> <b@x>\r\n\r\n"
	fetch := "* 1 FETCH (UID 7 FLAGS (\\Seen $Label1) INTERNALDATE \"17-Jul-1996 02:44:25 -0700\" RFC822.SIZE 4286 MODSEQ (12345) " +
		"X-GUID 0123abcd EMAILID (M6d99ac3275bb4e) THREADID (T64b478a75b7ea9) X-GM-MSGID 1278455344230334865 " +
		"ENVELOPE (\"Wed, 17 Jul 1996 02:23:25 -0700\" \"=?utf-8?q?Hi?=\" ((\"Terry\" NIL \"gray\" \"cac.example\")) NIL NIL " +
		"((NIL NIL \"imap\" \"cac.example\")) NIL NIL \"<p@x>\" \"<m@x>\") " +
		"BODYSTRUCTURE ((\"TEXT\" \"PLAIN\" (\"CHARSET\" \"US-ASCII\") NIL NIL \"7BIT\" 1152 23 NIL NIL NIL NIL)" +
		"(\"APPLICATION\" \"PDF\" (\"NAME\" \"a b.pdf\") NIL NIL \"BASE64\" 4554 NIL (\"attachment\" (\"filename\" \"a b.pdf\")) NIL NIL) \"MIXED\" (\"BOUNDARY\" \"x\") NIL NIL NIL) " +
		"BODY[HEADER.FIELDS (REFERENCES)] {" + itoa(len(refs)) + "}\r\n" + refs + ")\r\n"
	c := serve(t, "* OK [CAPABILITY IMAP4rev1 LITERAL+ CONDSTORE] hi",
		step{"UID FETCH 7 (UID", fetch + "TAG OK done\r\n"})
	var got *Message
	err := c.Fetch(context.Background(), "7", []string{"UID", "FLAGS"}, FetchOptions{UID: true}, func(m *Message) { got = m }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.UID != 7 || got.ModSeq != 12345 || got.Size != 4286 || got.XGUID != "0123abcd" || got.EmailID != "M6d99ac3275bb4e" || got.ThreadID != "T64b478a75b7ea9" {
		t.Fatalf("%+v", got)
	}
	if strings.Join(got.Flags, ",") != `\Seen,$Label1` {
		t.Fatalf("flags %v", got.Flags)
	}
	if got.InternalDate.UTC().Format(time.RFC3339) != "1996-07-17T09:44:25Z" {
		t.Fatalf("date %v", got.InternalDate)
	}
	if e := got.Envelope; e.Subject != "=?utf-8?q?Hi?=" || e.From[0].Name != "Terry" || e.From[0].Email() != "gray@cac.example" || e.To[0].Email() != "imap@cac.example" || e.MessageID != "<m@x>" || e.InReplyTo != "<p@x>" {
		t.Fatalf("envelope %+v", e)
	}
	b := got.Body
	if b.Path != "" || b.Subtype != "mixed" || len(b.Parts) != 2 || b.Parts[0].Path != "1" || b.Parts[1].Path != "2" {
		t.Fatalf("body %+v", b)
	}
	if p := b.Parts[1]; p.MediaType() != "application/pdf" || p.Disposition != "attachment" || p.Filename() != "a b.pdf" || p.Encoding != "base64" {
		t.Fatalf("part %+v", p)
	}
	if got.Sections["HEADER.FIELDS (REFERENCES)"] != refs {
		t.Fatalf("sections %q", got.Sections)
	}
	if got.Extra["X-GM-MSGID"].Num != 1278455344230334865 {
		t.Fatalf("extra %v", got.Extra)
	}
}

func TestSelectQResync(t *testing.T) {
	c := serve(t, "* OK hi",
		step{`SELECT "INBOX" (QRESYNC (67890007 20050715194045000))`,
			"* 314 EXISTS\r\n* OK [UIDVALIDITY 67890007] x\r\n* OK [UIDNEXT 567] x\r\n* OK [HIGHESTMODSEQ 90060115205545359] x\r\n" +
				"* OK [MAILBOXID (F2212ea87)] x\r\n* VANISHED (EARLIER) 41,43:45\r\n* 49 FETCH (UID 117 FLAGS (\\Seen) MODSEQ (90060115194045001))\r\n" +
				"TAG OK [READ-WRITE] done\r\n"})
	mb, err := c.Select(context.Background(), "INBOX", SelectOptions{QResync: &QResync{UIDValidity: 67890007, ModSeq: 20050715194045000}})
	if err != nil {
		t.Fatal(err)
	}
	if mb.Exists != 314 || mb.UIDValidity != 67890007 || mb.UIDNext != 567 || mb.HighestModSeq != 90060115205545359 || mb.MailboxID != "F2212ea87" {
		t.Fatalf("%+v", mb)
	}
	if mb.Vanished.Compact() != "41,43:45" || len(mb.Changed) != 1 || mb.Changed[0].UID != 117 {
		t.Fatalf("vanished %v changed %v", mb.Vanished, mb.Changed)
	}
}

func TestListStatusAndMove(t *testing.T) {
	c := serve(t, "* OK [CAPABILITY IMAP4rev1 SPECIAL-USE LIST-STATUS CONDSTORE MOVE UIDPLUS] hi",
		step{`LIST "" "*" RETURN (SPECIAL-USE STATUS (MESSAGES UIDNEXT UIDVALIDITY UNSEEN HIGHESTMODSEQ))`,
			"* LIST (\\HasNoChildren) \"/\" INBOX\r\n* STATUS INBOX (MESSAGES 3 UIDNEXT 9 UIDVALIDITY 5 UNSEEN 1 HIGHESTMODSEQ 77)\r\n" +
				"* LIST (\\HasNoChildren \\Sent) \"/\" \"Sent &AMk-l&AOk-ments\"\r\n* STATUS \"Sent &AMk-l&AOk-ments\" (MESSAGES 0 UIDNEXT 1 UIDVALIDITY 6 UNSEEN 0 HIGHESTMODSEQ 1)\r\n" +
				"TAG OK done\r\n"},
		step{`UID MOVE 4:5 "Sent &AMk-l&AOk-ments"`, "* OK [COPYUID 6 4:5 10:11] moved\r\n* 1 EXPUNGE\r\n* 1 EXPUNGE\r\nTAG OK done\r\n"},
	)
	list, err := c.List(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[1].Name != "Sent Éléments" || !list[1].HasAttr(`\sent`) || list[1].Status.UIDValidity != 6 || list[0].Status.HighestModSeq != 77 {
		t.Fatalf("%+v %+v", list[0], list[1])
	}
	cu, err := c.Move(context.Background(), UIDSet{4, 5}, "Sent Éléments")
	if err != nil || cu.Map()[5] != 11 || cu.UIDValidity != 6 {
		t.Fatalf("%+v %v", cu, err)
	}
}

func TestStatusErrorCode(t *testing.T) {
	c := serve(t, "* OK hi",
		step{"CAPABILITY", "* CAPABILITY IMAP4rev1\r\nTAG OK\r\n"},
		step{`LOGIN "u" "p\"w"`, "TAG NO [AUTHENTICATIONFAILED] Authentication failed.\r\n"})
	err := c.Login(context.Background(), "u", `p"w`)
	se, ok := err.(*StatusError)
	if !ok || se.Code != "AUTHENTICATIONFAILED" || se.Status != "NO" {
		t.Fatalf("%v", err)
	}
}

func TestUTF7AndSets(t *testing.T) {
	for _, s := range []string{"INBOX", "Entwürfe", "已发送", "a&b", "~peter/mail/台北/日本語"} {
		if got := DecodeMailbox(EncodeMailbox(s)); got != s {
			t.Errorf("%q -> %q -> %q", s, EncodeMailbox(s), got)
		}
	}
	if EncodeMailbox("~peter/mail/台北/日本語") != "~peter/mail/&U,BTFw-/&ZeVnLIqe-" {
		t.Errorf("rfc example: %s", EncodeMailbox("~peter/mail/台北/日本語"))
	}
	set, err := ParseUIDSet("9,1:3,5")
	if err != nil || set.Compact() != "1:3,5,9" {
		t.Fatalf("%v %v", set, err)
	}
}

func TestStreamedLiteral(t *testing.T) {
	body := "Subject: x\r\n\r\nhello\r\n"
	c := serve(t, "* OK hi",
		step{"UID FETCH 3 (UID BODY.PEEK[])", "* 1 FETCH (UID 3 BODY[] {" + itoa(len(body)) + "}\r\n" + body + ")\r\nTAG OK\r\n"})
	var buf bytes.Buffer
	found, err := c.FetchBody(context.Background(), 3, "", &buf)
	if err != nil || !found || buf.String() != body {
		t.Fatalf("%v %v %q", found, err, buf.String())
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
