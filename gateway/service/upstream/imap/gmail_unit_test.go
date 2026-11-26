package imap

import (
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
	"github.com/woodchen-ink/czlmail/gateway/service/upstream"
)

func TestGmailRawQuery(t *testing.T) {
	after := time.Unix(1700000000, 0)
	yes := true
	cases := []struct {
		f    upstream.SearchFilter
		want string
	}{
		{upstream.SearchFilter{}, ""},
		{upstream.SearchFilter{Text: "quarterly  report"}, "quarterly report"},
		{upstream.SearchFilter{From: "Ann Lee", Subject: "re: plan"}, `from:"Ann Lee" subject:"re: plan"`},
		{upstream.SearchFilter{To: "a@b.test", Cc: "c@d.test", Bcc: "e@f.test"}, "to:a@b.test cc:c@d.test bcc:e@f.test"},
		// Syntax in values cannot change the query.
		{upstream.SearchFilter{Subject: `x") OR (in:trash`}, `subject:"x OR in:trash"`},
		{upstream.SearchFilter{After: &after, MinSize: 100, MaxSize: 2000, HasAttachment: &yes}, "after:1699999999 larger:99 smaller:2000 has:attachment"},
	}
	for _, c := range cases {
		if got := GmailRawQuery(c.f); got != c.want {
			t.Errorf("GmailRawQuery(%+v) = %q, want %q", c.f, got, c.want)
		}
	}
}

func TestGmailLabelParsing(t *testing.T) {
	m := &imapc.Message{Flags: []string{`\Draft`}, Extra: map[string]imapc.Value{"X-GM-LABELS": {Kind: imapc.KindList, List: []imapc.Value{
		{Kind: imapc.KindString, Str: `\Inbox`}, {Kind: imapc.KindString, Str: "Work Projects"}, {Kind: imapc.KindAtom, Str: `\Important`},
		{Kind: imapc.KindString, Str: "&ZeVnLIqe-"}, // modified UTF-7: 日本語
	}}}}
	got, ok := gmLabels(m)
	want := []string{`\Inbox`, "Work Projects", `\Important`, "日本語", `\Draft`}
	if !ok || len(got) != len(want) {
		t.Fatalf("labels %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("labels %q, want %q", got, want)
		}
	}
	if canonicalLabel(`\Drafts`) != gmDraft || canonicalLabel(`\INBOX`) != gmInbox {
		t.Fatal("system labels not canonical")
	}
	if !hiddenLabel(`\Important`) || !hiddenLabel(`\Starred`) || hiddenLabel("Work") {
		t.Fatal("hidden labels")
	}
	if a, ok := labelArg("Work Projects").(imapc.MailboxName); !ok || a != "Work Projects" {
		t.Fatalf("user label arg %#v must be quoted", labelArg("Work Projects"))
	}
	if _, ok := labelArg(`\Inbox`).(imapc.Atom); !ok {
		t.Fatal("system label arg must be an atom")
	}
}
