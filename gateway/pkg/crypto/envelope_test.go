package crypto

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, keySize) }

var row = AAD{Table: "upstreams", RowID: "aK3V9QZ7M2XPA", Column: "imap_password"}

func TestSealOpenRoundTrip(t *testing.T) {
	k, err := NewKeyring(1, key(1))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := k.Seal(PurposeUpstreamSecret, row, []byte("app-password"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sealed, "v1.") || strings.Contains(sealed, "app-password") {
		t.Fatalf("unexpected sealed form %q", sealed)
	}
	got, err := k.Open(PurposeUpstreamSecret, row, sealed)
	if err != nil || string(got) != "app-password" {
		t.Fatalf("Open = %q, %v", got, err)
	}

	again, _ := k.Seal(PurposeUpstreamSecret, row, []byte("app-password"))
	if again == sealed {
		t.Fatal("two seals of the same value must differ (random nonce)")
	}
}

func TestOpenRejectsMisuse(t *testing.T) {
	k, _ := NewKeyring(1, key(1))
	sealed, _ := k.Seal(PurposeUpstreamSecret, row, []byte("secret"))
	other, _ := NewKeyring(1, key(2))

	cases := map[string]func() ([]byte, error){
		"other row": func() ([]byte, error) {
			return k.Open(PurposeUpstreamSecret, AAD{row.Table, "other", row.Column}, sealed)
		},
		"other column": func() ([]byte, error) {
			return k.Open(PurposeUpstreamSecret, AAD{row.Table, row.RowID, "smtp_password"}, sealed)
		},
		"other purpose": func() ([]byte, error) { return k.Open(PurposeProxySecret, row, sealed) },
		"other key":     func() ([]byte, error) { return other.Open(PurposeUpstreamSecret, row, sealed) },
		"tampered": func() ([]byte, error) {
			b := []byte(sealed)
			b[len(b)-2] ^= 1
			return k.Open(PurposeUpstreamSecret, row, string(b))
		},
		"unknown version": func() ([]byte, error) { return k.Open(PurposeUpstreamSecret, row, "v9"+sealed[2:]) },
		"malformed":       func() ([]byte, error) { return k.Open(PurposeUpstreamSecret, row, "garbage") },
		"truncated":       func() ([]byte, error) { return k.Open(PurposeUpstreamSecret, row, "v1.AAAA") },
	}
	for name, open := range cases {
		if _, err := open(); !errors.Is(err, ErrOpen) {
			t.Errorf("%s: err = %v, want ErrOpen", name, err)
		}
	}
}

func TestRotation(t *testing.T) {
	oldRing, _ := NewKeyring(1, key(1))
	sealed, _ := oldRing.Seal(PurposeOAuthClient, row, []byte("client-secret"))

	ring, _ := NewKeyring(2, key(2))
	if err := ring.AddOld(1, key(1)); err != nil {
		t.Fatal(err)
	}
	if !ring.NeedsRotation(sealed) {
		t.Fatal("value sealed with v1 should need rotation")
	}
	plain, err := ring.Open(PurposeOAuthClient, row, sealed)
	if err != nil || string(plain) != "client-secret" {
		t.Fatalf("Open old = %q, %v", plain, err)
	}
	resealed, _ := ring.Seal(PurposeOAuthClient, row, plain)
	if ring.NeedsRotation(resealed) || !strings.HasPrefix(resealed, "v2.") {
		t.Fatalf("resealed %q should use v2", resealed)
	}
}

func TestKeyringValidation(t *testing.T) {
	if _, err := NewKeyring(0, key(1)); err == nil {
		t.Error("version 0 accepted")
	}
	if _, err := NewKeyring(1, key(1)[:16]); err == nil {
		t.Error("short key accepted")
	}
	k, _ := NewKeyring(1, key(1))
	if err := k.AddOld(1, key(2)); err == nil {
		t.Error("old key with the current version accepted")
	}
	if _, err := k.Seal("", row, nil); err == nil {
		t.Error("empty purpose accepted")
	}
}
