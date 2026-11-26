package oauth

import (
	"testing"
	"time"
)

func TestSecretStateAt(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { x := now.Add(d); return &x }
	cases := []struct {
		exp  *time.Time
		want SecretState
	}{
		{nil, SecretUnknown},
		{at(180 * 24 * time.Hour), SecretOK},
		{at(30*24*time.Hour + time.Second), SecretOK},
		{at(30 * 24 * time.Hour), SecretExpiring},
		{at(time.Hour), SecretExpiring},
		{at(0), SecretExpired},
		{at(-24 * time.Hour), SecretExpired},
	}
	for _, c := range cases {
		if got := SecretStateAt(c.exp, now); got != c.want {
			t.Errorf("expiry %v: %s, want %s", c.exp, got, c.want)
		}
	}
}
