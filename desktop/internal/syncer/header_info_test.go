package syncer

import (
	"testing"

	"git.sr.ht/~rockorager/go-jmap/mail/email"
)

func TestParseHeaderInfo(t *testing.T) {
	headers := []*email.Header{
		{Name: "Delivered-To", Value: " me@example.com"},
		{Name: "Authentication-Results", Value: " mail.example.com;\r\n\tdkim=pass header.d=mailbaby.net header.s=sel header.b=abc;\r\n\tspf=pass (mail.example.com: domain of noreply@racknerd.com designates 67.217.53.22 as permitted sender) smtp.mailfrom=noreply@racknerd.com;\r\n\tdmarc=pass header.from=racknerd.com policy.dmarc=none;\r\n\tiprev=pass policy.iprev=67.217.53.22"},
		{Name: "X-Spam-Status", Value: " No, score=-2.90"},
		// 发件方自己写的认证结果不能用。
		{Name: "Authentication-Results", Value: " evil.example; spf=fail smtp.mailfrom=x@y"},
		{Name: "Content-Type", Value: ` multipart/alternative; boundary="b1"`},
	}
	info := ParseHeaderInfo(headers)

	if info.AuthServer != "mail.example.com" {
		t.Errorf("auth server = %q", info.AuthServer)
	}
	want := []AuthResult{
		{Method: "dkim", Result: "pass", Detail: "mailbaby.net"},
		{Method: "spf", Result: "pass", Detail: "noreply@racknerd.com"},
		{Method: "dmarc", Result: "pass", Detail: "racknerd.com", Policy: "none"},
		{Method: "iprev", Result: "pass", Detail: "67.217.53.22"},
	}
	if len(info.Auth) != len(want) {
		t.Fatalf("auth = %+v", info.Auth)
	}
	for i := range want {
		if info.Auth[i] != want[i] {
			t.Errorf("auth[%d] = %+v, want %+v", i, info.Auth[i], want[i])
		}
	}
	if info.SpamVerdict != "ham" || info.SpamScore != "-2.9" {
		t.Errorf("spam = %q %q", info.SpamVerdict, info.SpamScore)
	}
	if len(info.DeliveredTo) != 1 || info.DeliveredTo[0] != "me@example.com" {
		t.Errorf("delivered to = %v", info.DeliveredTo)
	}
	if info.ContentType != "multipart/alternative" {
		t.Errorf("content type = %q", info.ContentType)
	}
}

func TestParseHeaderInfoFallbacks(t *testing.T) {
	headers := []*email.Header{
		{Name: "Received-SPF", Value: " softfail (example.com: transitioning) client-ip=1.2.3.4; envelope-from=\"a@b.com\";"},
		{Name: "Authentication-Results", Value: " mx.example; dmarc=fail (p=REJECT sp=none) header.from=b.com"},
		{Name: "X-Spam-Flag", Value: "YES"},
	}
	info := ParseHeaderInfo(headers)
	if len(info.Auth) != 2 {
		t.Fatalf("auth = %+v", info.Auth)
	}
	if info.Auth[0] != (AuthResult{Method: "spf", Result: "softfail", Detail: "a@b.com"}) {
		t.Errorf("spf = %+v", info.Auth[0])
	}
	if info.Auth[1].Policy != "reject" {
		t.Errorf("dmarc = %+v", info.Auth[1])
	}
	if info.SpamVerdict != "spam" {
		t.Errorf("verdict = %q", info.SpamVerdict)
	}
}
