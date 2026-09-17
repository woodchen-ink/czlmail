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

// 回复、转发保留原邮件的内嵌图片: 带 CID 的部件以 inline 形式发出, 正文里的 cid: 引用才能显示。
func TestBuildEmailInlineParts(t *testing.T) {
	msg := buildEmail(Draft{
		FromEmail: "wood@czl.net",
		HTMLBody:  `<img src="cid:logo@x">`,
		Attachments: []Attachment{
			{BlobID: "b1", Type: "image/png", Name: "logo.png", CID: "logo@x"},
			{BlobID: "b2", Type: "application/pdf", Name: "a.pdf"},
		},
	}, "drafts")
	if len(msg.Attachments) != 2 {
		t.Fatalf("attachments = %d", len(msg.Attachments))
	}
	if msg.Attachments[0].CID != "logo@x" || msg.Attachments[0].Disposition != "inline" {
		t.Errorf("inline part = %+v", msg.Attachments[0])
	}
	if msg.Attachments[1].Disposition != "attachment" || msg.Attachments[1].CID != "" {
		t.Errorf("regular attachment = %+v", msg.Attachments[1])
	}
}
