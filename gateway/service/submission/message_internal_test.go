package submission

import (
	"strings"
	"testing"
	"time"
)

func TestOutgoingStripsBccAndAddsIDs(t *testing.T) {
	raw := "From: a@x.test\r\nTo: b@y.test\r\nBcc: Hidden\r\n <c@z.test>,\r\n\td@z.test\r\nSubject: hi\r\n\r\nBcc: stays in the body\r\n"
	h, err := parseHeader([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Bcc) != 2 || h.Bcc[1].Address != "d@z.test" || h.HasDate || h.MessageID != "" {
		t.Fatalf("header %+v", h)
	}
	id := messageID(h, "x.test")
	out := outgoing([]byte(raw), h, id, time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC), true)
	if kept := string(outgoing([]byte(raw), h, id, time.Now(), false)); !strings.Contains(kept, "d@z.test") {
		t.Fatalf("Bcc dropped from the Sent copy:\n%s", kept)
	}
	s := string(out)
	if strings.Contains(s, "c@z.test") || strings.Contains(s, "d@z.test") || strings.Contains(s, "Hidden") {
		t.Fatalf("Bcc left:\n%s", s)
	}
	if !strings.HasSuffix(s, "\r\n\r\nBcc: stays in the body\r\n") || !strings.Contains(s, "Subject: hi\r\n") {
		t.Fatalf("body or other headers changed:\n%s", s)
	}
	if !strings.HasSuffix(id, "@x.test") || !strings.HasPrefix(s, "Message-ID: <"+id+">\r\nDate: ") {
		t.Fatalf("Message-ID %q / Date missing:\n%s", id, s)
	}
}

func TestEnvelopeFromHeader(t *testing.T) {
	h, _ := parseHeader([]byte("From: a@x.test\r\nSender: s@x.test\r\nTo: B <b@y.test>, c@y.test\r\nCc: B@y.test\r\nBcc: d@z.test\r\n\r\n"))
	env, serr := envelopeOf(nil, h)
	if serr != nil {
		t.Fatal(serr)
	}
	var got []string
	for _, r := range env.RcptTo {
		got = append(got, r.Email)
	}
	if env.MailFrom.Email != "s@x.test" || strings.Join(got, ",") != "b@y.test,c@y.test,d@z.test" {
		t.Fatalf("envelope %+v", env)
	}
	if _, serr := envelopeOf(nil, &header{From: h.From}); serr["type"] != "noRecipients" {
		t.Fatalf("no recipients: %v", serr)
	}
}

func TestIdentityMatching(t *testing.T) {
	for _, c := range []struct {
		identity, addr string
		ok             bool
	}{
		{"me@x.test", "ME@x.test", true},
		{"me@x.test", "you@x.test", false},
		{"*@x.test", "anyone@X.test", true},
		{"*@x.test", "anyone@sub.x.test", false},
	} {
		if got := matches(c.identity, c.addr); got != c.ok {
			t.Errorf("matches(%q, %q) = %v", c.identity, c.addr, got)
		}
	}
	for _, bad := range []string{"", "x", "@x.test", "a@", "a b@x.test", "*@"} {
		if _, err := normalizeAddress(bad); err == nil {
			t.Errorf("normalizeAddress(%q) accepted", bad)
		}
	}
}
