package service

import (
	"bytes"
	"errors"
	"testing"

	"gorm.io/gorm"

	"github.com/woodchen-ink/czlmail/gateway/database"
	"github.com/woodchen-ink/czlmail/gateway/pkg/crypto"
)

func testKey(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := database.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close(db) })
	return db
}

func TestLoadKeyringFirstRunThenSameKey(t *testing.T) {
	db := openTestDB(t)
	ring, err := LoadKeyring(db, testKey(1))
	if err != nil || ring.Current() != 1 {
		t.Fatalf("first run: ring=%v err=%v", ring, err)
	}
	ring, err = LoadKeyring(db, testKey(1))
	if err != nil || ring.Current() != 1 {
		t.Fatalf("second run: ring=%v err=%v", ring, err)
	}
}

func TestLoadKeyringRejectsOtherKey(t *testing.T) {
	db := openTestDB(t)
	if _, err := LoadKeyring(db, testKey(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeyring(db, testKey(2)); !errors.Is(err, ErrWrongMasterKey) {
		t.Fatalf("err = %v, want ErrWrongMasterKey", err)
	}
}

type testSecret struct {
	PublicID string `gorm:"primaryKey"`
	Password string
}

func TestRotateMasterKey(t *testing.T) {
	db := openTestDB(t)
	if err := db.AutoMigrate(&testSecret{}); err != nil {
		t.Fatal(err)
	}
	RegisterSealedColumn(SealedColumn{Table: "test_secrets", IDColumn: "public_id", Column: "password", Purpose: crypto.PurposeUpstreamSecret})

	ring, err := LoadKeyring(db, testKey(1))
	if err != nil {
		t.Fatal(err)
	}
	seal := func(id, plain string) {
		s, err := ring.Seal(crypto.PurposeUpstreamSecret, crypto.AAD{Table: "test_secrets", RowID: id, Column: "password"}, []byte(plain))
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&testSecret{PublicID: id, Password: s}).Error; err != nil {
			t.Fatal(err)
		}
	}
	seal("a1", "first")
	seal("a2", "second")
	if err := db.Create(&testSecret{PublicID: "a3"}).Error; err != nil { // empty column is skipped
		t.Fatal(err)
	}

	if _, err := RotateMasterKey(db, testKey(9), testKey(2)); err == nil {
		t.Fatal("rotation with a wrong old key must fail")
	}
	n, err := RotateMasterKey(db, testKey(1), testKey(2))
	if err != nil || n != 2 {
		t.Fatalf("rotate: n=%d err=%v", n, err)
	}

	if _, err := LoadKeyring(db, testKey(1)); !errors.Is(err, ErrWrongMasterKey) {
		t.Fatalf("old key still accepted: %v", err)
	}
	ring2, err := LoadKeyring(db, testKey(2))
	if err != nil || ring2.Current() != 2 {
		t.Fatalf("new key: ring=%v err=%v", ring2, err)
	}
	var row testSecret
	db.First(&row, "public_id = ?", "a2")
	plain, err := ring2.Open(crypto.PurposeUpstreamSecret, crypto.AAD{Table: "test_secrets", RowID: "a2", Column: "password"}, row.Password)
	if err != nil || string(plain) != "second" {
		t.Fatalf("after rotation: %q %v", plain, err)
	}
}
