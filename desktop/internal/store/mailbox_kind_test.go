package store

import "testing"

func TestInferMailboxKind(t *testing.T) {
	cases := []struct{ role, name, want string }{
		{"inbox", "随便什么名字", KindInbox}, // role 优先于名称
		{"", "Deleted Items", KindTrash},
		{"", "Junk Mail", KindJunk},
		{"", "Sent Items", KindSent},
		{"", "已发送", KindSent},
		{"", "[Gmail]/Spam", KindJunk}, // 层级名只看最后一段
		{"", "INBOX.Trash", KindTrash},
		{"", "Sent to accountant", KindCustom}, // 整名匹配, 不误伤自建文件夹
		{"", "Notes", KindCustom},
		{"", "v1.0 release", KindCustom},
	}
	for _, c := range cases {
		if got := InferMailboxKind(c.role, c.name); got != c.want {
			t.Errorf("InferMailboxKind(%q, %q) = %q, want %q", c.role, c.name, got, c.want)
		}
	}
}

// 服务器已声明某类别时, 同名推断不得再生效, 否则删除可能移进错误的回收站。
func TestAssignKindsDeclaredRoleWins(t *testing.T) {
	boxes := []Mailbox{
		{ID: "1", Name: "Deleted Items", Role: "trash"},
		{ID: "2", Name: "Trash"},
		{ID: "3", Name: "Spam"},
		{ID: "4", Name: "Junk"},
	}
	assignKinds(boxes)

	want := map[string]string{"1": KindTrash, "2": KindCustom, "3": KindJunk, "4": KindCustom}
	for _, b := range boxes {
		if b.Kind != want[b.ID] {
			t.Errorf("mailbox %s (%s) kind = %q, want %q", b.ID, b.Name, b.Kind, want[b.ID])
		}
	}
}
