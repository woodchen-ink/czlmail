package syncer

import (
	"encoding/json"
	"testing"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 撤销把文件夹整体换回原来的一组, 垃圾邮件标记按原值设置或清除(JSON 里是 null),
// 其余关键字不碰; 没有文件夹的条目跳过。
func TestRestoreUpdates(t *testing.T) {
	got := restoreUpdates([]store.EmailPlacement{
		{ID: "a", MailboxIDs: []string{"inbox", "work"}, NotJunk: true},
		{ID: "b", MailboxIDs: []string{"junk"}, Junk: true},
		{ID: "c"},
	})
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":{"keywords/$junk":null,"keywords/$notjunk":true,"mailboxIds":{"inbox":true,"work":true}},` +
		`"b":{"keywords/$junk":true,"keywords/$notjunk":null,"mailboxIds":{"junk":true}}}`
	if string(raw) != want {
		t.Fatalf("got  %s\nwant %s", raw, want)
	}
}
