package syncseal

import (
	"errors"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	p, key, err := New("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	again, err := Unlock("correct horse", p)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(key) {
		t.Fatal("unlock derived a different key")
	}
	sealed, err := Seal(key, "a#@user", "app-password")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Open(again, "a#@user", sealed); err != nil || got != "app-password" {
		t.Fatalf("open = %q, %v", got, err)
	}
	// 挪到别的同步键名下解不开。
	if _, err := Open(key, "b#@user", sealed); err == nil {
		t.Fatal("ciphertext opened under another entry")
	}
}

func TestWrongPassphrase(t *testing.T) {
	p, _, err := New("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Unlock("wrong horse!", p); !errors.Is(err, ErrWrongPassphrase) {
		t.Fatalf("err = %v, want wrong passphrase", err)
	}
}

func TestShortPassphrase(t *testing.T) {
	if _, _, err := New("short"); !errors.Is(err, ErrShortPassphrase) {
		t.Fatalf("err = %v", err)
	}
}

func TestRejectsDamagedParams(t *testing.T) {
	p, _, err := New("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	p.Memory = 1 << 30
	if _, err := Unlock("correct horse", p); err == nil {
		t.Fatal("accepted absurd memory cost")
	}
}

func TestSameKey(t *testing.T) {
	a, _, _ := New("correct horse")
	b, _, _ := New("correct horse")
	if a.SameKey(b) {
		t.Fatal("two salts compared equal")
	}
	c := a
	c.Check = "x"
	if !a.SameKey(c) {
		t.Fatal("check text should not matter")
	}
}
