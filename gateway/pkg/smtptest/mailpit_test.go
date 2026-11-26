package smtptest

import (
	"net/smtp"
	"strings"
	"testing"
	"time"
)

func TestMailpitReceives(t *testing.T) {
	mp := New(t)
	to := UniqueAddress("smoke")
	msg := "From: gw@example.test\r\nTo: " + to + "\r\nSubject: smtptest smoke\r\n\r\nhi\r\n"

	// Mailpit accepts any credentials over plaintext (MP_SMTP_AUTH_ALLOW_INSECURE).
	auth := plaintextAuth{smtp.PlainAuth("", "user", "secret", strings.Split(mp.SMTPAddr, ":")[0])}
	if err := smtp.SendMail(mp.SMTPAddr, auth, "gw@example.test", []string{to}, []byte(msg)); err != nil {
		t.Fatal(err)
	}

	got := mp.WaitFor("to:"+to, 1, 5*time.Second)
	if got[0].Subject != "smtptest smoke" {
		t.Errorf("subject %q", got[0].Subject)
	}
	if raw := mp.Raw(got[0].ID); !strings.Contains(string(raw), "hi") {
		t.Errorf("raw message %q", raw)
	}
}

// plaintextAuth lets net/smtp send PLAIN without TLS to a non-localhost test host
// (e.g. host.docker.internal), which it otherwise refuses.
type plaintextAuth struct{ smtp.Auth }

func (a plaintextAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	s := *server
	s.TLS = true
	return a.Auth.Start(&s)
}
