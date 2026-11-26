package imap

import (
	"testing"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imapc"
)

// Exchange Online (work and Outlook.com) folder lists, with and without the
// SPECIAL-USE attributes, in English and Chinese mailbox languages.
func TestRolesExchange(t *testing.T) {
	cases := []struct {
		name  string
		attrs []string
		want  string
	}{
		{"INBOX", nil, "inbox"},
		{"Sent Items", []string{`\HasNoChildren`, `\Sent`}, "sent"},
		{"Sent Items", []string{`\HasNoChildren`}, "sent"},
		{"Deleted Items", []string{`\HasChildren`}, "trash"},
		{"Junk Email", []string{`\HasNoChildren`}, "junk"},
		{"Drafts", nil, "drafts"},
		{"Archive", nil, "archive"},
		{"Deleted", nil, "trash"}, // Outlook.com
		{"Junk", nil, "junk"},     // Outlook.com
		{"Sent", nil, "sent"},     // Outlook.com
		{"已发送邮件", nil, "sent"},
		{"已删除邮件", nil, "trash"},
		{"垃圾邮件", nil, "junk"},
		{"草稿", nil, "drafts"},
		{"存档", nil, "archive"},
		{"Outbox", nil, ""},
		{"Conversation History", nil, ""},
		{"Inbox/Receipts", nil, ""},
	}
	for _, c := range cases {
		e := &imapc.ListEntry{Name: c.name, Delimiter: "/", Attrs: c.attrs}
		if got, _ := roleOf(e); got != c.want {
			t.Errorf("%q %v: role %q, want %q", c.name, c.attrs, got, c.want)
		}
	}
}

// One folder per role: attributes beat names, top-level names beat nested ones.
func TestAssignRolesUnique(t *testing.T) {
	list := []*imapc.ListEntry{
		{Name: "INBOX", Delimiter: "/"},
		{Name: "Inbox/Archive", Delimiter: "/"},
		{Name: "Archive", Delimiter: "/"},
		{Name: "Sent", Delimiter: "/"},
		{Name: "Sent Items", Delimiter: "/", Attrs: []string{`\Sent`}},
		{Name: "Projects/Junk", Delimiter: "/"},
		{Name: "Junk Email", Delimiter: "/"},
		{Name: "Trash", Delimiter: "/"},
		{Name: "Deleted Items", Delimiter: "/"},
	}
	want := []string{"inbox", "", "archive", "", "sent", "", "junk", "trash", ""}
	got := assignRoles(list)
	for i := range list {
		if got[i] != want[i] {
			t.Errorf("%s: role %q, want %q", list[i].Name, got[i], want[i])
		}
	}
}
