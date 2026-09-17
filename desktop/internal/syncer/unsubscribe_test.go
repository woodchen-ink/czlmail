package syncer

import (
	"testing"

	"git.sr.ht/~rockorager/go-jmap/mail/email"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

func TestParseUnsubscribe(t *testing.T) {
	cases := []struct {
		headers []*email.Header
		want    store.Unsubscribe
	}{
		{nil, store.Unsubscribe{}},
		{[]*email.Header{
			{Name: "List-Unsubscribe", Value: " <mailto:u@list.example?subject=unsub>,\r\n <https://list.example/u/1>"},
			{Name: "List-Unsubscribe-Post", Value: " List-Unsubscribe=One-Click"},
		}, store.Unsubscribe{HTTP: "https://list.example/u/1", Mailto: "mailto:u@list.example?subject=unsub", OneClick: true}},
		// 一键退订只接受 https。
		{[]*email.Header{
			{Name: "list-unsubscribe", Value: "<http://list.example/u>"},
			{Name: "List-Unsubscribe-Post", Value: "List-Unsubscribe=One-Click"},
		}, store.Unsubscribe{HTTP: "http://list.example/u"}},
	}
	for i, c := range cases {
		if got := ParseUnsubscribe(c.headers); got != c.want {
			t.Errorf("case %d: got %+v want %+v", i, got, c.want)
		}
	}
}

func TestReceiptAddress(t *testing.T) {
	for in, want := range map[string]string{
		"Alice <alice@example.com>": "alice@example.com",
		"bob@example.com":           "bob@example.com",
		"not an address":            "",
	} {
		if got := receiptAddress(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}
