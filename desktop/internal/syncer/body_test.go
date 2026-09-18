package syncer

import (
	"testing"

	"git.sr.ht/~rockorager/go-jmap/mail/email"
)

// 纯文本邮件没有 text/html 部件时, 服务器会把 text/plain 部件放进 htmlBody。
// 取到的 HTML 必须为空, 否则界面会按 HTML 渲染, 换行全被折叠。
func TestJoinBodyValuesSkipsPlainTextInHTMLBody(t *testing.T) {
	msg := &email.Email{
		TextBody:   []*email.BodyPart{{PartID: "1", Type: "text/plain"}},
		HTMLBody:   []*email.BodyPart{{PartID: "1", Type: "text/plain"}},
		BodyValues: map[string]*email.BodyValue{"1": {Value: "第一行\n第二行"}},
	}
	if got := joinBodyValues(msg, msg.TextBody, ""); got != "第一行\n第二行" {
		t.Errorf("text = %q", got)
	}
	if got := joinBodyValues(msg, msg.HTMLBody, "text/html"); got != "" {
		t.Errorf("html = %q, want empty", got)
	}
}

func TestJoinBodyValuesJoinsHTMLParts(t *testing.T) {
	msg := &email.Email{
		HTMLBody: []*email.BodyPart{
			{PartID: "1", Type: "text/html"},
			{PartID: "2", Type: "image/png"},
			{PartID: "3", Type: "TEXT/HTML"},
		},
		BodyValues: map[string]*email.BodyValue{
			"1": {Value: "<p>正文</p>"},
			"2": {Value: "二进制"},
			"3": {Value: "<p>签名</p>"},
		},
	}
	want := "<p>正文</p>\n<p>签名</p>"
	if got := joinBodyValues(msg, msg.HTMLBody, "text/html"); got != want {
		t.Errorf("html = %q, want %q", got, want)
	}
}
