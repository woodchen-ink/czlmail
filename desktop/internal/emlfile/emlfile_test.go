package emlfile

import (
	"strings"
	"testing"
)

func crlf(s string) []byte { return []byte(strings.ReplaceAll(s, "\n", "\r\n")) }

func TestParseAlternativeWithInlineImageAndAttachments(t *testing.T) {
	raw := crlf(`From: =?UTF-8?B?5byg5LiJ?= <zhang@example.com>
To: Bob <bob@example.com>, carol@example.com
Cc: dave@example.com
Subject: =?UTF-8?B?5rWL6K+V6YKu5Lu2?=
Date: Fri, 27 Nov 2026 10:00:00 +0800
Message-ID: <abc@example.com>
In-Reply-To: <parent@example.com>
List-Unsubscribe: <https://example.com/u>
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="mix"

--mix
Content-Type: multipart/related; boundary="rel"

--rel
Content-Type: multipart/alternative; boundary="alt"

--alt
Content-Type: text/plain; charset=utf-8

Hello plain
--alt
Content-Type: text/html; charset=utf-8

<p>Hello <img src="cid:logo@x"></p>
--alt--
--rel
Content-Type: image/png
Content-ID: <logo@x>
Content-Transfer-Encoding: base64

iVBORw0KGgo=
--rel--
--mix
Content-Type: application/pdf
Content-Disposition: attachment; filename*=UTF-8''%E6%8A%A5%E5%91%8A.pdf
Content-Transfer-Encoding: base64

JVBERi0=
--mix
Content-Type: message/rfc822
Content-Disposition: attachment; filename="inner.eml"

Subject: inner
From: x@example.com

inner body
--mix--
`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "测试邮件" {
		t.Errorf("subject = %q", m.Subject)
	}
	if len(m.From) != 1 || m.From[0].Name != "张三" || m.From[0].Email != "zhang@example.com" {
		t.Errorf("from = %+v", m.From)
	}
	if len(m.To) != 2 || m.To[1].Email != "carol@example.com" || len(m.CC) != 1 {
		t.Errorf("to = %+v cc = %+v", m.To, m.CC)
	}
	if m.MessageID != "abc@example.com" || m.InReplyTo != "parent@example.com" {
		t.Errorf("ids = %q %q", m.MessageID, m.InReplyTo)
	}
	if m.Date.IsZero() {
		t.Error("date not parsed")
	}
	if strings.TrimSpace(m.Text) != "Hello plain" || !strings.Contains(m.HTML, "cid:logo@x") {
		t.Errorf("text = %q html = %q", m.Text, m.HTML)
	}
	if len(m.Parts) != 3 {
		t.Fatalf("parts = %d", len(m.Parts))
	}
	img, pdf, inner := m.Parts[0], m.Parts[1], m.Parts[2]
	if img.Type != "image/png" || img.CID != "logo@x" || !img.Inline || string(img.Data[:4]) != "\x89PNG" {
		t.Errorf("inline image = %+v", img)
	}
	if pdf.Name != "报告.pdf" || pdf.Inline || string(pdf.Data) != "%PDF-" {
		t.Errorf("pdf = %+v %q", pdf, pdf.Data)
	}
	if inner.Type != "message/rfc822" || inner.Name != "inner.eml" || !strings.Contains(string(inner.Data), "inner body") {
		t.Errorf("inner = %+v", inner)
	}
	found := false
	for _, h := range m.Headers {
		if h.Name == "List-Unsubscribe" && strings.Contains(h.Value, "https://example.com/u") {
			found = true
		}
	}
	if !found {
		t.Error("headers missing List-Unsubscribe")
	}

	d := m.Detail("acc", "eml:x", "eml:x", func(i int) string { return "eml:x:" + string(rune('0'+i)) })
	if !d.HasAttachment || len(d.AttachmentNames) != 2 || d.Attachments[0].BlobID != "eml:x:0" || !d.BodyFetched || !d.MDNSent {
		t.Errorf("detail = %+v", d)
	}
}

func TestParseLegacyCharsetAndHTMLOnly(t *testing.T) {
	// GBK 编码的「你好」, 正文只有 HTML。
	raw := []byte("Subject: =?GBK?B?xOO6ww==?=\r\nFrom: a@example.com\r\nContent-Type: text/html; charset=GBK\r\n\r\n<p>\xc4\xe3\xba\xc3</p>\r\n")
	m, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "你好" || !strings.Contains(m.HTML, "你好") {
		t.Errorf("subject = %q html = %q", m.Subject, m.HTML)
	}
	if strings.TrimSpace(m.Text) != "你好" {
		t.Errorf("text from html = %q", m.Text)
	}
}

func TestParseSignedAndBadAddress(t *testing.T) {
	raw := crlf(`From: not an address at all
Subject: signed
Content-Type: multipart/signed; protocol="application/pkcs7-signature"; boundary="s"

--s
Content-Type: text/plain

body
--s
Content-Type: application/pkcs7-signature; name=smime.p7s
Content-Transfer-Encoding: base64

MIIB
--s--
`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !m.Signed || len(m.From) != 1 || m.From[0].Name == "" {
		t.Errorf("signed = %v from = %+v", m.Signed, m.From)
	}
	d := m.Detail("acc", "id", "raw", func(int) string { return "p" })
	if d.HasAttachment {
		t.Error("signature part counted as attachment")
	}
}

func TestParseRejectsNonEmail(t *testing.T) {
	if _, err := Parse([]byte("")); err == nil {
		t.Error("empty file parsed")
	}
}

func TestParseKeepsWhatItCanFromDamagedMail(t *testing.T) {
	cases := map[string]string{
		"bad base64":  "Subject: a\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Type: text/plain\r\n\r\nhello body\r\n--b\r\nContent-Type: application/pdf\r\nContent-Transfer-Encoding: base64\r\n\r\n!!!notbase64!!!\r\n--b--\r\n",
		"truncated":   "Subject: a\r\nContent-Type: multipart/mixed; boundary=b\r\n\r\n--b\r\nContent-Type: text/plain\r\n\r\nhello body\r\n--b\r\nContent-Type: text/plain\r\n\r\nmore",
		"no boundary": "Subject: a\r\nContent-Type: multipart/mixed\r\n\r\nhello body\r\n",
	}
	for name, raw := range cases {
		m, err := Parse([]byte(raw))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !strings.Contains(m.Text, "hello body") {
			t.Errorf("%s: text = %q", name, m.Text)
		}
	}
}

func TestParseUnlabelledGBKHeadersAndReceivedDate(t *testing.T) {
	// 发件人名字与主题直接写 GBK 字节, 没有 RFC 2047 编码; 也没有 Date 头。
	raw := "From: \xd5\xc5\xc8\xfd <zhang@example.com>\r\n" +
		"Subject: \xc4\xe3\xba\xc3\r\n" +
		"Received: from mx.example.com by mail.example.com; Fri, 27 Nov 2026 10:00:00 +0800\r\n" +
		"Content-Type: multipart/mixed; boundary=b\r\n\r\n" +
		"--b\r\nContent-Type: text/plain\r\n\r\nbody\r\n" +
		"--b\r\nContent-Type: message/rfc822\r\n\r\nSubject: Inner plan\r\nFrom: x@example.com\r\n\r\ninner\r\n--b--\r\n"
	m, err := Parse([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if m.Subject != "你好" {
		t.Errorf("subject = %q", m.Subject)
	}
	if len(m.From) != 1 || m.From[0].Email != "zhang@example.com" || m.From[0].Name != "张三" {
		t.Errorf("from = %+v", m.From)
	}
	if m.Date.IsZero() || m.Date.Year() != 2026 {
		t.Errorf("date = %v", m.Date)
	}
	if len(m.Parts) != 1 || m.Parts[0].Name != "Inner plan.eml" {
		t.Errorf("parts = %+v", m.Parts)
	}
}
