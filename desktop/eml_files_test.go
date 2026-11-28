package main

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const emlSample = "From: Bob <bob@example.com>\r\n" +
	"To: you@example.com\r\n" +
	"Subject: hello\r\n" +
	"Content-Type: multipart/related; boundary=r\r\n" +
	"\r\n" +
	"--r\r\n" +
	"Content-Type: text/html\r\n" +
	"\r\n" +
	"<img src=\"cid:img@x\">\r\n" +
	"--r\r\n" +
	"Content-Type: image/png\r\n" +
	"Content-ID: <img@x>\r\n" +
	"\r\n" +
	"\x89PNGdata\r\n" +
	"--r\r\n" +
	"Content-Type: text/html; name=page.html\r\n" +
	"Content-Disposition: attachment; filename=page.html\r\n" +
	"\r\n" +
	"<script>alert(1)</script>\r\n" +
	"--r--\r\n"

func TestEmlRegistryServesPartsWithPreviewRules(t *testing.T) {
	a := &App{}
	d, err := a.registerEml([]byte(emlSample), "acc")
	if err != nil {
		t.Fatal(err)
	}
	if !isEmlID(d.ID) || d.BlobID != d.ID || d.AccountID != "acc" || len(d.Attachments) != 2 {
		t.Fatalf("detail = %+v", d)
	}
	// 同一份原文得到同一个 id (译文缓存靠它复用)。
	if again, _ := a.registerEml([]byte(emlSample), "acc"); again.ID != d.ID {
		t.Errorf("id changed: %s vs %s", again.ID, d.ID)
	}
	// 阅读栏读邮件、取正文都走内存。
	if got, err := a.GetEmail("acc", d.ID); err != nil || got.Subject != "hello" || !got.BodyFetched {
		t.Errorf("GetEmail = %+v, %v", got, err)
	}

	serve := func(blob, typ string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		q := url.Values{"account": {"acc"}, "blob": {blob}, "type": {typ}}
		a.serveBlob(w, httptest.NewRequest("GET", BlobPath+"?"+q.Encode(), nil))
		return w
	}
	img := serve(d.Attachments[0].BlobID, "image/png")
	if img.Code != 200 || img.Header().Get("Content-Type") != "image/png" || !strings.HasPrefix(img.Body.String(), "\x89PNG") {
		t.Errorf("inline image: %d %q %q", img.Code, img.Header().Get("Content-Type"), img.Body.String())
	}
	// HTML 附件不许内联: 与服务器上的附件同一条安全边界。
	page := serve(d.Attachments[1].BlobID, "text/html")
	if page.Header().Get("Content-Type") != "application/octet-stream" || page.Header().Get("Content-Disposition") != "attachment" {
		t.Errorf("html attachment served inline: %v", page.Header())
	}
	if w := serve(d.ID+":9", "image/png"); w.Code == 200 {
		t.Error("out-of-range part served")
	}

	raw, err := a.rawMessage("acc", d.ID)
	if err != nil || string(raw) != emlSample {
		t.Errorf("raw = %v", err)
	}
	if _, err := a.emlDetail("eml:unknown"); err == nil || !strings.HasPrefix(err.Error(), "2450") {
		t.Errorf("closed file err = %v", err)
	}
}

func TestEmlRegistryKeepsRecent(t *testing.T) {
	a := &App{}
	var first string
	for i := 0; i <= emlKeep; i++ {
		d, err := a.registerEml([]byte(strings.Replace(emlSample, "hello", "hello "+strings.Repeat("x", i), 1)), "acc")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = d.ID
		}
	}
	if _, err := a.emlDetail(first); err == nil {
		t.Error("oldest file still open after exceeding the limit")
	}
}

func TestRegisterEmlRejectsNonEmail(t *testing.T) {
	a := &App{}
	if _, err := a.registerEml([]byte{}, "acc"); err == nil || !strings.HasPrefix(err.Error(), "2451") {
		t.Errorf("err = %v", err)
	}
}
