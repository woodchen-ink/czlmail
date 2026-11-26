package syncer

import (
	"strings"
	"testing"

	"git.sr.ht/~rockorager/go-jmap/mail/email"
)

// 附件名跳过正文、内嵌图片与 S/MIME 签名部件; 没有附件时是空切片而不是 nil。
func TestAttachmentNamesFromStructure(t *testing.T) {
	tree := &email.BodyPart{Type: "multipart/signed", SubParts: []*email.BodyPart{
		{Type: "multipart/mixed", SubParts: []*email.BodyPart{
			{Type: "multipart/related", SubParts: []*email.BodyPart{
				{Type: "text/html"},
				{Type: "image/png", Disposition: "inline", Name: "logo.png"},
			}},
			{Type: "application/pdf", Disposition: "attachment", Name: " 2611200000509.pdf "},
			{Type: "application/zip", Name: "codes.zip"},
		}},
		{Type: "application/pkcs7-signature", Disposition: "attachment", Name: "smime.p7s"},
	}}
	if got := strings.Join(attachmentNames(tree, nil), ","); got != "2611200000509.pdf,codes.zip" {
		t.Fatalf("got %q", got)
	}
	if got := attachmentNames(&email.BodyPart{Type: "text/plain"}, nil); got == nil || len(got) != 0 {
		t.Fatalf("plain text: %#v", got)
	}
}
