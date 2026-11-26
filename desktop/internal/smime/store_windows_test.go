//go:build windows

package smime

import (
	"os"
	"testing"
	"time"
)

// 用真实证书存储签名: 设置 CZLMAIL_SMIME_THUMBPRINT (与可选的 CZLMAIL_SMIME_OUT 输出 .eml 路径) 才运行。
func TestWindowsStoreSign(t *testing.T) {
	list, err := List(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range list {
		t.Logf("%s %s %v %s problem=%q", c.Thumbprint, c.Subject, c.Emails, c.NotAfter.Format("2006-01-02"), c.Problem)
	}
	tp := os.Getenv("CZLMAIL_SMIME_THUMBPRINT")
	if tp == "" {
		t.Skip("CZLMAIL_SMIME_THUMBPRINT not set")
	}
	s, err := Open(tp)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	t.Logf("chain: %d", len(s.Chain))
	out, err := SignMessage([]byte(sample), s.Sign)
	if err != nil {
		t.Fatal(err)
	}
	if p := os.Getenv("CZLMAIL_SMIME_OUT"); p != "" {
		if err := os.WriteFile(p, out, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
