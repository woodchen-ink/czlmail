package syncer

import (
	"testing"

	"github.com/woodchen-ink/czlmail/desktop/internal/store"
)

// 发出去的邮件必须带 From 头, 否则收件人看到的是"无发件人"。
func TestBuildEmailSetsFrom(t *testing.T) {
	msg := buildEmail(Draft{
		AccountID: "a", IdentityID: "i", FromEmail: "wood@czl.net", FromName: "Wood",
		To: []store.Address{{Email: "zn@czl.net"}}, Subject: "hi", TextBody: "x",
	}, "drafts")
	if len(msg.From) != 1 || msg.From[0].Email != "wood@czl.net" || msg.From[0].Name != "Wood" {
		t.Fatalf("from = %+v", msg.From)
	}
}
