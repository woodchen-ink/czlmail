package main

import (
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmaptest"
)

// 场景 7: 新邮件通知、托盘未读、"自己发的信不通知" 覆盖所有登录。

func TestMultiSignInNewMailNotificationAndUnread(t *testing.T) {
	ts := newTwoServers(t)
	ta := newTestApp(t)
	s2 := ts.signIn(ta)
	if n := ta.a.unreadTotal(); n != 2 {
		t.Fatalf("unread total = %d, want 2", n)
	}

	id := ts.twoA.AddEmail(jmaptest.Email{Subject: "news for carol", From: "y@ext.test"})
	waitFor(t, "notification for s2's new mail", func() bool { return len(ta.notes.all()) == 1 })
	n := ta.notes.all()[0]
	if n.ActionID != s2+"~a/"+id || n.Title != "y@ext.test" {
		t.Fatalf("notification = %+v, want action %s~a/%s", n, s2, id)
	}
	waitFor(t, "unread event with 3", func() bool { return ta.a.unreadTotal() == 3 })
	waitFor(t, "tray unread pushed", func() bool { return ta.events.count(EventUnread) > 0 })
}

// 附加登录收到自己 (它自己的发件身份) 发出的信时不通知。
func TestMultiSignInOwnIdentityNotNotified(t *testing.T) {
	ts := newTwoServers(t)
	ta := newTestApp(t)
	ts.signIn(ta)
	ts.twoA.AddEmail(jmaptest.Email{Subject: "note to self", From: "carol@two.test"})
	ts.oneA.AddEmail(jmaptest.Email{Subject: "marker", From: "x@ext.test"})
	waitFor(t, "notification for the marker", func() bool { return len(ta.notes.all()) > 0 })
	time.Sleep(300 * time.Millisecond)
	for _, n := range ta.notes.all() {
		if n.Title == "carol@two.test" {
			t.Fatalf("carol was notified of her own mail: %+v", n)
		}
	}
}

// 附加登录的登录名本身是一个地址、但不在它的发件身份里 (身份只有别名) 时, 发自登录名的信也算自己的。
// ownAddresses 包含所有登录的登录名 (第 5 步)。
func TestMultiSignInOwnUsernameNotNotified(t *testing.T) {
	ts := newTwoServers(t)
	ts.twoA.SetIdentities("c.alias@two.test")
	ta := newTestApp(t)
	ts.signIn(ta)
	ts.twoA.AddEmail(jmaptest.Email{Subject: "from my login address", From: "carol@two.test"})
	ts.oneA.AddEmail(jmaptest.Email{Subject: "marker", From: "x@ext.test"})
	waitFor(t, "notification for the marker", func() bool { return len(ta.notes.all()) > 0 })
	time.Sleep(300 * time.Millisecond)
	for _, n := range ta.notes.all() {
		if n.Title == "carol@two.test" {
			t.Fatalf("carol was notified of mail from her own login address: %+v", n)
		}
	}
}
