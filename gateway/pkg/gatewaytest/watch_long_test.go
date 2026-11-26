package gatewaytest

import (
	"os"
	"testing"
	"time"

	"github.com/woodchen-ink/czlmail/gateway/pkg/imaptest"
)

// The watcher still reports changes after a long quiet spell. Opt-in
// (GATEWAY_TEST_LONG=1): it waits six minutes.
func TestWatchAfterQuietSpell(t *testing.T) {
	if os.Getenv("GATEWAY_TEST_LONG") == "" {
		t.Skip("GATEWAY_TEST_LONG not set")
	}
	mbox := imaptest.Dovecot(t).NewUser(t)
	box := mbox.Dial(t)
	gw := Start(t)
	cred := gw.NewUser(t)
	acct := string(gw.AddIMAPUpstream(t, cred, mbox))
	h, _ := gw.App.Manager.Lookup(acct)
	db := h.Upstream.Index().DB()
	time.Sleep(6 * time.Minute)
	box.AppendSample("INBOX", "after the quiet")
	deadline := time.Now().Add(20 * time.Second)
	for {
		var n int64
		db.Table("emails").Where("subject = ?", "after the quiet").Count(&n)
		if n == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("a new message after six quiet minutes was not seen within 20 s")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
