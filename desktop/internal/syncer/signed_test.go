package syncer

import (
	"testing"

	"git.sr.ht/~rockorager/go-jmap/mail/email"
)

// 签名邮件: 打上签名标记, smime.p7s 不算附件; 签名之外的真附件仍显示回形针。
func TestToStoreEmailSigned(t *testing.T) {
	sig := &email.BodyPart{Type: "application/pkcs7-signature", Disposition: "attachment"}
	html := &email.BodyPart{Type: "multipart/related", SubParts: []*email.BodyPart{
		{Type: "text/html"}, {Type: "image/png", Disposition: "inline"},
	}}
	body := &email.BodyPart{Type: "multipart/alternative", SubParts: []*email.BodyPart{{Type: "text/plain"}, html}}

	got := toStoreEmail(&email.Email{HasAttachment: true, BodyStructure: &email.BodyPart{Type: "multipart/signed", SubParts: []*email.BodyPart{body, sig}}})
	if !got.Signed || got.HasAttachment {
		t.Fatalf("signed without attachment: %+v", got)
	}

	mixed := &email.BodyPart{Type: "multipart/mixed", SubParts: []*email.BodyPart{body, {Type: "application/pdf", Disposition: "attachment"}}}
	got = toStoreEmail(&email.Email{HasAttachment: true, BodyStructure: &email.BodyPart{Type: "Multipart/Signed", SubParts: []*email.BodyPart{mixed, sig}}})
	if !got.Signed || !got.HasAttachment {
		t.Fatalf("signed with attachment: %+v", got)
	}

	got = toStoreEmail(&email.Email{HasAttachment: true, BodyStructure: mixed})
	if got.Signed || !got.HasAttachment {
		t.Fatalf("unsigned: %+v", got)
	}
}
