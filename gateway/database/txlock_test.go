package database

import (
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"
)

// A transaction that reads, then writes after another connection committed,
// must not fail with SQLITE_BUSY (the read-to-write upgrade in WAL mode).
func TestTransactionReadThenWrite(t *testing.T) {
	db, err := openFile(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { Close(db) })
	type row struct{ ID, N int }
	if err := db.AutoMigrate(&row{}); err != nil {
		t.Fatal(err)
	}
	db.Create(&row{ID: 1})

	other := make(chan error, 1)
	err = db.Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&row{}).Count(&n).Error; err != nil {
			return err
		}
		go func() { other <- db.Create(&row{ID: 2}).Error }()
		time.Sleep(100 * time.Millisecond) // the other writer commits (deferred) or waits (immediate)
		return tx.Create(&row{ID: 3}).Error
	})
	if err != nil {
		t.Fatalf("transaction: %v", err)
	}
	if err := <-other; err != nil {
		t.Fatalf("other writer: %v", err)
	}
}
