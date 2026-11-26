package service

import "testing"

func TestScopesGranted(t *testing.T) {
	ms := []string{"offline_access", "openid", "email", "https://outlook.office.com/IMAP.AccessAsUser.All", "https://outlook.office.com/SMTP.Send"}
	cases := []struct {
		granted string
		wanted  []string
		ok      bool
	}{
		{"", ms, true},
		{"https://outlook.office.com/IMAP.AccessAsUser.All https://outlook.office.com/SMTP.Send", ms, true},
		{"https://outlook.office.com/imap.accessasuser.all https://outlook.office.com/smtp.send openid", ms, true},
		{"IMAP.AccessAsUser.All SMTP.Send", ms, true},
		{"https://outlook.office.com/IMAP.AccessAsUser.All", ms, false},
		{"https://graph.microsoft.com/SMTP.Send https://outlook.office.com/IMAP.AccessAsUser.All", ms, false},
		{"https://mail.google.com/", []string{"https://mail.google.com/"}, true},
		{"https://www.googleapis.com/auth/userinfo.email", []string{"https://mail.google.com/"}, false},
	}
	for _, c := range cases {
		if got := scopesGranted(c.granted, c.wanted); got != c.ok {
			t.Errorf("granted %q: %v, want %v", c.granted, got, c.ok)
		}
	}
}
